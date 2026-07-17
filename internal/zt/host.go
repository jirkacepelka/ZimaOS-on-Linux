package zt

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// runner executes a zerotier-cli invocation and returns its combined output.
// It is a field on hostBackend so tests can substitute a fake daemon.
type runner func(ctx context.Context, args ...string) (string, error)

// hostBackend drives a system-installed zerotier-one daemon through the
// zerotier-cli command-line tool. It runs a background poller that keeps the
// cached Status fresh and locates the ZimaOS node once the network is joined.
type hostBackend struct {
	pinnedHost string
	run        runner

	mu     sync.Mutex
	status Status
	netID  string

	cancel context.CancelFunc // stops the current poll loop
	wg     sync.WaitGroup
}

func newHostBackend(pinnedHost string) *hostBackend {
	return &hostBackend{
		pinnedHost: pinnedHost,
		run:        execCLI,
		status:     Status{State: StateStopped},
	}
}

func (h *hostBackend) Name() string { return "host-daemon" }

// Available reports whether zerotier-cli is on PATH.
func (h *hostBackend) Available() bool {
	_, err := exec.LookPath("zerotier-cli")
	return err == nil
}

func (h *hostBackend) Join(ctx context.Context, networkID string) error {
	h.mu.Lock()
	// Restart the poll loop for the (possibly new) network.
	if h.cancel != nil {
		h.cancel()
	}
	h.netID = networkID
	h.status = Status{State: StateJoining, NetworkID: networkID}
	loopCtx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.mu.Unlock()

	if out, err := h.run(ctx, "join", networkID); err != nil {
		h.setError(fmt.Sprintf("zerotier-cli join failed: %v: %s", err, out))
		return err
	}

	h.wg.Add(1)
	go h.pollLoop(loopCtx, networkID)
	return nil
}

func (h *hostBackend) Leave(ctx context.Context) error {
	h.mu.Lock()
	if h.cancel != nil {
		h.cancel()
		h.cancel = nil
	}
	netID := h.netID
	h.status = Status{State: StateStopped}
	h.mu.Unlock()

	if netID == "" {
		return nil
	}
	_, err := h.run(ctx, "leave", netID)
	return err
}

func (h *hostBackend) Status(context.Context) Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.status
}

func (h *hostBackend) Close() error {
	h.mu.Lock()
	if h.cancel != nil {
		h.cancel()
		h.cancel = nil
	}
	h.mu.Unlock()
	h.wg.Wait()
	return nil
}

// pollLoop refreshes network membership state until the context is cancelled.
// Once the network reports an assigned IP it kicks off ZimaOS discovery.
func (h *hostBackend) pollLoop(ctx context.Context, networkID string) {
	defer h.wg.Done()

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	var discovered bool
	discovered = h.pollOnce(ctx, networkID, discovered)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			discovered = h.pollOnce(ctx, networkID, discovered)
		}
	}
}

// pollOnce performs a single refresh of the membership state and updates the
// cached Status. It takes and returns whether the ZimaOS server has already
// been located, so the caller can carry that across ticks. Split out from
// pollLoop so the state machine can be unit-tested without timers.
func (h *hostBackend) pollOnce(ctx context.Context, networkID string, discovered bool) bool {
	net, err := h.listNetwork(ctx, networkID)
	if err != nil {
		h.setError(fmt.Sprintf("cannot query zerotier: %v", err))
		return discovered
	}
	if net == nil {
		h.setState(StateJoining, networkID, "", false, "waiting for network membership")
		return discovered
	}

	authorized := net.Status == "OK"
	cidr := firstIPv4(net.AssignedAddresses)
	ip := ipOnly(cidr)

	switch {
	case !authorized:
		// The most common first-run case: ZimaOS has not yet approved this
		// device on its "Connect" controller.
		msg := "waiting for authorization — approve this device in ZimaOS (Network → Remote Login)"
		if net.Status == "ACCESS_DENIED" {
			msg = "access denied by ZimaOS — approve this device in ZimaOS settings"
		}
		h.setState(StateJoining, networkID, ip, false, msg)
		return false
	case cidr == "":
		h.setState(StateJoining, networkID, "", true, "authorized, awaiting IP assignment")
		return discovered
	case !discovered:
		h.setState(StateDiscover, networkID, ip, true, "locating your ZimaOS server")
		if url := h.locateZima(ctx, cidr); url != "" {
			h.setConnected(networkID, ip, url)
			return true
		}
		return false
	default:
		// Already connected; keep the cached URL, just refresh liveness.
		h.mu.Lock()
		h.status.AssignedIP = ip
		h.status.Authorized = true
		h.mu.Unlock()
		return true
	}
}

// locateZima resolves the ZimaOS web login URL. It honours a pinned host if the
// user set one, otherwise it scans the ZeroTier subnet.
func (h *hostBackend) locateZima(ctx context.Context, assignedCIDR string) string {
	if h.pinnedHost != "" {
		return normalizeZimaURL(h.pinnedHost)
	}
	return discoverZima(ctx, assignedCIDR)
}

// ---- zerotier-cli plumbing -------------------------------------------------

// cliNetwork mirrors the subset of `zerotier-cli -j listnetworks` we use.
type cliNetwork struct {
	NWID              string   `json:"nwid"`
	Name              string   `json:"name"`
	Status            string   `json:"status"`
	AssignedAddresses []string `json:"assignedAddresses"`
}

func (h *hostBackend) listNetwork(ctx context.Context, networkID string) (*cliNetwork, error) {
	out, err := h.run(ctx, "-j", "listnetworks")
	if err != nil {
		return nil, fmt.Errorf("%v: %s", err, out)
	}
	var nets []cliNetwork
	if err := json.Unmarshal([]byte(out), &nets); err != nil {
		return nil, fmt.Errorf("parsing listnetworks: %w", err)
	}
	for i := range nets {
		if strings.EqualFold(nets[i].NWID, networkID) {
			return &nets[i], nil
		}
	}
	return nil, nil
}

// execCLI is the production runner: it shells out to the real zerotier-cli.
func execCLI(ctx context.Context, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "zerotier-cli", args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// ---- status helpers --------------------------------------------------------

func (h *hostBackend) setState(s State, netID, ip string, authorized bool, msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status = Status{
		State:      s,
		NetworkID:  netID,
		Authorized: authorized,
		AssignedIP: ip,
		Message:    msg,
		ZimaURL:    h.status.ZimaURL, // preserve if already found
	}
}

func (h *hostBackend) setConnected(netID, ip, url string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status = Status{
		State:      StateConnected,
		NetworkID:  netID,
		Authorized: true,
		AssignedIP: ip,
		ZimaURL:    url,
		Message:    "connected to ZimaOS",
	}
}

func (h *hostBackend) setError(msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status.State = StateError
	h.status.Message = msg
}
