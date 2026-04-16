package grpctransport

import (
	"context"
	"testing"

	starmapv1 "github.com/chenwenlong-java/StarMap/api/gen/go/starmap/v1"
	internalmetrics "github.com/chenwenlong-java/StarMap/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"google.golang.org/grpc/metadata"
)

type fakeService struct {
	downloadChunks []SnapshotChunk
}

func (f *fakeService) SendRaftMessages(ctx context.Context, batch RaftMessageBatch) error {
	return nil
}

func (f *fakeService) InstallSnapshotChunk(ctx context.Context, chunk SnapshotChunk) error {
	return nil
}

func (f *fakeService) DownloadSnapshot(ctx context.Context, term, index uint64) ([]SnapshotChunk, error) {
	return append([]SnapshotChunk(nil), f.downloadChunks...), nil
}

type fakeDownloadServer struct {
	ctx  context.Context
	sent []*starmapv1.DownloadSnapshotChunk
}

func (f *fakeDownloadServer) SetHeader(md metadata.MD) error  { return nil }
func (f *fakeDownloadServer) SendHeader(md metadata.MD) error { return nil }
func (f *fakeDownloadServer) SetTrailer(md metadata.MD)       {}
func (f *fakeDownloadServer) Context() context.Context {
	if f.ctx != nil {
		return f.ctx
	}
	return context.Background()
}
func (f *fakeDownloadServer) SendMsg(m interface{}) error { return nil }
func (f *fakeDownloadServer) RecvMsg(m interface{}) error { return nil }
func (f *fakeDownloadServer) Send(chunk *starmapv1.DownloadSnapshotChunk) error {
	f.sent = append(f.sent, chunk)
	return nil
}

func TestServerMetricsObserveUnaryAndStreamRPC(t *testing.T) {
	registry := prometheus.NewRegistry()
	transportMetrics := internalmetrics.NewTransportMetrics()
	if err := transportMetrics.Register(registry); err != nil {
		t.Fatalf("register transport metrics failed: %v", err)
	}

	service := &fakeService{
		downloadChunks: []SnapshotChunk{
			{
				Metadata: SnapshotMetadata{Term: 1, Index: 2, FileSize: 4},
				Data:     []byte("test"),
				Offset:   0,
				EOF:      true,
			},
		},
	}
	server := NewServer(service).WithMetrics(transportMetrics.GRPC())

	raftBatch := &starmapv1.RaftMessageBatch{
		Messages: []*starmapv1.RaftEnvelope{
			{From: 1, To: 2, Payload: []byte("raft-a")},
			{From: 1, To: 2, Payload: []byte("raft-b")},
		},
	}
	if _, err := server.Send(context.Background(), raftBatch); err != nil {
		t.Fatalf("send raft batch failed: %v", err)
	}

	downloadServer := &fakeDownloadServer{}
	if err := server.Download(&starmapv1.DownloadSnapshotRequest{Term: 1, Index: 2}, downloadServer); err != nil {
		t.Fatalf("download snapshot failed: %v", err)
	}
	if len(downloadServer.sent) != 1 {
		t.Fatalf("unexpected sent chunk count: %d", len(downloadServer.sent))
	}

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics failed: %v", err)
	}

	assertGRPCCounterValue(t, families, "starmap_grpc_server_requests_total", map[string]string{
		"method":   methodRaftSend,
		"rpc_type": rpcTypeUnary,
		"code":     "OK",
	}, 1)
	assertGRPCCounterValue(t, families, "starmap_grpc_server_requests_total", map[string]string{
		"method":   methodSnapshotDownload,
		"rpc_type": rpcTypeServerStream,
		"code":     "OK",
	}, 1)
	assertGRPCHistogramCount(t, families, "starmap_grpc_server_raft_batch_messages", map[string]string{
		"method": methodRaftSend,
	}, 1)
	assertGRPCHistogramCount(t, families, "starmap_grpc_server_snapshot_bytes", map[string]string{
		"method":    methodSnapshotDownload,
		"direction": "send",
	}, 1)
}

func assertGRPCCounterValue(t *testing.T, families []*dto.MetricFamily, name string, labels map[string]string, want float64) {
	t.Helper()

	metric := findGRPCMetric(t, families, name, labels)
	if metric.GetCounter().GetValue() != want {
		t.Fatalf("metric %s labels=%v want=%v got=%v", name, labels, want, metric.GetCounter().GetValue())
	}
}

func assertGRPCHistogramCount(t *testing.T, families []*dto.MetricFamily, name string, labels map[string]string, want uint64) {
	t.Helper()

	metric := findGRPCMetric(t, families, name, labels)
	if metric.GetHistogram().GetSampleCount() != want {
		t.Fatalf("metric %s labels=%v want count=%d got=%d", name, labels, want, metric.GetHistogram().GetSampleCount())
	}
}

func findGRPCMetric(t *testing.T, families []*dto.MetricFamily, name string, labels map[string]string) *dto.Metric {
	t.Helper()

	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if grpcMetricMatches(metric, labels) {
				return metric
			}
		}
	}

	t.Fatalf("metric %s labels=%v not found", name, labels)
	return nil
}

func grpcMetricMatches(metric *dto.Metric, labels map[string]string) bool {
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

var _ starmapv1.SnapshotService_DownloadServer = (*fakeDownloadServer)(nil)
