package proxy

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestProxyForwardsHTTP verifies the proxy transparently tunnels an HTTP
// request/response to the far side, using an ordinary net.Dialer in place of
// libzt.
func TestProxyForwardsHTTP(t *testing.T) {
	// Far side: an HTTP server standing in for ZimaOS.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello from zimaos")
	}))
	defer backend.Close()

	target := backend.Listener.Addr().String()

	p := New(DialerFunc(func(ctx context.Context, network, address string) (net.Conn, error) {
		// The proxy should ask us to dial exactly the configured target.
		if address != target {
			t.Errorf("dial address = %q, want %q", address, target)
		}
		var d net.Dialer
		return d.DialContext(ctx, network, address)
	}), target)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, err := p.ListenAndServe(ctx)
	if err != nil {
		t.Fatalf("ListenAndServe: %v", err)
	}

	// Talk HTTP to the proxy's local address; expect the backend's response.
	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("GET via proxy: %v", err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	if got := string(buf[:n]); got != "hello from zimaos" {
		t.Errorf("body = %q, want %q", got, "hello from zimaos")
	}
}

// TestProxyBidirectional checks that bytes flow both ways over a raw stream.
func TestProxyBidirectional(t *testing.T) {
	// Far side: an echo server.
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				sc := bufio.NewScanner(c)
				for sc.Scan() {
					fmt.Fprintf(c, "echo:%s\n", sc.Text())
				}
			}(c)
		}
	}()

	p := New(DialerFunc(func(ctx context.Context, network, address string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, address)
	}), echo.Addr().String())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, err := p.ListenAndServe(ctx)
	if err != nil {
		t.Fatal(err)
	}

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	fmt.Fprintln(conn, "ping")
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if line != "echo:ping\n" {
		t.Errorf("got %q, want %q", line, "echo:ping\n")
	}
}

// TestProxyStopsOnCancel ensures Serve returns and the listener closes when the
// context is cancelled.
func TestProxyStopsOnCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := New(DialerFunc(func(ctx context.Context, network, address string) (net.Conn, error) {
		return nil, fmt.Errorf("unused")
	}), "127.0.0.1:1")

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- p.Serve(ctx, ln) }()

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Serve returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}

	// The listener must be closed.
	if _, err := ln.Accept(); err == nil {
		t.Error("listener still open after Serve returned")
	}
}
