// Package zt abstracts the ZeroTier engine that connects the local machine to
// the user's ZimaOS server.
//
// The rest of the application only ever talks to the Backend interface, so the
// engine can be swapped without touching the UI or the HTTP server. Two
// implementations are planned:
//
//   - hostBackend (host.go): drives a system-installed zerotier-one daemon via
//     the zerotier-cli tool. This works today on a normal desktop install and
//     is what you get from the AppImage / run-from-source build.
//
//   - libztBackend (libzt.go): embeds ZeroTier in userspace (no root, no TUN
//     device) so the whole thing can ship as a sandboxed Flatpak on Flathub —
//     which is what makes it show up in Bazaar. See libzt.go for status.
//
// Selection happens in New(): it prefers the embedded backend when compiled in
// and falls back to the host daemon otherwise.
package zt

import (
	"context"
	"errors"
)

var errNoEngine = errors.New("no ZeroTier engine available")

// State is a coarse connection phase, surfaced to the UI.
type State string

const (
	StateStopped   State = "stopped"     // engine not running
	StateStarting  State = "starting"    // engine coming up
	StateJoining   State = "joining"     // asked to join, awaiting authorization
	StateDiscover  State = "discovering" // joined, locating the ZimaOS node
	StateConnected State = "connected"   // ZimaOS reachable
	StateError     State = "error"       // see Status.Message
)

// Status is a snapshot of the engine, returned by Backend.Status.
type Status struct {
	State State `json:"state"`

	// NetworkID is the network we are (trying to be) a member of.
	NetworkID string `json:"network_id,omitempty"`

	// Authorized reports whether the network controller (running on ZimaOS)
	// has admitted this node. A brand-new join is unauthorized until the
	// ZimaOS side approves it.
	Authorized bool `json:"authorized"`

	// AssignedIP is this machine's address on the ZeroTier network.
	AssignedIP string `json:"assigned_ip,omitempty"`

	// ZimaURL is the fully-formed URL of the ZimaOS web login, once the
	// server has been located. Empty until StateConnected.
	ZimaURL string `json:"zima_url,omitempty"`

	// Message carries human-readable detail, especially for StateError.
	Message string `json:"message,omitempty"`
}

// Backend is the contract every ZeroTier engine implementation fulfils.
type Backend interface {
	// Name is a short identifier for diagnostics ("host-daemon", "libzt").
	Name() string

	// Available reports whether this backend can actually run in the current
	// environment (e.g. is zerotier-one installed?). New() uses this to pick.
	Available() bool

	// Join asks the engine to become a member of networkID and start looking
	// for the ZimaOS server. It returns quickly; progress is observed via
	// Status. Calling Join with a new ID switches networks.
	Join(ctx context.Context, networkID string) error

	// Leave disconnects from the current network.
	Leave(ctx context.Context) error

	// Status returns the current snapshot. It must be cheap and non-blocking.
	Status(ctx context.Context) Status

	// Close releases resources held by the backend.
	Close() error
}

// New selects the best available backend. It prefers the embedded libzt engine
// (sandbox-friendly) and falls back to the host zerotier-one daemon.
//
// pinnedHost, when non-empty, forces the ZimaOS address instead of relying on
// auto-discovery.
func New(pinnedHost string) (Backend, error) {
	candidates := []Backend{
		newLibztBackend(pinnedHost),
		newHostBackend(pinnedHost),
	}
	for _, b := range candidates {
		if b != nil && b.Available() {
			return b, nil
		}
	}
	// Nothing usable: keep the app alive with a placeholder so the UI can tell
	// the user what to install rather than the process exiting.
	return unavailableBackend{}, nil
}
