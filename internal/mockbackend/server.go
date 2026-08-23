// Package mockbackend provides the test backend protected by Gatekeeper.
package mockbackend

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
)

const maxUploadBytes int64 = 1 << 20 // 1 MiB

// Server is a concurrency-safe mock HTTP backend.
type Server struct {
	totalRequests  atomic.Uint64
	searchRequests atomic.Uint64
	uploadRequests atomic.Uint64
	handler        http.Handler
}

// Stats reports how many application requests reached the mock backend.
type Stats struct {
	Total  uint64 `json:"total"`
	Search uint64 `json:"search"`
	Upload uint64 `json:"upload"`
}

// New creates a mock backend with all counters set to zero.
func New() *Server {
	server := &Server{}

	mux := http.NewServeMux()
	mux.Handle("/api/search", requireMethod(http.MethodGet, http.HandlerFunc(server.handleSearch)))
	mux.Handle("/api/upload", requireMethod(http.MethodPost, http.HandlerFunc(server.handleUpload)))
	mux.Handle("/health", requireMethod(http.MethodGet, http.HandlerFunc(server.handleHealth)))
	mux.Handle("/_mock/stats", requireMethod(http.MethodGet, http.HandlerFunc(server.handleStats)))
	mux.Handle("/_mock/reset", requireMethod(http.MethodPost, http.HandlerFunc(server.handleReset)))

	server.handler = mux
	return server
}

// Handler returns the HTTP handler for the mock backend.
func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	s.totalRequests.Add(1)
	s.searchRequests.Add(1)

	writeJSON(w, http.StatusOK, struct {
		Message string `json:"message"`
		Query   string `json:"query"`
	}{
		Message: "search completed",
		Query:   r.URL.Query().Get("q"),
	})
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	s.totalRequests.Add(1)
	s.uploadRequests.Add(1)

	body := http.MaxBytesReader(w, r.Body, maxUploadBytes)
	defer body.Close()

	bytesReceived, err := io.Copy(io.Discard, body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, struct {
				Error string `json:"error"`
			}{Error: "request body exceeds 1 MiB limit"})
			return
		}

		writeJSON(w, http.StatusBadRequest, struct {
			Error string `json:"error"`
		}{Error: "could not read request body"})
		return
	}

	writeJSON(w, http.StatusAccepted, struct {
		Message       string `json:"message"`
		BytesReceived int64  `json:"bytes_received"`
	}{
		Message:       "upload accepted",
		BytesReceived: bytesReceived,
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		Status string `json:"status"`
	}{Status: "healthy"})
}

func (s *Server) handleStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.stats())
}

func (s *Server) handleReset(w http.ResponseWriter, _ *http.Request) {
	s.totalRequests.Store(0)
	s.searchRequests.Store(0)
	s.uploadRequests.Store(0)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) stats() Stats {
	return Stats{
		Total:  s.totalRequests.Load(),
		Search: s.searchRequests.Load(),
		Upload: s.uploadRequests.Load(),
	}
}

func requireMethod(method string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
