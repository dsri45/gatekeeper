package mockbackend

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestSearch(t *testing.T) {
	t.Parallel()

	backend := New()
	response := serve(backend, http.MethodGet, "/api/search?q=redis", nil)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}

	var body struct {
		Message string `json:"message"`
		Query   string `json:"query"`
	}
	decodeJSON(t, response.Body, &body)

	if body.Message != "search completed" {
		t.Errorf("message = %q, want search completed", body.Message)
	}
	if body.Query != "redis" {
		t.Errorf("query = %q, want redis", body.Query)
	}

	assertStats(t, backend, Stats{Total: 1, Search: 1})
}

func TestUpload(t *testing.T) {
	t.Parallel()

	backend := New()
	payload := "example upload contents"
	response := serve(backend, http.MethodPost, "/api/upload", strings.NewReader(payload))

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusAccepted)
	}

	var body struct {
		Message       string `json:"message"`
		BytesReceived int64  `json:"bytes_received"`
	}
	decodeJSON(t, response.Body, &body)

	if body.Message != "upload accepted" {
		t.Errorf("message = %q, want upload accepted", body.Message)
	}
	if body.BytesReceived != int64(len(payload)) {
		t.Errorf("bytes_received = %d, want %d", body.BytesReceived, len(payload))
	}

	assertStats(t, backend, Stats{Total: 1, Upload: 1})
}

func TestUploadRejectsOversizedBodyAndCountsRequest(t *testing.T) {
	t.Parallel()

	backend := New()
	response := serve(
		backend,
		http.MethodPost,
		"/api/upload",
		strings.NewReader(strings.Repeat("x", int(maxUploadBytes)+1)),
	)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
	assertStats(t, backend, Stats{Total: 1, Upload: 1})
}

func TestHealthAndStatsDoNotIncrementCounters(t *testing.T) {
	t.Parallel()

	backend := New()
	for _, path := range []string{"/health", "/_mock/stats"} {
		response := serve(backend, http.MethodGet, path, nil)
		if response.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want %d", path, response.Code, http.StatusOK)
		}
	}

	assertStats(t, backend, Stats{})
}

func TestStatsEndpointAndReset(t *testing.T) {
	t.Parallel()

	backend := New()
	serve(backend, http.MethodGet, "/api/search", nil)
	serve(backend, http.MethodPost, "/api/upload", strings.NewReader("data"))

	response := serve(backend, http.MethodGet, "/_mock/stats", nil)
	var beforeReset Stats
	decodeJSON(t, response.Body, &beforeReset)

	wantBeforeReset := Stats{Total: 2, Search: 1, Upload: 1}
	if beforeReset != wantBeforeReset {
		t.Errorf("stats before reset = %+v, want %+v", beforeReset, wantBeforeReset)
	}

	response = serve(backend, http.MethodPost, "/_mock/reset", nil)
	if response.Code != http.StatusNoContent {
		t.Fatalf("reset status = %d, want %d", response.Code, http.StatusNoContent)
	}

	assertStats(t, backend, Stats{})
}

func TestMethodRestrictionsAndUnknownPath(t *testing.T) {
	t.Parallel()

	backend := New()
	tests := []struct {
		name      string
		method    string
		path      string
		wantCode  int
		wantAllow string
	}{
		{
			name:      "search requires GET",
			method:    http.MethodPost,
			path:      "/api/search",
			wantCode:  http.StatusMethodNotAllowed,
			wantAllow: http.MethodGet,
		},
		{
			name:      "HEAD is not treated as GET",
			method:    http.MethodHead,
			path:      "/api/search",
			wantCode:  http.StatusMethodNotAllowed,
			wantAllow: http.MethodGet,
		},
		{
			name:      "upload requires POST",
			method:    http.MethodGet,
			path:      "/api/upload",
			wantCode:  http.StatusMethodNotAllowed,
			wantAllow: http.MethodPost,
		},
		{
			name:     "unknown path",
			method:   http.MethodGet,
			path:     "/missing",
			wantCode: http.StatusNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			response := serve(backend, test.method, test.path, nil)
			if response.Code != test.wantCode {
				t.Errorf("status = %d, want %d", response.Code, test.wantCode)
			}
			if allow := response.Header().Get("Allow"); allow != test.wantAllow {
				t.Errorf("Allow = %q, want %q", allow, test.wantAllow)
			}
		})
	}

	assertStats(t, backend, Stats{})
}

func TestConcurrentSearchCounting(t *testing.T) {
	t.Parallel()

	const requestCount = 250
	backend := New()
	errors := make(chan error, requestCount)

	var waitGroup sync.WaitGroup
	waitGroup.Add(requestCount)

	for range requestCount {
		go func() {
			defer waitGroup.Done()

			response := serve(backend, http.MethodGet, "/api/search?q=concurrent", nil)
			if response.Code != http.StatusOK {
				errors <- fmt.Errorf("unexpected status: %d", response.Code)
			}
		}()
	}

	waitGroup.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent request failed: %v", err)
	}

	assertStats(t, backend, Stats{Total: requestCount, Search: requestCount})
}

func serve(backend *Server, method string, target string, body io.Reader) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, body)
	response := httptest.NewRecorder()
	backend.Handler().ServeHTTP(response, request)
	return response
}

func assertStats(t *testing.T, backend *Server, want Stats) {
	t.Helper()

	if got := backend.stats(); got != want {
		t.Errorf("stats = %+v, want %+v", got, want)
	}
}

func decodeJSON(t *testing.T, reader io.Reader, destination any) {
	t.Helper()

	if err := json.NewDecoder(reader).Decode(destination); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
}
