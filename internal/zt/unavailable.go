package zt

import "context"

// unavailableBackend is a stand-in used when no real ZeroTier engine is present.
// It keeps the app (and its localhost UI) running so the user sees an actionable
// message instead of the process exiting.
type unavailableBackend struct{}

func (unavailableBackend) Name() string    { return "none" }
func (unavailableBackend) Available() bool { return true }
func (unavailableBackend) Join(context.Context, string) error {
	return errNoEngine
}
func (unavailableBackend) Leave(context.Context) error { return nil }
func (unavailableBackend) Status(context.Context) Status {
	return Status{
		State:   StateError,
		Message: "ZeroTier engine not found — install the zerotier-one package to connect",
	}
}
func (unavailableBackend) Close() error { return nil }
