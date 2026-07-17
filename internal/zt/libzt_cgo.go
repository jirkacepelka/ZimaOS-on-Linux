//go:build libzt

// Userspace ZeroTier engine backed by libzt (https://github.com/zerotier/libzt).
//
// This is the backend that makes a fully-sandboxed Flatpak — and therefore a
// Bazaar listing — possible, because ZeroTier runs entirely in userspace: no
// root, no /dev/net/tun, no CAP_NET_ADMIN.
//
// Build with:  go build -tags libzt   (requires the libzt C library + headers)
//
// VERIFICATION STATUS: this file targets libzt's documented public API
// (ZeroTierSockets.h, the zts_* functions). It is compiled only under the
// `libzt` tag and is NOT built by the default toolchain, so it has not been
// compile-checked in an environment without libzt present. When you vendor
// libzt, run `go build -tags libzt ./...` and reconcile any API drift — the
// concurrency-heavy part (the local TCP proxy) lives in internal/proxy and is
// already fully tested, so this file only needs to provide a working Dialer and
// the node lifecycle.

package zt

/*
#cgo LDFLAGS: -lzt
#include <ZeroTierSockets.h>
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/jirkacepelka/zimaos-on-linux/internal/proxy"
)

// libztBackend implements Backend on top of libzt.
type libztBackend struct {
	pinnedHost string

	startMu sync.Mutex // serialises node startup; NOT held during the online wait
	started bool

	mu        sync.Mutex // guards status/netID/proxyStop only
	status    Status
	netID     uint64
	proxyStop context.CancelFunc
}

func newLibztBackend(pinnedHost string) *libztBackend {
	return &libztBackend{
		pinnedHost: pinnedHost,
		status:     Status{State: StateStopped},
	}
}

func (l *libztBackend) Name() string    { return "libzt" }
func (l *libztBackend) Available() bool { return true }

func (l *libztBackend) Status(context.Context) Status {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.status
}

// ensureStarted brings the libzt node online exactly once. It is serialised by
// startMu but deliberately does NOT hold the status mutex during the online
// wait, so Status() stays responsive and the wait can be cancelled via ctx.
// The node identity is persisted under storageDir so the device keeps a stable
// ZeroTier address across restarts.
func (l *libztBackend) ensureStarted(ctx context.Context) error {
	l.startMu.Lock()
	defer l.startMu.Unlock()
	if l.started {
		return nil
	}

	dir, err := storageDir()
	if err != nil {
		return err
	}
	cpath := C.CString(dir)
	defer C.free(unsafe.Pointer(cpath))
	if rc := C.zts_init_from_storage(cpath); rc != C.ZTS_ERR_OK {
		return fmt.Errorf("zts_init_from_storage: rc=%d", int(rc))
	}
	if rc := C.zts_node_start(); rc != C.ZTS_ERR_OK {
		return fmt.Errorf("zts_node_start: rc=%d", int(rc))
	}

	// Wait for the node to come online (bounded, cancellable).
	deadline := time.Now().Add(30 * time.Second)
	for C.zts_node_is_online() != 1 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("libzt node did not come online in time")
		}
		time.Sleep(200 * time.Millisecond)
	}
	l.started = true
	return nil
}

// Join returns immediately; the potentially slow node startup and network join
// happen on a background goroutine so login autostart never blocks.
func (l *libztBackend) Join(_ context.Context, networkID string) error {
	id, err := strconv.ParseUint(networkID, 16, 64)
	if err != nil {
		return fmt.Errorf("invalid network ID %q: %w", networkID, err)
	}

	l.mu.Lock()
	if l.proxyStop != nil {
		l.proxyStop()
	}
	l.netID = id
	monCtx, cancel := context.WithCancel(context.Background())
	l.proxyStop = cancel
	l.mu.Unlock()

	l.setState(StateStarting, networkID, "", false, "starting ZeroTier engine")
	go l.bringUp(monCtx, id, networkID)
	return nil
}

// bringUp starts the node, joins the network, and then monitors for readiness.
func (l *libztBackend) bringUp(ctx context.Context, id uint64, networkID string) {
	if err := l.ensureStarted(ctx); err != nil {
		if ctx.Err() == nil {
			l.setError(err.Error())
		}
		return
	}
	if rc := C.zts_net_join(C.uint64_t(id)); rc != C.ZTS_ERR_OK {
		l.setError(fmt.Sprintf("zts_net_join: rc=%d", int(rc)))
		return
	}
	l.setState(StateJoining, networkID, "", false,
		"waiting for authorization — approve this device in ZimaOS (Network → Remote Login)")
	l.monitor(ctx, id, networkID)
}

// monitor waits for the network transport to become ready, then starts a local
// TCP proxy that bridges the browser to ZimaOS over the userspace stack.
func (l *libztBackend) monitor(ctx context.Context, id uint64, networkID string) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if C.zts_net_transport_is_ready(C.uint64_t(id)) != 1 {
			continue
		}

		assigned := l.assignedIPv4(id)
		if assigned == "" {
			l.setState(StateJoining, networkID, "", true, "authorized, awaiting IP assignment")
			continue
		}

		l.setState(StateDiscover, networkID, assigned, true, "locating your ZimaOS server")
		target := l.resolveTarget(ctx, assigned)
		if target == "" {
			l.setState(StateDiscover, networkID, assigned, true,
				"could not find ZimaOS automatically — set its address under Advanced")
			continue
		}

		p := proxy.New(libztDialer{}, target)
		local, err := p.ListenAndServe(ctx)
		if err != nil {
			l.setError("could not start local proxy: " + err.Error())
			return
		}
		l.setConnected(networkID, assigned, "http://"+local)
		return
	}
}

// resolveTarget returns the ZimaOS "host:port" to tunnel to. A pinned host wins;
// otherwise it scans the ZeroTier subnet over the userspace stack, reusing the
// shared discovery code with a libzt-backed dialer. The subnet mask is not
// exposed by libzt's simple API, so we assume a /24 around the assigned address
// (ZeroTier's common default); the Advanced pin covers anything unusual.
func (l *libztBackend) resolveTarget(ctx context.Context, assignedIP string) string {
	if h := strings.TrimSpace(l.pinnedHost); h != "" {
		h = strings.TrimPrefix(strings.TrimPrefix(h, "http://"), "https://")
		if !strings.Contains(h, ":") {
			h += ":80"
		}
		return h
	}
	if u := discoverZima(ctx, assignedIP+"/24", libztDialer{}); u != "" {
		return urlToHostPort(u)
	}
	return ""
}

// assignedIPv4 returns this node's IPv4 address on the network, or "".
func (l *libztBackend) assignedIPv4(id uint64) string {
	buf := (*C.char)(C.malloc(64))
	defer C.free(unsafe.Pointer(buf))
	rc := C.zts_addr_get_str(C.uint64_t(id), C.ZTS_AF_INET, buf, 64)
	if rc != C.ZTS_ERR_OK {
		return ""
	}
	s := C.GoString(buf)
	if s == "" || strings.HasPrefix(s, "0.0.0.0") {
		return ""
	}
	return s
}

func (l *libztBackend) Leave(context.Context) error {
	l.mu.Lock()
	id := l.netID
	if l.proxyStop != nil {
		l.proxyStop()
		l.proxyStop = nil
	}
	l.status = Status{State: StateStopped}
	l.mu.Unlock()

	if id != 0 {
		C.zts_net_leave(C.uint64_t(id))
	}
	return nil
}

func (l *libztBackend) Close() error {
	l.mu.Lock()
	if l.proxyStop != nil {
		l.proxyStop()
		l.proxyStop = nil
	}
	started := l.started
	l.mu.Unlock()
	if started {
		C.zts_node_stop()
	}
	return nil
}

// ---- libzt-backed net.Conn -------------------------------------------------

// libztDialer opens connections to ZimaOS through the userspace stack. It
// satisfies proxy.Dialer, so the already-tested proxy handles all the byte
// pumping; this only has to produce a working net.Conn.
type libztDialer struct{}

func (libztDialer) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("invalid port %q: %w", portStr, err)
	}

	fd := C.zts_socket(C.ZTS_AF_INET, C.ZTS_SOCK_STREAM, 0)
	if fd < 0 {
		return nil, fmt.Errorf("zts_socket failed: %d", int(fd))
	}

	chost := C.CString(host)
	defer C.free(unsafe.Pointer(chost))
	// zts_connect(fd, ipstr, port, timeout_ms); 0 = block until connected/failed.
	if rc := C.zts_connect(fd, chost, C.ushort(port), 10000); rc != C.ZTS_ERR_OK {
		C.zts_close(fd)
		return nil, fmt.Errorf("zts_connect %s: rc=%d", address, int(rc))
	}
	return &libztConn{fd: fd, remote: address}, nil
}

// libztConn adapts a libzt socket fd to net.Conn. The proxy uses only
// Read/Write/Close; the address and deadline methods are provided to satisfy
// the interface (deadlines are no-ops on the userspace socket).
type libztConn struct {
	fd     C.int
	remote string
	once   sync.Once
}

func (c *libztConn) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	n := C.zts_recv(c.fd, unsafe.Pointer(&b[0]), C.size_t(len(b)), 0)
	if n < 0 {
		return 0, fmt.Errorf("zts_recv: %d", int(n))
	}
	if n == 0 {
		return 0, net.ErrClosed // peer closed
	}
	return int(n), nil
}

func (c *libztConn) Write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	n := C.zts_send(c.fd, unsafe.Pointer(&b[0]), C.size_t(len(b)), 0)
	if n < 0 {
		return 0, fmt.Errorf("zts_send: %d", int(n))
	}
	return int(n), nil
}

func (c *libztConn) Close() error {
	c.once.Do(func() { C.zts_close(c.fd) })
	return nil
}

func (c *libztConn) LocalAddr() net.Addr  { return zaddr("libzt") }
func (c *libztConn) RemoteAddr() net.Addr { return zaddr(c.remote) }

func (c *libztConn) SetDeadline(time.Time) error      { return nil }
func (c *libztConn) SetReadDeadline(time.Time) error  { return nil }
func (c *libztConn) SetWriteDeadline(time.Time) error { return nil }

type zaddr string

func (z zaddr) Network() string { return "libzt" }
func (z zaddr) String() string  { return string(z) }

// ---- status helpers (mirrors host.go) --------------------------------------

func (l *libztBackend) setState(s State, netID, ip string, authorized bool, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.status = Status{
		State:      s,
		NetworkID:  netID,
		Authorized: authorized,
		AssignedIP: ip,
		Message:    msg,
		ZimaURL:    l.status.ZimaURL,
	}
}

func (l *libztBackend) setConnected(netID, ip, url string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.status = Status{
		State: StateConnected, NetworkID: netID, Authorized: true,
		AssignedIP: ip, ZimaURL: url, Message: "connected to ZimaOS",
	}
}

func (l *libztBackend) setError(msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.status.State = StateError
	l.status.Message = msg
}
