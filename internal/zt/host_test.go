package zt

import (
	"context"
	"testing"
)

// fakeDaemon returns a runner that serves canned zerotier-cli output. The
// listnetworks JSON is swappable between calls to simulate the daemon state
// changing over time.
func fakeDaemon(listnetworks *string) runner {
	return func(_ context.Context, args ...string) (string, error) {
		switch {
		case len(args) >= 1 && args[0] == "join":
			return "200 join OK", nil
		case len(args) >= 1 && args[0] == "leave":
			return "200 leave OK", nil
		case len(args) >= 2 && args[0] == "-j" && args[1] == "listnetworks":
			return *listnetworks, nil
		}
		return "", nil
	}
}

func TestPollOnce_StateMachine(t *testing.T) {
	const netID = "8056c2e21c000001"
	var out string

	h := newHostBackend("10.147.20.5") // pin ZimaOS so discovery is deterministic
	h.run = fakeDaemon(&out)

	ctx := context.Background()

	// 1. Network not yet a member -> joining.
	out = `[]`
	if got := h.pollOnce(ctx, netID, false); got {
		t.Error("expected not discovered when network absent")
	}
	if s := h.Status(ctx); s.State != StateJoining {
		t.Errorf("state = %q, want joining", s.State)
	}

	// 2. Member but awaiting authorization -> joining, unauthorized.
	out = `[{"nwid":"8056c2e21c000001","status":"REQUESTING_CONFIGURATION","assignedAddresses":[]}]`
	h.pollOnce(ctx, netID, false)
	if s := h.Status(ctx); s.State != StateJoining || s.Authorized {
		t.Errorf("got state=%q authorized=%v, want joining/false", s.State, s.Authorized)
	}

	// 3. Access denied -> distinct message.
	out = `[{"nwid":"8056c2e21c000001","status":"ACCESS_DENIED","assignedAddresses":[]}]`
	h.pollOnce(ctx, netID, false)
	if s := h.Status(ctx); s.State != StateJoining {
		t.Errorf("state = %q, want joining", s.State)
	}

	// 4. Authorized with an IP and a pinned host -> connected.
	out = `[{"nwid":"8056c2e21c000001","status":"OK","assignedAddresses":["10.147.20.42/24"]}]`
	discovered := h.pollOnce(ctx, netID, false)
	if !discovered {
		t.Fatal("expected discovered=true once authorized with pinned host")
	}
	s := h.Status(ctx)
	if s.State != StateConnected {
		t.Errorf("state = %q, want connected", s.State)
	}
	if s.AssignedIP != "10.147.20.42" {
		t.Errorf("assigned IP = %q, want 10.147.20.42", s.AssignedIP)
	}
	if s.ZimaURL != "http://10.147.20.5" {
		t.Errorf("zima URL = %q, want http://10.147.20.5", s.ZimaURL)
	}
	if !s.Authorized {
		t.Error("expected authorized=true")
	}
}

func TestPollOnce_ReauthResetsDiscovery(t *testing.T) {
	const netID = "8056c2e21c000001"
	var out string
	h := newHostBackend("10.147.20.5")
	h.run = fakeDaemon(&out)
	ctx := context.Background()

	// Start connected.
	out = `[{"nwid":"8056c2e21c000001","status":"OK","assignedAddresses":["10.147.20.42/24"]}]`
	if !h.pollOnce(ctx, netID, false) {
		t.Fatal("expected connected")
	}

	// Lose authorization -> discovery must reset to false.
	out = `[{"nwid":"8056c2e21c000001","status":"ACCESS_DENIED","assignedAddresses":[]}]`
	if h.pollOnce(ctx, netID, true) {
		t.Error("losing authorization should reset discovered to false")
	}
	if s := h.Status(ctx); s.State != StateJoining {
		t.Errorf("state = %q, want joining after deauth", s.State)
	}
}

func TestListNetwork_ParsesAndMatches(t *testing.T) {
	var out string
	h := newHostBackend("")
	h.run = fakeDaemon(&out)

	out = `[{"nwid":"AAAA0000BBBB1111","status":"OK","assignedAddresses":["1.2.3.4/24"]},
	        {"nwid":"8056c2e21c000001","status":"OK","assignedAddresses":["10.0.0.9/24"]}]`

	// Case-insensitive match on the second entry.
	net, err := h.listNetwork(context.Background(), "8056C2E21C000001")
	if err != nil {
		t.Fatal(err)
	}
	if net == nil || net.AssignedAddresses[0] != "10.0.0.9/24" {
		t.Errorf("wrong network matched: %+v", net)
	}

	// A network we are not a member of returns nil, nil.
	net, err = h.listNetwork(context.Background(), "ffffffffffffffff")
	if err != nil || net != nil {
		t.Errorf("expected nil network, got %+v (err %v)", net, err)
	}
}
