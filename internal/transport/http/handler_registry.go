package httptransport

import (
	"net/http"
)

const (
	routeRegistryRegister          = "/api/v1/registry/register"
	routeRegistryDeregister        = "/api/v1/registry/deregister"
	routeRegistryHeartbeat         = "/api/v1/registry/heartbeat"
	routeRegistryInstances         = "/api/v1/registry/instances"
	routeRegistryWatch             = "/api/v1/registry/watch"
	routeReplicationWatch          = "/internal/v1/replication/watch"
	routePrometheusServiceDiscover = "/internal/v1/prometheus/sd"
)

func registerRegistryRoutes(mux *http.ServeMux, handler RegistryHandler) {
	mux.HandleFunc(routeRegistryRegister, handler.Register)
	mux.HandleFunc(routeRegistryDeregister, handler.Deregister)
	mux.HandleFunc(routeRegistryHeartbeat, handler.Heartbeat)
	mux.HandleFunc(routeRegistryInstances, handler.QueryInstances)
	mux.HandleFunc(routeRegistryWatch, handler.WatchInstances)
	mux.HandleFunc(routeReplicationWatch, handler.WatchReplication)
	mux.HandleFunc(routePrometheusServiceDiscover, handler.PrometheusSD)
}

// NoopRegistryHandler 是对外数据面 handler 的最小占位实现。
type NoopRegistryHandler struct{}

// Register 处理注册请求。
func (NoopRegistryHandler) Register(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "register not implemented", http.StatusNotImplemented)
}

// Deregister 处理注销请求。
func (NoopRegistryHandler) Deregister(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "deregister not implemented", http.StatusNotImplemented)
}

// Heartbeat 处理续约请求。
func (NoopRegistryHandler) Heartbeat(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "heartbeat not implemented", http.StatusNotImplemented)
}

// QueryInstances 处理实例查询请求。
func (NoopRegistryHandler) QueryInstances(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "query instances not implemented", http.StatusNotImplemented)
}

// WatchInstances 处理实例 watch 请求。
func (NoopRegistryHandler) WatchInstances(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "watch instances not implemented", http.StatusNotImplemented)
}

// WatchReplication 处理跨 region 目录同步 watch 请求。
func (NoopRegistryHandler) WatchReplication(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "watch replication not implemented", http.StatusNotImplemented)
}

// PrometheusSD 处理 Prometheus HTTP SD 请求。
func (NoopRegistryHandler) PrometheusSD(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "prometheus sd not implemented", http.StatusNotImplemented)
}
