# Zima Connect

A lightweight Linux app that connects your desktop to your **ZimaOS** server over
**ZeroTier** and drops you straight onto the ZimaOS login page — filling the gap
left by the official Zima Client, which ships for Windows/macOS/Android but not
Linux.

It does exactly four things, matching the original brief:

1. **Runs ZeroTier** so you can reach your server from anywhere.
2. **Starts automatically** when you log in.
3. **Serves a localhost web page** where you paste your **Remote ID**.
4. **Opens the ZimaOS login automatically** once the connection is live.

It's a single, dependency-free Go binary (~6 MB) that idles as a background
service — no Electron, no embedded browser, near-zero CPU when connected.

---

## How it works

```
 ┌────────────────────┐        ┌──────────────────────┐        ┌───────────┐
 │  Your browser      │ ─────▶ │  zima-connect        │ ─────▶ │  ZeroTier │
 │  127.0.0.1:8787    │        │  (background service)│        │  network  │
 │  Remote ID form    │ ◀───── │  local web UI + API  │ ◀───── │           │
 └────────────────────┘        │  ZeroTier backend    │        └─────┬─────┘
        auto-redirect          └──────────────────────┘              │
        when connected                                         ┌─────▼─────┐
                                                               │  ZimaOS   │
                                                               │  server   │
                                                               └───────────┘
```

- **Remote ID** is the **ZeroTier network ID** ZimaOS shows under
  **Network → Remote Login** (16 hex characters, e.g. `8056c2e21c000001`).
- You paste it into the localhost page. The app joins that ZeroTier network.
- The first time, you approve the device on the ZimaOS side (it's a private
  ZeroTier controller run by ZimaOS, not by ZeroTier Inc.).
- Once authorized, the app finds your ZimaOS server on the network and the page
  auto-redirects to its login.

## Project layout

| Path | Purpose |
|------|---------|
| `cmd/zima-connect` | Entry point: single-instance service, opens the browser |
| `internal/config` | Persist the Remote ID + settings (`~/.config/zima-connect`) |
| `internal/zt` | ZeroTier engine behind a swappable `Backend` interface |
| `internal/zt/host.go` | Backend driving the system `zerotier-one` daemon (works today) |
| `internal/zt/libzt.go` | Embedded userspace backend for the sandboxed Flatpak (scaffold) |
| `internal/zt/discover.go` | Locates the ZimaOS server on the ZeroTier subnet |
| `internal/autostart` | Login autostart via XDG `~/.config/autostart` |
| `internal/server` | Localhost web UI + JSON API |
| `packaging/flatpak` | Flatpak manifest, AppStream metainfo, desktop entry, icon |

## Build & run

Requires Go 1.24+.

```sh
make run        # build and launch (opens the browser at http://127.0.0.1:8787)
make test       # unit tests
make build      # -> dist/zima-connect
```

To connect you need a ZeroTier engine. Today that means the system daemon:

```sh
# Debian/Ubuntu/Fedora — see https://www.zerotier.com/download/
curl -s https://install.zerotier.com | sudo bash
```

Then run `zima-connect`, paste your Remote ID, and approve the device in ZimaOS.

---

## Distribution paths (and the ZeroTier catch)

ZeroTier normally needs kernel-level networking (a TUN device, `CAP_NET_ADMIN`).
That drives a real architectural fork:

### Path A — system daemon (works now)
The `host` backend drives an installed `zerotier-one`. Robust, but not
sandbox-friendly, so it's distributed as a plain binary / AppImage / `.deb`.
This is what the current build does out of the box.

### Path B — embedded libzt (required for Flathub → Bazaar)
A **Flatpak sandbox cannot run the host daemon**, so the store build embeds
**[libzt](https://github.com/zerotier/libzt)** — ZeroTier in *userspace*, no root,
no TUN. A tiny local TCP proxy then bridges the browser to ZimaOS over the
userspace stack. This is the backend that lets the app ship on Flathub and
therefore show up in Bazaar. It's scaffolded in `internal/zt/libzt.go` with the
implementation plan; wiring the cgo bindings is the remaining milestone.

The `Backend` interface means the UI, config, autostart, and web server are all
already done and shared across both paths.

---

## Publishing to Bazaar

**There is no separate "Bazaar upload".** Bazaar is just a nice frontend for
**Flathub**. Publish to Flathub once and the app automatically appears in
Bazaar, GNOME Software, KDE Discover, and on Bazzite — everywhere.

The steps:

1. **Finish the libzt backend** (Path B) so the app works inside the sandbox.
2. **Build the Flatpak locally** and make sure it runs:
   ```sh
   make flatpak && make flatpak-run
   ```
3. **Validate the metadata** (Flathub's bots check this):
   ```sh
   flatpak run org.freedesktop.appstream-glib validate \
     packaging/flatpak/io.github.jirkacepelka.ZimaConnect.metainfo.xml
   desktop-file-validate packaging/flatpak/io.github.jirkacepelka.ZimaConnect.desktop
   ```
4. **Add screenshots** referenced by the metainfo (`packaging/flatpak/screenshots/`).
5. **Tag a release** and switch the manifest source from `type: dir` to a
   `type: git` tag (see the comment in the manifest).
6. **Submit to Flathub**: fork <https://github.com/flathub/flathub>, open a PR on
   the `new-pr` branch adding your manifest. Flathub's bot builds and reviews it.
   Full guide: <https://docs.flathub.org/docs/for-app-authors/submission>.
7. Once merged, the app is on Flathub — and **shows up in Bazaar automatically**.

The app ID `io.github.jirkacepelka.ZimaConnect` already follows Flathub's
`io.github.<user>.<App>` convention for GitHub-hosted projects.

---

## Status

- ✅ Localhost UI, Remote ID entry, live status, auto-redirect to ZimaOS
- ✅ Login autostart (XDG)
- ✅ Host `zerotier-one` backend with ZimaOS auto-discovery
- ✅ Flatpak manifest, AppStream metainfo, desktop entry, icon
- 🚧 Embedded libzt backend (needed for the actual Flathub/Bazaar release)
- 🚧 Background-portal autostart inside the Flatpak sandbox
- 🚧 Screenshots for the store listing

## License

MIT — see [LICENSE](LICENSE).
