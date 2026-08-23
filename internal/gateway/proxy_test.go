package gateway

import (
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dsri45/gatekeeper/internal/config"
)

func TestProxyPreservesApplicationRequestAndRelaysResponse(t *testing.T) {
	t.Parallel()

	type capturedRequest struct {
		method      string
		scheme      string
		host        string
		path        string
		rawQuery    string
		body        string
		headers     http.Header
		requestHost string
	}

	captured := make(chan capturedRequest, 1)
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		captured <- capturedRequest{
			method:      request.Method,
			scheme:      request.URL.Scheme,
			host:        request.URL.Host,
			path:        request.URL.Path,
			rawQuery:    request.URL.RawQuery,
			body:        string(body),
			headers:     request.Header.Clone(),
			requestHost: request.Host,
		}

		return &http.Response{
			StatusCode: http.StatusAccepted,
			Header: http.Header{
				"Content-Type":  []string{"application/json"},
				"X-Backend":     []string{"mock"},
				"Connection":    []string{"X-Backend-Hop"},
				"X-Backend-Hop": []string{"remove-me"},
			},
			Body: io.NopCloser(strings.NewReader(`{"message":"upload accepted"}`)),
		}, nil
	})

	registry := mustProxyRegistry(t, map[string]config.BackendConfig{
		"mock": {URL: "https://backend.test/v1"},
	}, transport)
	proxy, found := registry.Handler("mock")
	if !found {
		t.Fatal("mock proxy was not found")
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"http://gateway.test/api/upload?source=mobile",
		strings.NewReader("file contents"),
	)
	request.RemoteAddr = "192.0.2.10:54321"
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("X-Custom", "preserve-me")
	request.Header.Set("X-API-Key", "client-123")
	request.Header.Set("X-Forwarded-For", "fake-address")
	request.Header.Set("X-Forwarded-Host", "fake-host")
	request.Header.Set("X-Forwarded-Proto", "fake-proto")
	request.Header.Set("Forwarded", "for=fake-address")
	request.Header.Set("Connection", "X-Remove-Me")
	request.Header.Set("X-Remove-Me", "remove-me")
	response := httptest.NewRecorder()

	proxy.ServeHTTP(response, request)
	outbound := <-captured

	if outbound.method != http.MethodPost {
		t.Errorf("method = %q, want POST", outbound.method)
	}
	if outbound.scheme != "https" || outbound.host != "backend.test" {
		t.Errorf("destination = %s://%s, want https://backend.test", outbound.scheme, outbound.host)
	}
	if outbound.requestHost != "" {
		t.Errorf("outbound Request.Host = %q, want empty so URL host is used", outbound.requestHost)
	}
	if outbound.path != "/v1/api/upload" {
		t.Errorf("path = %q, want /v1/api/upload", outbound.path)
	}
	if outbound.rawQuery != "source=mobile" {
		t.Errorf("query = %q, want source=mobile", outbound.rawQuery)
	}
	if outbound.body != "file contents" {
		t.Errorf("body = %q, want file contents", outbound.body)
	}
	if outbound.headers.Get("Content-Type") != "application/octet-stream" {
		t.Errorf("Content-Type = %q", outbound.headers.Get("Content-Type"))
	}
	if outbound.headers.Get("X-Custom") != "preserve-me" {
		t.Errorf("X-Custom = %q, want preserve-me", outbound.headers.Get("X-Custom"))
	}
	if outbound.headers.Get("X-API-Key") != "client-123" {
		t.Errorf("X-API-Key = %q, want client-123", outbound.headers.Get("X-API-Key"))
	}
	if outbound.headers.Get("X-Remove-Me") != "" {
		t.Errorf("hop-by-hop request header was forwarded")
	}
	if outbound.headers.Get("Forwarded") != "" {
		t.Errorf("untrusted Forwarded header was forwarded")
	}
	if outbound.headers.Get("X-Forwarded-For") != "192.0.2.10" {
		t.Errorf("X-Forwarded-For = %q, want direct peer IP", outbound.headers.Get("X-Forwarded-For"))
	}
	if outbound.headers.Get("X-Forwarded-Host") != "gateway.test" {
		t.Errorf("X-Forwarded-Host = %q, want gateway.test", outbound.headers.Get("X-Forwarded-Host"))
	}
	if outbound.headers.Get("X-Forwarded-Proto") != "http" {
		t.Errorf("X-Forwarded-Proto = %q, want http", outbound.headers.Get("X-Forwarded-Proto"))
	}

	if response.Code != http.StatusAccepted {
		t.Errorf("response status = %d, want %d", response.Code, http.StatusAccepted)
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Errorf("response Content-Type = %q", response.Header().Get("Content-Type"))
	}
	if response.Header().Get("X-Backend") != "mock" {
		t.Errorf("response X-Backend = %q, want mock", response.Header().Get("X-Backend"))
	}
	if response.Header().Get("X-Backend-Hop") != "" {
		t.Errorf("hop-by-hop response header was relayed")
	}
	if response.Body.String() != `{"message":"upload accepted"}` {
		t.Errorf("response body = %q", response.Body.String())
	}
}

