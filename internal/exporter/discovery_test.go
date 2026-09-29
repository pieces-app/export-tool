package exporter

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEnvironmentReadinessAndRangeBoundary(t *testing.T) {
	health := "ok:macos"
	version := "12.6.29-staging"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/health" {
			fmt.Fprint(w, health)
		} else {
			fmt.Fprint(w, version)
		}
	}))
	defer srv.Close()
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	c, info, err := discoverRange(context.Background(), time.Second, 4096, "staging", port, port)
	if err != nil || c == nil || info.Environment != "staging" {
		t.Fatalf("boundary discovery failed: %v", err)
	}
	if _, _, err := discoverRange(context.Background(), time.Second, 4096, "production", port, port); !errors.Is(err, ErrNotFound) {
		t.Fatal("wrong environment accepted")
	}
	health = "migrating"
	if _, _, err := discoverRange(context.Background(), time.Second, 4096, "staging", port, port); !errors.Is(err, ErrMigrating) {
		t.Fatal("migration reported as ready")
	}
	health = "ordinary HTTP server"
	if _, _, err := discoverRange(context.Background(), time.Second, 4096, "auto", port, port); !errors.Is(err, ErrNotFound) {
		t.Fatal("unrelated server accepted")
	}
}
