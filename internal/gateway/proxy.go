package gateway

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/dsri45/gatekeeper/internal/config"
)

const (
	proxyConnectTimeout        = 5 * time.Second
	proxyKeepAlive             = 30 * time.Second
	proxyTLSHandshakeTimeout   = 5 * time.Second
	proxyResponseHeaderTimeout = 10 * time.Second
	proxyIdleConnectionTimeout = 90 * time.Second
)

// ProxyRegistry stores prepared reverse proxies by configured backend name.
// Its handler map is immutable after construction and safe for concurrent use.
type ProxyRegistry struct {
	handlers  map[string]http.Handler
	transport http.RoundTripper
}

// NewProxyRegistry validates backend URLs and prepares their proxy handlers.
func NewProxyRegistry(backends map[string]config.BackendConfig) (*ProxyRegistry, error) {
	return newProxyRegistry(backends, newProxyTransport())
}

func newProxyRegistry(
	backends map[string]config.BackendConfig,
	transport http.RoundTripper,
) (*ProxyRegistry, error) {
	if transport == nil {
		return nil, fmt.Errorf("proxy transport must not be nil")
	}

	handlers := make(map[string]http.Handler, len(backends))
	for name, backend := range backends {
		target, err := parseBackendURL(backend.URL)
		if err != nil {
			return nil, fmt.Errorf("backend %q: %w", name, err)
		}

		handlers[name] = newBackendProxy(target, transport)
	}

	return &ProxyRegistry{
		handlers:  handlers,
		transport: transport,
	}, nil
}

// Handler returns the prepared proxy for a backend name.
func (r *ProxyRegistry) Handler(backend string) (http.Handler, bool) {
	handler, found := r.handlers[backend]
	return handler, found
}

// CloseIdleConnections releases idle backend connections during shutdown.
func (r *ProxyRegistry) CloseIdleConnections() {
	if closer, ok := r.transport.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func newBackendProxy(target *url.URL, transport http.RoundTripper) http.Handler {
	return &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(request *httputil.ProxyRequest) {
			removeUntrustedForwardingHeaders(request.Out.Header)
			request.SetURL(target)
			request.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "{\"error\":\"backend unavailable\"}\n")
		},
	}
}

func parseBackendURL(rawURL string) (*url.URL, error) {
	target, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, fmt.Errorf("URL must use http or https")
	}
	if target.Host == "" {
		return nil, fmt.Errorf("URL must include a host")
	}
	return target, nil
}

func removeUntrustedForwardingHeaders(header http.Header) {
	header.Del("Forwarded")
	header.Del("X-Forwarded-For")
	header.Del("X-Forwarded-Host")
	header.Del("X-Forwarded-Proto")
}

func newProxyTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   proxyConnectTimeout,
			KeepAlive: proxyKeepAlive,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       proxyIdleConnectionTimeout,
		TLSHandshakeTimeout:   proxyTLSHandshakeTimeout,
		ResponseHeaderTimeout: proxyResponseHeaderTimeout,
		ExpectContinueTimeout: time.Second,
	}
}