func TestProxyReplacesForwardingHeadersForTLSRequest(t *testing.T) {
	t.Parallel()

	captured := make(chan http.Header, 1)
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		captured <- request.Header.Clone()
		return successfulResponse(), nil
	})
	registry := mustProxyRegistry(t, testBackends(), transport)
	proxy, _ := registry.Handler("mock")

	request := httptest.NewRequest(http.MethodGet, "https://gateway.test/api/search", nil)
	request.TLS = &tls.ConnectionState{}
	request.RemoteAddr = "[2001:db8::1]:5000"
	request.Header.Set("X-Forwarded-For", "198.51.100.1")
	response := httptest.NewRecorder()

	proxy.ServeHTTP(response, request)
	headers := <-captured

	if headers.Get("X-Forwarded-For") != "2001:db8::1" {
		t.Errorf("X-Forwarded-For = %q, want direct IPv6 peer", headers.Get("X-Forwarded-For"))
	}
	if headers.Get("X-Forwarded-Proto") != "https" {
		t.Errorf("X-Forwarded-Proto = %q, want https", headers.Get("X-Forwarded-Proto"))
	}
}

func TestProxyReturnsControlledBadGatewayResponse(t *testing.T) {
	t.Parallel()

	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial backend.internal: secret connection detail")
	})
	registry := mustProxyRegistry(t, testBackends(), transport)
	proxy, _ := registry.Handler("mock")
	response := httptest.NewRecorder()

	proxy.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/search", nil))

	if response.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", response.Code, http.StatusBadGateway)
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", response.Header().Get("Content-Type"))
	}
	body := response.Body.String()
	if body != "{\"error\":\"backend unavailable\"}\n" {
		t.Errorf("body = %q, want controlled error", body)
	}
	if strings.Contains(body, "backend.internal") || strings.Contains(body, "secret") {
		t.Errorf("response exposed internal error details: %q", body)
	}
}

func TestProxyRegistryRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		backend config.BackendConfig
		wantErr string
	}{
		{
			name:    "unsupported scheme",
			backend: config.BackendConfig{URL: "ftp://backend.test/files"},
			wantErr: "must use http or https",
		},
		{
			name:    "missing host",
			backend: config.BackendConfig{URL: "http:///missing-host"},
			wantErr: "must include a host",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := newProxyRegistry(
				map[string]config.BackendConfig{"broken": test.backend},
				roundTripFunc(func(*http.Request) (*http.Response, error) {
					return successfulResponse(), nil
				}),
			)
			if err == nil {
				t.Fatal("newProxyRegistry returned nil error")
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Errorf("error = %q, want %q", err, test.wantErr)
			}
		})
	}
}

func TestProxyRegistryRejectsNilTransport(t *testing.T) {
	t.Parallel()

	_, err := newProxyRegistry(testBackends(), nil)
	if err == nil || !strings.Contains(err.Error(), "transport must not be nil") {
		t.Fatalf("error = %v, want nil-transport error", err)
	}
}

func TestProxyRegistryReturnsNotFoundForUnknownBackend(t *testing.T) {
	t.Parallel()

	registry := mustProxyRegistry(t, testBackends(), roundTripFunc(func(*http.Request) (*http.Response, error) {
		return successfulResponse(), nil
	}))
	if _, found := registry.Handler("missing"); found {
		t.Error("unknown backend unexpectedly returned a handler")
	}
}

func TestProxyRegistrySupportsConcurrentReaders(t *testing.T) {
	t.Parallel()

	const readerCount = 250
	registry := mustProxyRegistry(t, testBackends(), roundTripFunc(func(*http.Request) (*http.Response, error) {
		return successfulResponse(), nil
	}))

	var waitGroup sync.WaitGroup
	var failures atomic.Int64
	waitGroup.Add(readerCount)

	for range readerCount {
		go func() {
			defer waitGroup.Done()
			if _, found := registry.Handler("mock"); !found {
				failures.Add(1)
			}
		}()
	}

	waitGroup.Wait()
	if failures.Load() != 0 {
		t.Errorf("%d concurrent lookups failed", failures.Load())
	}
}

func TestProxyRegistryClosesIdleConnections(t *testing.T) {
	t.Parallel()

	transport := &closeTrackingTransport{}
	registry := mustProxyRegistry(t, testBackends(), transport)
	registry.CloseIdleConnections()

	if !transport.closed.Load() {
		t.Error("CloseIdleConnections did not close the shared transport")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type closeTrackingTransport struct {
	closed atomic.Bool
}

func (transport *closeTrackingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return successfulResponse(), nil
}

func (transport *closeTrackingTransport) CloseIdleConnections() {
	transport.closed.Store(true)
}

func mustProxyRegistry(
	t *testing.T,
	backends map[string]config.BackendConfig,
	transport http.RoundTripper,
) *ProxyRegistry {
	t.Helper()

	registry, err := newProxyRegistry(backends, transport)
	if err != nil {
		t.Fatalf("newProxyRegistry returned an error: %v", err)
	}
	return registry
}

func testBackends() map[string]config.BackendConfig {
	return map[string]config.BackendConfig{
		"mock": {URL: "http://backend.test"},
	}
}

func successfulResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("ok")),
	}
}
