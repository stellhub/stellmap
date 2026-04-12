package httptransport

import (
	"net/http"
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
	mux *http.ServeMux
}

// NewPublicServer 创建公共 HTTP 服务骨架。
//
// 公共 HTTP 只承载对外数据面和健康检查，不再暴露成员变更、Leader 转移等控制面接口。
func NewPublicServer(registry RegistryHandler, health HealthHandler) *Server {
	mux := http.NewServeMux()

	if registry != nil {
		registerRegistryRoutes(mux, registry)
	}
	if health != nil {
		registerHealthRoutes(mux, health)
	}

	return &Server{mux: mux}
}

// NewAdminServer 创建独立的 admin HTTP 服务骨架。
//
// admin server 只承载状态查询、成员变更和 Leader 转移等控制面能力，
// 由 starmapctl 这类受控入口使用，不对业务客户端开放。
func NewAdminServer(control ControlHandler) *Server {
	mux := http.NewServeMux()
	if control != nil {
		registerControlRoutes(mux, control)
	}

	return &Server{mux: mux}
}

// Handler 返回根 HTTP handler。
func (s *Server) Handler() http.Handler {
	return s.mux
}
