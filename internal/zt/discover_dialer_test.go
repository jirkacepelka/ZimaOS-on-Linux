package zt

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestURLToHostPort(t *testing.T) {
	cases := map[string]string{
		"http://10.147.20.5":      "10.147.20.5:80",
		"https://10.147.20.5":     "10.147.20.5:443",
		"http://10.147.20.5:8080": "10.147.20.5:8080",
		"https://zima.local:8443": "zima.local:8443",
		"not a url":               "",
		"":                        "",
	}
	for in, want := range cases {
		if got := urlToHostPort(in); got != want {
			t.Errorf("urlToHostPort(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestProbeZima_Signature exercises the shared discovery probe through the
// default netDialer against real local servers — the same probe the libzt
// backend runs through its own dialer.
func TestProbeZima_Signature(t *testing.T) {
	// A server that identifies as ZimaOS.
	zima := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html><title>ZimaOS</title></html>")
	}))
	defer zima.Close()

	// A server with no recognisable signature.
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "just a webserver")
	}))
	defer plain.Close()

	ctx := context.Background()

	// The httptest addresses are "127.0.0.1:PORT"; probeZima builds
	// "http://<addr>" from them, which the netDialer dials directly.
	url, strong, ok := probeZima(ctx, strings.TrimPrefix(zima.URL, "http://"), netDialer{})
	if !ok || !strong {
		t.Errorf("ZimaOS server: ok=%v strong=%v, want true/true", ok, strong)
	}
	if !strings.HasPrefix(url, "http://") {
		t.Errorf("unexpected url %q", url)
	}

	_, strong, ok = probeZima(ctx, strings.TrimPrefix(plain.URL, "http://"), netDialer{})
	if !ok {
		t.Error("plain server: expected ok=true (answered)")
	}
	if strong {
		t.Error("plain server: expected strong=false (no signature)")
	}

	// A dead address answers nothing.
	if _, _, ok := probeZima(ctx, "127.0.0.1:1", netDialer{}); ok {
		t.Error("dead port: expected ok=false")
	}
}
