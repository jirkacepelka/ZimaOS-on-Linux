//go:build !libzt

// This file is the fallback used in ordinary builds. The real userspace engine
// lives in libzt_cgo.go and is compiled only with `-tags libzt` (which also
// links against the libzt C library). Keeping them build-tag-separated means
// the default binary needs no C toolchain and stays tiny.

package zt

import (
	"context"
	"errors"
)

var errNotImplemented = errors.New("libzt backend not compiled in")

// libztBackend is the embedded, userspace ZeroTier engine.
//
// STATUS: scaffold. This is the backend that makes a fully-sandboxed Flatpak
// possible (and therefore a Bazaar listing), because it runs ZeroTier entirely
// in userspace — no root, no /dev/net/tun, no CAP_NET_ADMIN, which a Flatpak
// sandbox does not grant.
//
// How it will work when implemented:
//
//  1. Link against libzt (https://github.com/zerotier/libzt) via cgo. libzt
//     bundles ZeroTier plus a userspace TCP/IP stack (lwIP), exposing a
//     BSD-socket-like API (zts_bsd_connect, zts_bsd_recv, ...).
//  2. Join the network with zts_net_join(networkID).
//  3. Because the browser cannot speak the userspace stack directly, run a
//     small local TCP proxy: the app listens on 127.0.0.1:PORT, and for each
//     inbound connection it opens a matching libzt socket to the ZimaOS node
//     and pumps bytes both ways. The web UI then points the browser at
//     http://127.0.0.1:PORT which is bridged to ZimaOS over ZeroTier.
//
// Until the cgo bindings are wired up, this backend reports itself unavailable
// so New() cleanly falls back to the host daemon. Build-tag the real
// implementation behind `//go:build libzt` and provide a matching manifest that
// vendors libzt.
type libztBackend struct {
	pinnedHost string
}

func newLibztBackend(pinnedHost string) *libztBackend {
	return &libztBackend{pinnedHost: pinnedHost}
}

func (l *libztBackend) Name() string { return "libzt" }

// Available is false until the cgo/libzt integration is compiled in.
func (l *libztBackend) Available() bool { return false }

func (l *libztBackend) Join(context.Context, string) error { return errNotImplemented }
func (l *libztBackend) Leave(context.Context) error        { return errNotImplemented }
func (l *libztBackend) Status(context.Context) Status {
	return Status{State: StateStopped, Message: "libzt backend not compiled in"}
}
func (l *libztBackend) Close() error { return nil }
