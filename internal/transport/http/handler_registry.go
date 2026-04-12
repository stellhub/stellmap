package httptransport

import (
	"net/http"
)

func registerRegistryRoutes(mux *http.ServeMux, handler RegistryHandler) {
	mux.HandleFunc("/api/v1/registry/register", handler.Register)
	mux.HandleFunc("/api/v1/registry/deregister", handler.Deregister)
	mux.HandleFunc("/api/v1/registry/heartbeat", handler.Heartbeat)
	mux.HandleFunc("/api/v1/registry/instances", handler.QueryInstances)
	mux.HandleFunc("/api/v1/registry/watch", handler.WatchInstances)
	mux.HandleFunc("/internal/v1/replication/watch", handler.WatchReplication)
	mux.HandleFunc("/internal/v1/prometheus/sd", handler.PrometheusSD)
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
