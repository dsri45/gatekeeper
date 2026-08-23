// Package gateway contains Gatekeeper's HTTP request handling components.
package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
)

const APIKeyHeader = "X-API-Key"

// IdentityKind describes which request property identified a client.
type IdentityKind string

const (
	IdentityAPIKey IdentityKind = "api-key"
	IdentityIP     IdentityKind = "ip"
)

// ClientIdentity is the normalized identity associated with one request.
// Raw API keys remain private so callers cannot accidentally log or persist
// them. APIKey exposes the value only for client-override selection.
type ClientIdentity struct {
	kind      IdentityKind
	rawAPIKey string
	bucketID  string
}

// IdentifyClient identifies a request by API key, then falls back to peer IP.
func IdentifyClient(request *http.Request) (ClientIdentity, error) {
	values := request.Header.Values(APIKeyHeader)
	if len(values) > 1 {
		return ClientIdentity{}, fmt.Errorf("multiple %s headers are not allowed", APIKeyHeader)
	}

	if len(values) == 1 {
		apiKey := strings.TrimSpace(values[0])
		if apiKey != "" {
			return ClientIdentity{
				kind:      IdentityAPIKey,
				rawAPIKey: apiKey,
				bucketID:  hashIdentity(IdentityAPIKey, apiKey),
			}, nil
		}
	}

	ip, err := peerIP(request.RemoteAddr)
	if err != nil {
		return ClientIdentity{}, err
	}

	return ClientIdentity{
		kind:     IdentityIP,
		bucketID: hashIdentity(IdentityIP, ip),
	}, nil
}

// Kind reports whether the identity came from an API key or peer IP.
func (i ClientIdentity) Kind() IdentityKind {
	return i.kind
}

// BucketID returns a stable, non-raw identifier suitable for Redis keys.
func (i ClientIdentity) BucketID() string {
	return i.bucketID
}

// APIKey returns the normalized raw API key only for API-key identities.
func (i ClientIdentity) APIKey() (string, bool) {
	if i.kind != IdentityAPIKey {
		return "", false
	}
	return i.rawAPIKey, true
}

func peerIP(remoteAddress string) (string, error) {
	remoteAddress = strings.TrimSpace(remoteAddress)
	if remoteAddress == "" {
		return "", fmt.Errorf("request remote address is empty")
	}

	addressPort, addressPortErr := netip.ParseAddrPort(remoteAddress)
	if addressPortErr == nil {
		address := addressPort.Addr().WithZone("").Unmap()
		return address.String(), nil
	}

	address, addressErr := netip.ParseAddr(remoteAddress)
	if addressErr != nil {
		return "", fmt.Errorf("parse request remote address: %w", addressPortErr)
	}
	address = address.WithZone("").Unmap()
	return address.String(), nil
}

func hashIdentity(kind IdentityKind, value string) string {
	digest := sha256.Sum256([]byte(string(kind) + "\x00" + value))
	return string(kind) + ":" + hex.EncodeToString(digest[:])
}
