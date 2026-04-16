package httptransport

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	internalmetrics "github.com/stellaraxis/starmap/internal/metrics"
)

func TestPublicServerWithMetricsObservesKnownAndUnknownRoutes(t *testing.T) {
	registry := prometheus.NewRegistry()
	transportMetrics := internalmetrics.NewTransportMetrics()
	if err := transportMetrics.Register(registry); err != nil {
		t.Fatalf("register transport metrics failed: %v", err)
	}

	server := NewPublicServer(NoopRegistryHandler{}, NoopHealthHandler{}).WithMetrics(transportMetrics.HTTP())
	handler := server.Handler()

	healthRequest := httptest.NewRequest(http.MethodGet, routeHealthz, nil)
	healthRecorder := httptest.NewRecorder()
	handler.ServeHTTP(healthRecorder, healthRequest)
	if healthRecorder.Code != http.StatusOK {
		t.Fatalf("unexpected health status code: %d", healthRecorder.Code)
	}

	missingRequest := httptest.NewRequest(http.MethodGet, "/missing", nil)
	missingRecorder := httptest.NewRecorder()
	handler.ServeHTTP(missingRecorder, missingRequest)
	if missingRecorder.Code != http.StatusNotFound {
		t.Fatalf("unexpected missing status code: %d", missingRecorder.Code)
	}

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics failed: %v", err)
	}

	assertHTTPMetricCounter(t, families, "starmap_http_server_requests_total", map[string]string{
		"route":  routeHealthz,
		"method": http.MethodGet,
		"code":   "200",
	}, 1)
	assertHTTPMetricCounter(t, families, "starmap_http_server_requests_total", map[string]string{
		"route":  "unknown",
		"method": http.MethodGet,
		"code":   "404",
	}, 1)
}

func assertHTTPMetricCounter(t *testing.T, families []*dto.MetricFamily, name string, labels map[string]string, want float64) {
	t.Helper()

	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if httpMetricMatches(metric, labels) {
				if metric.GetCounter().GetValue() != want {
					t.Fatalf("metric %s labels=%v want=%v got=%v", name, labels, want, metric.GetCounter().GetValue())
				}
				return
			}
		}
	}

	t.Fatalf("metric %s labels=%v not found", name, labels)
}

func httpMetricMatches(metric *dto.Metric, labels map[string]string) bool {
	if len(metric.GetLabel()) != len(labels) {
		return false
	}
	for _, label := range metric.GetLabel() {
		if labels[label.GetName()] != label.GetValue() {
			return false
		}
	}
	return true
}
