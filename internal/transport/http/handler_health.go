package httptransport

import (
	"net/http"
)

func registerHealthRoutes(mux *http.ServeMux, handler HealthHandler) {
	mux.HandleFunc("/healthz", handler.Healthz)
	mux.HandleFunc("/readyz", handler.Readyz)
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
