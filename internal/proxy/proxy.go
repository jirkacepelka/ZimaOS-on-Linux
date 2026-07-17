// Package proxy bridges the user's browser to a ZimaOS server that is only
// reachable over a userspace ZeroTier stack (libzt).
//
// The browser cannot speak the userspace network stack directly, so we run a
// tiny TCP proxy: it listens on 127.0.0.1 and, for every accepted connection,
// opens a matching connection to ZimaOS through a Dialer (backed by libzt) and
// pumps bytes both ways. The web UI then points the browser at the proxy's
// local address, which is transparently tunnelled to ZimaOS over ZeroTier.
//
// The proxy is deliberately transport-agnostic: it depends only on the Dialer
// interface, so it is fully testable with an ordinary net.Dialer and does not
// need libzt to be present.
package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
)

// Dialer opens a connection to an address over some transport. The libzt
// backend implements this with zts_bsd_connect; tests implement it with a
// plain net.Dialer.
type Dialer interface {
	Dial(ctx context.Context, network, address string) (net.Conn, error)
}

// DialerFunc adapts a function to the Dialer interface.
type DialerFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Dial implements Dialer.
func (f DialerFunc) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	return f(ctx, network, address)
}

// Proxy forwards local TCP connections to a fixed target through a Dialer.
type Proxy struct {
	dialer Dialer
	target string // host:port on the far (ZimaOS) side
}

// New creates a Proxy that forwards to target (e.g. "10.147.20.5:80") via d.
func New(d Dialer, target string) *Proxy {
	return &Proxy{dialer: d, target: target}
}

// ListenAndServe binds 127.0.0.1 on an OS-chosen port, begins serving in the
// background, and returns the bound address (host:port). Serving stops when ctx
// is cancelled. The caller can use the returned address to build the local URL.
func (p *Proxy) ListenAndServe(ctx context.Context) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	go func() {
		_ = p.Serve(ctx, ln)
	}()
	return ln.Addr().String(), nil
}

// Serve accepts connections on ln and forwards each one until ctx is cancelled
// or ln is closed. It always closes ln before returning.
func (p *Proxy) Serve(ctx context.Context, ln net.Listener) error {
	// Close the listener when the context is cancelled so Accept unblocks.
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	var wg sync.WaitGroup
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				wg.Wait()
				return nil
			}
			// Transient accept error; keep serving.
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.handle(ctx, conn)
		}()
	}
}

// handle bridges a single accepted connection to the target.
func (p *Proxy) handle(ctx context.Context, local net.Conn) {
	defer local.Close()

	remote, err := p.dialer.Dial(ctx, "tcp", p.target)
	if err != nil {
		return
	}
	defer remote.Close()

	// Pump both directions; unblock the other copy as soon as one side ends by
	// closing both connections.
	done := make(chan struct{}, 2)
	pipe := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		done <- struct{}{}
	}
	go pipe(remote, local)
	go pipe(local, remote)

	select {
	case <-done:
	case <-ctx.Done():
	}
	// Closing here (via the deferred Close calls) breaks the remaining copy.
}
