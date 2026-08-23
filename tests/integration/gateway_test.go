// Package integration verifies Gatekeeper against the running Docker Compose stack.
package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	defaultGatewayURL = "http://127.0.0.1:8080"
	defaultBackendURL = "http://127.0.0.1:8081"
	apiKeyHeader      = "X-API-Key"
)

func TestComposeStackEnforcesUploadLimit(t *testing.T) {
	if os.Getenv("GATEKEEPER_INTEGRATION") != "1" {
		t.Skip("set GATEKEEPER_INTEGRATION=1 to test the running Docker Compose stack")
	}

	client := &http.Client{Timeout: 5 * time.Second}
	gatewayURL := environmentOrDefault("GATEKEEPER_URL", defaultGatewayURL)
	backendURL := environmentOrDefault("MOCK_BACKEND_URL", defaultBackendURL)

	assertReady(t, client, gatewayURL)
	resetBackendCounters(t, client, backendURL)

	apiKey := fmt.Sprintf("integration-%d", time.Now().UnixNano())
	testStarted := time.Now()
	for requestNumber := 1; requestNumber <= 6; requestNumber++ {
		request, err := http.NewRequest(
			http.MethodPost,
			gatewayURL+"/api/upload",
			strings.NewReader("integration payload"),
		)
		if err != nil {
			t.Fatalf("create upload request %d: %v", requestNumber, err)
		}
		request.Header.Set(apiKeyHeader, apiKey)

		response := do(t, client, request)
		body := readAndClose(t, response)

		if response.Header.Get("RateLimit-Limit") != "5" {
			t.Errorf("request %d RateLimit-Limit = %q, want 5", requestNumber, response.Header.Get("RateLimit-Limit"))
		}

		if requestNumber <= 5 {
			if response.StatusCode != http.StatusAccepted {
				t.Fatalf("request %d status = %d, want %d; body = %s", requestNumber, response.StatusCode, http.StatusAccepted, body)
			}
			continue
		}

		if response.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("request 6 status = %d, want %d; body = %s", response.StatusCode, http.StatusTooManyRequests, body)
		}
		if response.Header.Get("RateLimit-Remaining") != "0" {
			t.Errorf("request 6 RateLimit-Remaining = %q, want 0", response.Header.Get("RateLimit-Remaining"))
		}
		retryAfter, err := strconv.Atoi(response.Header.Get("Retry-After"))
		if err != nil || retryAfter <= 0 {
			t.Errorf("request 6 Retry-After = %q, want a positive whole number", response.Header.Get("Retry-After"))
		}
	}
	if elapsed := time.Since(testStarted); elapsed >= 12*time.Second {
		t.Fatalf("six requests took %s; a token may have refilled during the proof", elapsed)
	}

	assertBackendCounters(t, client, backendURL)
	assertMetrics(t, client, gatewayURL)
}

func assertReady(t *testing.T, client *http.Client, gatewayURL string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, gatewayURL+"/ready", nil)
	if err != nil {
		t.Fatalf("create readiness request: %v", err)
	}
	response := do(t, client, request)
	body := readAndClose(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"ready"`) {
		t.Fatalf("readiness status = %d, body = %s", response.StatusCode, body)
	}
}

func resetBackendCounters(t *testing.T, client *http.Client, backendURL string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, backendURL+"/_mock/reset", nil)
	if err != nil {
		t.Fatalf("create reset request: %v", err)
	}
	response := do(t, client, request)
	readAndClose(t, response)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("reset status = %d, want %d", response.StatusCode, http.StatusNoContent)
	}
}

func assertBackendCounters(t *testing.T, client *http.Client, backendURL string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, backendURL+"/_mock/stats", nil)
	if err != nil {
		t.Fatalf("create stats request: %v", err)
	}
	response := do(t, client, request)
	defer response.Body.Close()

	var stats struct {
		Total  uint64 `json:"total"`
		Search uint64 `json:"search"`
		Upload uint64 `json:"upload"`
	}
	if err := json.NewDecoder(response.Body).Decode(&stats); err != nil {
		t.Fatalf("decode backend counters: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("stats status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if stats.Total != 5 || stats.Search != 0 || stats.Upload != 5 {
		t.Fatalf("backend counters = %+v, want total=5 search=0 upload=5", stats)
	}
}

func assertMetrics(t *testing.T, client *http.Client, gatewayURL string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, gatewayURL+"/metrics", nil)
	if err != nil {
		t.Fatalf("create metrics request: %v", err)
	}
	response := do(t, client, request)
	body := readAndClose(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", response.StatusCode, http.StatusOK)
	}

	metrics := string(body)
	for _, sample := range []string{
		`gatekeeper_requests_total{result="allowed",route="upload"}`,
		`gatekeeper_requests_total{result="rejected",route="upload"}`,
		`gatekeeper_request_duration_seconds_count{result="allowed",route="upload"}`,
	} {
		if !strings.Contains(metrics, sample) {
			t.Errorf("metrics output does not contain %q", sample)
		}
	}
}

func do(t *testing.T, client *http.Client, request *http.Request) *http.Response {
	t.Helper()
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", request.Method, request.URL, err)
	}
	return response
}

func readAndClose(t *testing.T, response *http.Response) []byte {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return body
}

func environmentOrDefault(name, fallback string) string {
	if value := strings.TrimRight(os.Getenv(name), "/"); value != "" {
		return value
	}
	return fallback
}
