package httptransport

import (
	"net/http"
)

func registerHealthRoutes(mux *http.ServeMux, handler HealthHandler) {
	mux.HandleFunc("/healthz", handler.Healthz)
	mux.HandleFunc("/readyz", handler.Readyz)
	mux.HandleFunc("/metrics", handler.Metrics)
}

// NoopHealthHandler 是健康检查 handler 的最小占位实现。
type NoopHealthHandler struct{}

// Healthz 处理存活探测。
func (NoopHealthHandler) Healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// Readyz 处理就绪探测。
func (NoopHealthHandler) Readyz(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not ready", http.StatusServiceUnavailable)
}

// Metrics 处理基础指标暴露。
func (NoopHealthHandler) Metrics(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "metrics not implemented", http.StatusNotImplemented)
}
