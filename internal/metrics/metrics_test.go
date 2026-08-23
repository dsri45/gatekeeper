package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsHandlerExportsRecordedValues(t *testing.T) {
	t.Parallel()

	applicationMetrics := New()
	applicationMetrics.ObserveRequest("search", "allowed", 25*time.Millisecond)
	applicationMetrics.ObserveRequest("search", "rejected", 2*time.Millisecond)
	applicationMetrics.IncLimiterError("search")

	response := httptest.NewRecorder()
	applicationMetrics.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	for _, expected := range []string{
		`gatekeeper_requests_total{result="allowed",route="search"} 1`,
		`gatekeeper_requests_total{result="rejected",route="search"} 1`,
		`gatekeeper_limiter_errors_total{route="search"} 1`,
		`gatekeeper_request_duration_seconds_count{result="allowed",route="search"} 1`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("metrics output does not contain %q", expected)
		}
	}
}
