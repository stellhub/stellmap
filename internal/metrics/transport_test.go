package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestTransportMetricsRegisterAndObserve(t *testing.T) {
	registry := prometheus.NewRegistry()
	transportMetrics := NewTransportMetrics()
	if err := transportMetrics.Register(registry); err != nil {
		t.Fatalf("register transport metrics failed: %v", err)
	}

	transportMetrics.HTTP().IncInflight("/healthz")
	transportMetrics.HTTP().ObserveRequest("/healthz", "GET", 200, 25*time.Millisecond)
	transportMetrics.HTTP().DecInflight("/healthz")

	transportMetrics.GRPC().IncInflight("/stellmap.v1.RaftTransport/Send", "unary")
	transportMetrics.GRPC().ObserveRaftBatch("/stellmap.v1.RaftTransport/Send", 3, 512)
	transportMetrics.GRPC().ObserveSnapshotRecv("/stellmap.v1.SnapshotService/Install", 2, 4096)
	transportMetrics.GRPC().ObserveSnapshotSend("/stellmap.v1.SnapshotService/Download", 4, 8192)
	transportMetrics.GRPC().ObserveRequest("/stellmap.v1.RaftTransport/Send", "unary", nil, 10*time.Millisecond)
	transportMetrics.GRPC().DecInflight("/stellmap.v1.RaftTransport/Send", "unary")

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics failed: %v", err)
	}

	assertCounterValue(t, families, "stellmap_http_server_requests_total", map[string]string{
		"route":  "/healthz",
		"method": "GET",
		"code":   "200",
	}, 1)
	assertGaugeValue(t, families, "stellmap_http_server_inflight_requests", map[string]string{
		"route": "/healthz",
	}, 0)
	assertCounterValue(t, families, "stellmap_grpc_server_requests_total", map[string]string{
		"method":   "/stellmap.v1.RaftTransport/Send",
		"rpc_type": "unary",
		"code":     "OK",
	}, 1)
	assertHistogramCount(t, families, "stellmap_grpc_server_raft_batch_messages", map[string]string{
		"method": "/stellmap.v1.RaftTransport/Send",
	}, 1)
	assertHistogramCount(t, families, "stellmap_grpc_server_snapshot_chunks", map[string]string{
		"method":    "/stellmap.v1.SnapshotService/Install",
		"direction": "recv",
	}, 1)
	assertHistogramCount(t, families, "stellmap_grpc_server_snapshot_bytes", map[string]string{
		"method":    "/stellmap.v1.SnapshotService/Download",
		"direction": "send",
	}, 1)
}

func assertCounterValue(t *testing.T, families []*dto.MetricFamily, name string, labels map[string]string, want float64) {
	t.Helper()

	metric := findMetric(t, families, name, labels)
	if metric.GetCounter().GetValue() != want {
		t.Fatalf("metric %s labels=%v want counter=%v got=%v", name, labels, want, metric.GetCounter().GetValue())
	}
}

func assertGaugeValue(t *testing.T, families []*dto.MetricFamily, name string, labels map[string]string, want float64) {
	t.Helper()

	metric := findMetric(t, families, name, labels)
	if metric.GetGauge().GetValue() != want {
		t.Fatalf("metric %s labels=%v want gauge=%v got=%v", name, labels, want, metric.GetGauge().GetValue())
	}
}

func assertHistogramCount(t *testing.T, families []*dto.MetricFamily, name string, labels map[string]string, want uint64) {
	t.Helper()

	metric := findMetric(t, families, name, labels)
	if metric.GetHistogram().GetSampleCount() != want {
		t.Fatalf("metric %s labels=%v want histogram_count=%d got=%d", name, labels, want, metric.GetHistogram().GetSampleCount())
	}
}

func findMetric(t *testing.T, families []*dto.MetricFamily, name string, labels map[string]string) *dto.Metric {
	t.Helper()

	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			if metricMatches(metric, labels) {
				return metric
			}
		}
	}

	t.Fatalf("metric %s with labels=%v not found", name, labels)
	return nil
}

func metricMatches(metric *dto.Metric, labels map[string]string) bool {
	if len(metric.GetLabel()) != len(labels) {
		return false
	}
	for _, pair := range metric.GetLabel() {
		if labels[pair.GetName()] != pair.GetValue() {
			return false
		}
	}
	return true
}
