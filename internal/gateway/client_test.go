package gateway

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIdentifyClientFromAPIKey(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest("GET", "http://gateway.test/api/search", nil)
	request.Header.Set(APIKeyHeader, "  demo-premium-client  ")
	request.RemoteAddr = "not-an-ip"

	identity, err := IdentifyClient(request)
	if err != nil {
		t.Fatalf("IdentifyClient returned an error: %v", err)
	}

	if identity.Kind() != IdentityAPIKey {
		t.Errorf("Kind = %q, want %q", identity.Kind(), IdentityAPIKey)
	}
	apiKey, ok := identity.APIKey()
	if !ok {
		t.Fatal("APIKey reported that the API key was unavailable")
	}
	if apiKey != "demo-premium-client" {
		t.Errorf("APIKey = %q, want demo-premium-client", apiKey)
	}
	if strings.Contains(identity.BucketID(), apiKey) {
		t.Errorf("BucketID %q contains the raw API key", identity.BucketID())
	}
	if !strings.HasPrefix(identity.BucketID(), "api-key:") {
		t.Errorf("BucketID = %q, want api-key prefix", identity.BucketID())
	}
}

func TestIdentifyClientFallsBackToIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		apiKey     string
		remoteAddr string
	}{
		{
			name:       "missing API key",
			remoteAddr: "192.0.2.10:53241",
		},
		{
			name:       "empty API key",
			apiKey:     "   ",
			remoteAddr: "192.0.2.10:53241",
		},
		{
			name:       "bare IPv4 address",
			remoteAddr: "192.0.2.10",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			request := httptest.NewRequest("GET", "http://gateway.test/api/search", nil)
			request.RemoteAddr = test.remoteAddr
			if test.apiKey != "" {
				request.Header.Set(APIKeyHeader, test.apiKey)
			}

			identity, err := IdentifyClient(request)
			if err != nil {
				t.Fatalf("IdentifyClient returned an error: %v", err)
			}
			if identity.Kind() != IdentityIP {
				t.Errorf("Kind = %q, want %q", identity.Kind(), IdentityIP)
			}
			if _, ok := identity.APIKey(); ok {
				t.Error("APIKey reported an API key for an IP identity")
			}
			if !strings.HasPrefix(identity.BucketID(), "ip:") {
				t.Errorf("BucketID = %q, want ip prefix", identity.BucketID())
			}
		})
	}
}

func TestIPNormalizationProducesStableBucketID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		first  string
		second string
	}{
		{
			name:   "IPv4 with and without port",
			first:  "192.0.2.10:5000",
			second: "192.0.2.10",
		},
		{
			name:   "IPv6 with and without port",
			first:  "[2001:db8::1]:5000",
			second: "2001:db8::1",
		},
		{
			name:   "IPv4-mapped IPv6",
			first:  "[::ffff:192.0.2.10]:5000",
			second: "192.0.2.10:6000",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			first := identifyFromRemoteAddress(t, test.first)
			second := identifyFromRemoteAddress(t, test.second)
			if first.BucketID() != second.BucketID() {
				t.Errorf("BucketIDs differ: %q != %q", first.BucketID(), second.BucketID())
			}
		})
	}
}

func TestIdentifyClientIgnoresForwardedFor(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest("GET", "http://gateway.test/api/search", nil)
	request.RemoteAddr = "192.0.2.10:5000"
	request.Header.Set("X-Forwarded-For", "203.0.113.50")

	identity, err := IdentifyClient(request)
	if err != nil {
		t.Fatalf("IdentifyClient returned an error: %v", err)
	}
	want := identifyFromRemoteAddress(t, "192.0.2.10:6000")

	if identity.BucketID() != want.BucketID() {
		t.Errorf("BucketID = %q, want direct peer identity %q", identity.BucketID(), want.BucketID())
	}
}

func TestIdentifyClientRejectsMultipleAPIKeys(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest("GET", "http://gateway.test/api/search", nil)
	request.Header.Add(APIKeyHeader, "client-a")
	request.Header.Add(APIKeyHeader, "client-b")

	_, err := IdentifyClient(request)
	if err == nil {
		t.Fatal("IdentifyClient returned nil error for multiple API keys")
	}
	if !strings.Contains(err.Error(), "multiple X-API-Key headers") {
		t.Errorf("error = %q, want multiple-header error", err)
	}
}

func TestIdentifyClientRejectsInvalidRemoteAddress(t *testing.T) {
	t.Parallel()

	tests := []string{"", "not-an-ip", "192.0.2.10:not-a-port"}
	for _, remoteAddress := range tests {
		remoteAddress := remoteAddress
		t.Run(remoteAddress, func(t *testing.T) {
			t.Parallel()

			request := httptest.NewRequest("GET", "http://gateway.test/api/search", nil)
			request.RemoteAddr = remoteAddress
			_, err := IdentifyClient(request)
			if err == nil {
				t.Fatalf("IdentifyClient returned nil error for %q", remoteAddress)
			}
		})
	}
}

func TestIdentityHashingIsStableAndNamespaced(t *testing.T) {
	t.Parallel()

	first := hashIdentity(IdentityAPIKey, "same-value")
	second := hashIdentity(IdentityAPIKey, "same-value")
	differentValue := hashIdentity(IdentityAPIKey, "different-value")
	differentKind := hashIdentity(IdentityIP, "same-value")

	if first != second {
		t.Errorf("same identity produced different hashes: %q != %q", first, second)
	}
	if first == differentValue {
		t.Error("different values produced the same bucket identifier")
	}
	if first == differentKind {
		t.Error("different identity kinds produced the same bucket identifier")
	}
	if strings.Contains(first, "same-value") {
		t.Errorf("bucket identifier %q contains its raw value", first)
	}
}

func identifyFromRemoteAddress(t *testing.T, remoteAddress string) ClientIdentity {
	t.Helper()

	request := httptest.NewRequest("GET", "http://gateway.test/api/search", nil)
	request.RemoteAddr = remoteAddress
	identity, err := IdentifyClient(request)
	if err != nil {
		t.Fatalf("IdentifyClient(%q) returned an error: %v", remoteAddress, err)
	}
	return identity
}
