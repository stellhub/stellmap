package metrics

import (
	"errors"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/status"
)

const (
	snapshotDirectionRecv = "recv"
	snapshotDirectionSend = "send"
)

// TransportMetrics 收敛 StellMap transport 层指标。
func NewTransportMetrics() *TransportMetrics {
	return &TransportMetrics{
		http: newHTTPServerMetrics(),
		grpc: newGRPCServerMetrics(),
	}
}

// TransportMetrics 统一管理 HTTP 和 gRPC 的 transport 指标。
type TransportMetrics struct {
	http *HTTPServerMetrics
	grpc *GRPCServerMetrics
}

// HTTP 返回 HTTP 服务端指标句柄。
func (m *TransportMetrics) HTTP() *HTTPServerMetrics {
	if m == nil {
		return nil
	}
	return m.http
}

// GRPC 返回 gRPC 服务端指标句柄。
func (m *TransportMetrics) GRPC() *GRPCServerMetrics {
	if m == nil {
		return nil
	}
	return m.grpc
}

// Register 将 transport 指标注册到指定 registry。
func (m *TransportMetrics) Register(registerer prometheus.Registerer) error {
	if m == nil || registerer == nil {
		return nil
	}

	var errs []error
	for _, collector := range append(m.http.collectors(), m.grpc.collectors()...) {
		if err := registerer.Register(collector); err != nil {
			var registeredErr prometheus.AlreadyRegisteredError
			if errors.As(err, &registeredErr) {
				continue
			}
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// HTTPServerMetrics 描述 HTTP 服务端指标。
type HTTPServerMetrics struct {
	inflight *prometheus.GaugeVec
	requests *prometheus.CounterVec
	latency  *prometheus.HistogramVec
}

func newHTTPServerMetrics() *HTTPServerMetrics {
	return &HTTPServerMetrics{
		inflight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "stellmap_http_server_inflight_requests",
			Help: "Current in-flight HTTP requests grouped by route.",
		}, []string{"route"}),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "stellmap_http_server_requests_total",
			Help: "Total HTTP requests handled by StellMap.",
		}, []string{"route", "method", "code"}),
		latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "stellmap_http_server_request_duration_seconds",
			Help:    "HTTP request latency in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route", "method", "code"}),
	}
}

func (m *HTTPServerMetrics) collectors() []prometheus.Collector {
	if m == nil {
		return nil
	}
	return []prometheus.Collector{m.inflight, m.requests, m.latency}
}

// IncInflight 标记一个新的 HTTP 在途请求。
func (m *HTTPServerMetrics) IncInflight(route string) {
	if m == nil {
		return
	}
	m.inflight.WithLabelValues(route).Inc()
}

// DecInflight 标记一个 HTTP 在途请求结束。
func (m *HTTPServerMetrics) DecInflight(route string) {
	if m == nil {
		return
	}
	m.inflight.WithLabelValues(route).Dec()
}

// ObserveRequest 记录一次 HTTP 请求结果。
func (m *HTTPServerMetrics) ObserveRequest(route, method string, statusCode int, duration time.Duration) {
	if m == nil {
		return
	}

	code := strconv.Itoa(statusCode)
	m.requests.WithLabelValues(route, method, code).Inc()
	m.latency.WithLabelValues(route, method, code).Observe(duration.Seconds())
}

// GRPCServerMetrics 描述 gRPC 服务端指标。
type GRPCServerMetrics struct {
	inflight           *prometheus.GaugeVec
	requests           *prometheus.CounterVec
	latency            *prometheus.HistogramVec
	raftBatchMessages  *prometheus.HistogramVec
	raftBatchBytes     *prometheus.HistogramVec
	snapshotChunkCount *prometheus.HistogramVec
	snapshotBytes      *prometheus.HistogramVec
}

