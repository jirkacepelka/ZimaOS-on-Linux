// Command zima-connect is a lightweight background service that connects a
// Linux desktop to a ZimaOS server over ZeroTier and exposes a small localhost
// web UI for entering the Remote ID and jumping to the ZimaOS login.
//
// Design goals (in priority order for a background app):
//   - tiny footprint: pure-stdlib Go, single static binary, no embedded browser
//   - efficient: an idle HTTP server plus a 3s ZeroTier poll; near-zero CPU
//   - unobtrusive: runs headless with --background, opens the browser on demand
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/jirkacepelka/zimaos-on-linux/internal/config"
	"github.com/jirkacepelka/zimaos-on-linux/internal/server"
	"github.com/jirkacepelka/zimaos-on-linux/internal/zt"
)

// defaultAddr is the fixed localhost address of the UI. A fixed port doubles as
// a simple single-instance lock: if we cannot bind it, an instance is already
// running and we just open the browser there.
const defaultAddr = "127.0.0.1:8787"

func main() {
	log.SetFlags(0)
	log.SetPrefix("zima-connect: ")

	background := flag.Bool("background", false, "run headless without opening the browser (used at login)")
	flag.Parse()

	addr := os.Getenv("ZIMA_CONNECT_ADDR")
	if addr == "" {
		addr = defaultAddr
	}
	uiURL := "http://" + addr + "/"

	// Single-instance: try to grab the port. If it is taken, assume our own
	// instance owns it and just surface the UI.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if !*background {
			openBrowser(uiURL)
		}
		log.Printf("another instance is already running at %s", uiURL)
		return
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}

	backend, err := zt.New(cfg.ZimaHost)
	if err != nil {
		log.Fatalf("%v", err)
	}
	defer backend.Close()
	log.Printf("using ZeroTier backend: %s", backend.Name())

	srv := server.New(cfg, backend, autostartCommand(), func(pinnedHost string) (zt.Backend, error) {
		return zt.New(pinnedHost)
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Reconnect to the saved network on startup (matters for login autostart).
	srv.AutoResume(ctx)

	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("UI at %s", uiURL)
		if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	if !*background {
		openBrowser(uiURL)
	}

	<-ctx.Done()
	log.Printf("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}

// autostartCommand is the command written into the login autostart entry. We
// prefer FLATPAK_ID (so the entry launches the Flatpak) and otherwise the
// absolute path to this binary, always with --background so login is silent.
func autostartCommand() string {
	if id := os.Getenv("FLATPAK_ID"); id != "" {
		return fmt.Sprintf("flatpak run %s --background", id)
	}
	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = "zima-connect"
	}
	return exe + " --background"
}

// openBrowser launches the user's default browser at url. On Flatpak, xdg-open
// is bridged to the host through the OpenURI portal.
func openBrowser(url string) {
	if _, err := exec.LookPath("xdg-open"); err != nil {
		log.Printf("open %s in your browser", url)
		return
	}
	if err := exec.Command("xdg-open", url).Start(); err != nil {
		log.Printf("could not open browser (%v); visit %s", err, url)
	}
}
