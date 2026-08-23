package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dsri45/gatekeeper/internal/config"
)

func TestParseOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		wantPath string
	}{
		{name: "default path", wantPath: "config/config.example.yaml"},
		{name: "custom path", args: []string{"-config", "custom.yaml"}, wantPath: "custom.yaml"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			options, err := parseOptions(test.args)
			if err != nil {
				t.Fatalf("parseOptions returned an error: %v", err)
			}
			if options.configPath != test.wantPath {
				t.Errorf("configPath = %q, want %q", options.configPath, test.wantPath)
			}
		})
	}
}

func TestParseOptionsRejectsInvalidArguments(t *testing.T) {
	t.Parallel()

	for _, arguments := range [][]string{{"-unknown"}, {"unexpected-positional-value"}} {
		if _, err := parseOptions(arguments); err == nil {
			t.Errorf("parseOptions(%v) returned nil error", arguments)
		}
	}
}

func TestRunRejectsMissingConfiguration(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing.yaml")
	err := run(context.Background(), []string{"-config", missing}, discardLogger())
	if err == nil {
		t.Fatal("run returned nil error for missing configuration")
	}
	if !strings.Contains(err.Error(), "open config") {
		t.Errorf("error = %q, want open-config error", err)
	}
}

func TestNewHTTPServerUsesConfiguration(t *testing.T) {
	t.Parallel()

	serverConfig := config.ServerConfig{
		Address:           "127.0.0.1:0",
		ReadHeaderTimeout: config.NewDuration(time.Second),
		ReadTimeout:       config.NewDuration(2 * time.Second),
		WriteTimeout:      config.NewDuration(3 * time.Second),
		IdleTimeout:       config.NewDuration(4 * time.Second),
	}
	server := newHTTPServer(serverConfig, http.NotFoundHandler())

	if server.Addr != serverConfig.Address {
		t.Errorf("Addr = %q, want %q", server.Addr, serverConfig.Address)
	}
	if server.ReadHeaderTimeout != time.Second {
		t.Errorf("ReadHeaderTimeout = %s, want 1s", server.ReadHeaderTimeout)
	}
	if server.ReadTimeout != 2*time.Second {
		t.Errorf("ReadTimeout = %s, want 2s", server.ReadTimeout)
	}
	if server.WriteTimeout != 3*time.Second {
		t.Errorf("WriteTimeout = %s, want 3s", server.WriteTimeout)
	}
	if server.IdleTimeout != 4*time.Second {
		t.Errorf("IdleTimeout = %s, want 4s", server.IdleTimeout)
	}
	if server.MaxHeaderBytes != maxHeaderBytes {
		t.Errorf("MaxHeaderBytes = %d, want %d", server.MaxHeaderBytes, maxHeaderBytes)
	}
}

func TestServeStopsOnContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	closer := &trackingCloser{}
	server := &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()}
	err := serve(ctx, server, closer, time.Second, discardLogger())
	if err != nil {
		t.Fatalf("serve returned an error: %v", err)
	}
	if !closer.closed.Load() {
		t.Error("serve did not close idle backend connections")
	}
}

type trackingCloser struct {
	closed atomic.Bool
}

func (closer *trackingCloser) CloseIdleConnections() {
	closer.closed.Store(true)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
