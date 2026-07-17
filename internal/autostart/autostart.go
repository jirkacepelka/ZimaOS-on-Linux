// Package autostart makes Zima Connect launch automatically when the user logs
// in.
//
// Two mechanisms, picked at runtime:
//
//   - Normal installs: write a freedesktop.org XDG autostart .desktop file into
//     ~/.config/autostart/. Every major desktop reads it on login.
//
//   - Flatpak: the sandbox's ~/.config is redirected to ~/.var/app/... which the
//     host session never reads at login, so the XDG file would do nothing.
//     Instead we ask the XDG Desktop Background portal to register autostart on
//     the host. We drive the portal with `gdbus` (shipped in the runtime's
//     glib), which keeps this package dependency-free.
package autostart

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const desktopFileName = "zima-connect.desktop"

// inFlatpak reports whether we are running inside a Flatpak sandbox.
func inFlatpak() bool {
	if os.Getenv("FLATPAK_ID") != "" {
		return true
	}
	_, err := os.Stat("/.flatpak-info")
	return err == nil
}

// Set enables or disables login autostart. execCmd is the command for the XDG
// .desktop entry (ignored on the Flatpak/portal path, which derives its own).
func Set(enabled bool, execCmd string) error {
	if inFlatpak() {
		return setViaPortal(enabled)
	}
	return setViaXDGFile(enabled, execCmd)
}

// Enabled reports whether the XDG autostart entry exists. On Flatpak the portal
// state cannot be cheaply queried, so callers should track intent themselves
// (Zima Connect persists it in its config).
func Enabled() bool {
	dir, err := autostartDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, desktopFileName))
	return err == nil
}

// ---- XDG autostart file ----------------------------------------------------

func autostartDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "autostart")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func setViaXDGFile(enabled bool, execCmd string) error {
	dir, err := autostartDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, desktopFileName)

	if !enabled {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	entry := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Zima Connect
Comment=Connect to your ZimaOS server
Exec=%s
Icon=io.github.jirkacepelka.ZimaConnect
Terminal=false
Categories=Network;
X-GNOME-Autostart-enabled=true
`, execCmd)

	return os.WriteFile(path, []byte(entry), 0o644)
}

// ---- Flatpak Background portal ---------------------------------------------

// setViaPortal asks org.freedesktop.portal.Background.RequestBackground to
// register (or clear) login autostart for this Flatpak. The first request may
// prompt the user for permission; the definitive result is delivered on the
// portal Request's Response signal, but for autostart a best-effort call is
// sufficient, so we don't block on it.
func setViaPortal(enabled bool) error {
	gdbus, err := exec.LookPath("gdbus")
	if err != nil {
		return fmt.Errorf("cannot configure autostart: gdbus not found in sandbox: %w", err)
	}
	out, err := exec.Command(gdbus, "call", "--session",
		"--dest", "org.freedesktop.portal.Desktop",
		"--object-path", "/org/freedesktop/portal/desktop",
		"--method", "org.freedesktop.portal.Background.RequestBackground",
		"", portalOptions(enabled),
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("background portal call failed: %v: %s", err, out)
	}
	return nil
}

// portalOptions builds the a{sv} options dict (in GVariant text form) for
// RequestBackground. Kept separate so it can be unit-tested without a bus.
func portalOptions(enabled bool) string {
	if !enabled {
		return "{'autostart': <false>, 'handle_token': <'zimaconnect'>}"
	}
	return "{" +
		"'reason': <'Reconnect to your ZimaOS server when you log in'>, " +
		"'autostart': <true>, " +
		"'commandline': <['zima-connect', '--background']>, " +
		"'handle_token': <'zimaconnect'>" +
		"}"
}