func newGRPCServerMetrics() *GRPCServerMetrics {
	return &GRPCServerMetrics{
		inflight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "stellmap_grpc_server_inflight_requests",
			Help: "Current in-flight gRPC requests grouped by method and rpc type.",
		}, []string{"method", "rpc_type"}),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "stellmap_grpc_server_requests_total",
			Help: "Total gRPC requests handled by StellMap.",
		}, []string{"method", "rpc_type", "code"}),
		latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "stellmap_grpc_server_request_duration_seconds",
			Help:    "gRPC request latency in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "rpc_type", "code"}),
		raftBatchMessages: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "stellmap_grpc_server_raft_batch_messages",
			Help:    "Number of raft messages carried by a single gRPC batch.",
			Buckets: []float64{1, 2, 4, 8, 16, 32, 64, 128},
		}, []string{"method"}),
		raftBatchBytes: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "stellmap_grpc_server_raft_batch_payload_bytes",
			Help:    "Serialized raft payload bytes carried by a single gRPC batch.",
			Buckets: []float64{256, 1024, 4096, 16384, 65536, 262144, 1048576},
		}, []string{"method"}),
		snapshotChunkCount: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "stellmap_grpc_server_snapshot_chunks",
			Help:    "Number of snapshot chunks sent or received in a single gRPC request.",
			Buckets: []float64{1, 2, 4, 8, 16, 32, 64, 128, 256},
		}, []string{"method", "direction"}),
		snapshotBytes: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "stellmap_grpc_server_snapshot_bytes",
			Help:    "Snapshot bytes sent or received in a single gRPC request.",
			Buckets: []float64{64 << 10, 256 << 10, 1 << 20, 4 << 20, 16 << 20, 64 << 20, 256 << 20, 1 << 30},
		}, []string{"method", "direction"}),
	}
}

func (m *GRPCServerMetrics) collectors() []prometheus.Collector {
	if m == nil {
		return nil
	}
	return []prometheus.Collector{
		m.inflight,
		m.requests,
		m.latency,
		m.raftBatchMessages,
		m.raftBatchBytes,
		m.snapshotChunkCount,
		m.snapshotBytes,
	}
}

// IncInflight 标记一个新的 gRPC 在途请求。
func (m *GRPCServerMetrics) IncInflight(method, rpcType string) {
	if m == nil {
		return
	}
	m.inflight.WithLabelValues(method, rpcType).Inc()
}

// DecInflight 标记一个 gRPC 在途请求结束。
func (m *GRPCServerMetrics) DecInflight(method, rpcType string) {
	if m == nil {
		return
	}
	m.inflight.WithLabelValues(method, rpcType).Dec()
}

// ObserveRequest 记录一次 gRPC 请求结果。
func (m *GRPCServerMetrics) ObserveRequest(method, rpcType string, err error, duration time.Duration) {
	if m == nil {
		return
	}

	code := status.Code(err).String()
	m.requests.WithLabelValues(method, rpcType, code).Inc()
	m.latency.WithLabelValues(method, rpcType, code).Observe(duration.Seconds())
}

// ObserveRaftBatch 记录一次 Raft 消息批次规模。
func (m *GRPCServerMetrics) ObserveRaftBatch(method string, messages int, bytes int) {
	if m == nil {
		return
	}

	m.raftBatchMessages.WithLabelValues(method).Observe(float64(messages))
	m.raftBatchBytes.WithLabelValues(method).Observe(float64(bytes))
}

// ObserveSnapshotRecv 记录一次快照接收规模。
func (m *GRPCServerMetrics) ObserveSnapshotRecv(method string, chunks int, bytes int) {
	m.observeSnapshotTransfer(method, snapshotDirectionRecv, chunks, bytes)
}

// ObserveSnapshotSend 记录一次快照发送规模。
func (m *GRPCServerMetrics) ObserveSnapshotSend(method string, chunks int, bytes int) {
	m.observeSnapshotTransfer(method, snapshotDirectionSend, chunks, bytes)
}

func (m *GRPCServerMetrics) observeSnapshotTransfer(method, direction string, chunks int, bytes int) {
	if m == nil {
		return
	}

	m.snapshotChunkCount.WithLabelValues(method, direction).Observe(float64(chunks))
	m.snapshotBytes.WithLabelValues(method, direction).Observe(float64(bytes))
}
