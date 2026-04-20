package httptransport

import (
	"net/http"
	"time"

	"github.com/stellhub/stellmap/internal/metrics"
)

// RegistryHandler 描述注册中心 HTTP 数据面需要实现的能力。
type RegistryHandler interface {
	Register(w http.ResponseWriter, r *http.Request)
	Deregister(w http.ResponseWriter, r *http.Request)
	Heartbeat(w http.ResponseWriter, r *http.Request)
	QueryInstances(w http.ResponseWriter, r *http.Request)
	WatchInstances(w http.ResponseWriter, r *http.Request)
	WatchReplication(w http.ResponseWriter, r *http.Request)
	PrometheusSD(w http.ResponseWriter, r *http.Request)
}

// HealthHandler 描述健康检查能力。
type HealthHandler interface {
	Healthz(w http.ResponseWriter, r *http.Request)
	Readyz(w http.ResponseWriter, r *http.Request)
	Metrics(w http.ResponseWriter, r *http.Request)
}

// ControlHandler 描述控制面能力。
type ControlHandler interface {
	Status(w http.ResponseWriter, r *http.Request)
	ReplicationStatus(w http.ResponseWriter, r *http.Request)
	AddLearner(w http.ResponseWriter, r *http.Request)
	PromoteLearner(w http.ResponseWriter, r *http.Request)
	RemoveMember(w http.ResponseWriter, r *http.Request)
	TransferLeader(w http.ResponseWriter, r *http.Request)
}

// Server 是 HTTP 服务骨架。
type Server struct {
	mux     *http.ServeMux
	metrics *metrics.HTTPServerMetrics
	routes  map[string]struct{}
}

// NewPublicServer 创建公共 HTTP 服务骨架。
//
// 公共 HTTP 只承载对外数据面和健康检查，不再暴露成员变更、Leader 转移等控制面接口。
func NewPublicServer(registry RegistryHandler, health HealthHandler) *Server {
	mux := http.NewServeMux()
	routes := make(map[string]struct{})

	if registry != nil {
		registerRegistryRoutes(mux, registry)
		for _, route := range publicRegistryRoutes() {
			routes[route] = struct{}{}
		}
	}
	if health != nil {
		registerHealthRoutes(mux, health)
		for _, route := range publicHealthRoutes() {
			routes[route] = struct{}{}
		}
	}

	return &Server{mux: mux, routes: routes}
}

// NewAdminServer 创建独立的 admin HTTP 服务骨架。
//
// admin server 只承载状态查询、成员变更和 Leader 转移等控制面能力，
// 由 stellmapctl 这类受控入口使用，不对业务客户端开放。
func NewAdminServer(control ControlHandler) *Server {
	mux := http.NewServeMux()
	routes := make(map[string]struct{})
	if control != nil {
		registerControlRoutes(mux, control)
		for _, route := range adminControlRoutes() {
			routes[route] = struct{}{}
		}
	}

	return &Server{mux: mux, routes: routes}
}

// WithMetrics 为当前 HTTP server 增加轻量 transport 指标。
func (s *Server) WithMetrics(m *metrics.HTTPServerMetrics) *Server {
	if s == nil {
		return nil
	}
	s.metrics = m
	return s
}

// Handler 返回根 HTTP handler。
func (s *Server) Handler() http.Handler {
	if s == nil {
		return http.NewServeMux()
	}
	if s.metrics == nil {
		return s.mux
	}

	return newInstrumentedMux(s.mux, s.routes, s.metrics)
}

func publicRegistryRoutes() []string {
	return []string{
		routeRegistryRegister,
		routeRegistryDeregister,
		routeRegistryHeartbeat,
		routeRegistryInstances,
		routeRegistryWatch,
		routeReplicationWatch,
		routePrometheusServiceDiscover,
	}
}

func publicHealthRoutes() []string {
	return []string{
		routeHealthz,
		routeReadyz,
		routeMetrics,
	}
}

func adminControlRoutes() []string {
	return []string{
		routeAdminStatus,
		routeAdminReplicationStatus,
		routeAdminAddLearner,
		routeAdminPromoteLearner,
		routeAdminRemoveMember,
		routeAdminTransferLeader,
	}
}

func newInstrumentedMux(next http.Handler, routes map[string]struct{}, metrics *metrics.HTTPServerMetrics) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := r.URL.Path
		if _, ok := routes[route]; !ok {
			route = "unknown"
		}

		metrics.IncInflight(route)
		defer metrics.DecInflight(route)

		startedAt := time.Now()
		writer := newStatusCapturingResponseWriter(w)
		next.ServeHTTP(writer, r)
		metrics.ObserveRequest(route, r.Method, writer.statusCode(), time.Since(startedAt))
	})
}
