// Package autostart makes Zima Connect launch automatically when the user logs
// in, using the freedesktop.org XDG autostart specification.
//
// It writes a .desktop file into ~/.config/autostart/. Every major Linux
// desktop (GNOME, KDE, XFCE, ...) reads this directory on login. When running
// as a Flatpak the same file works, but the recommended path there is the
// Background portal (org.freedesktop.portal.Background) — see EnableFlatpakNote.
package autostart

import (
	"fmt"
	"os"
	"path/filepath"
)

const desktopFileName = "zima-connect.desktop"

// autostartDir returns ~/.config/autostart, creating it on demand.
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

// Enabled reports whether the autostart entry currently exists.
func Enabled() bool {
	dir, err := autostartDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, desktopFileName))
	return err == nil
}

// Set enables or disables login autostart.
//
// execCmd is the command the desktop entry runs. Pass the absolute path to the
// running binary (or the Flatpak launch command) plus any flags — typically a
// "--background" flag so it starts headless.
func Set(enabled bool, execCmd string) error {
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
