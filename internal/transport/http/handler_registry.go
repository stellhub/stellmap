package httptransport

import (
	"net/http"
)

func registerRegistryRoutes(mux *http.ServeMux, handler RegistryHandler) {
	mux.HandleFunc("/api/v1/registry/register", handler.Register)
	mux.HandleFunc("/api/v1/registry/deregister", handler.Deregister)
	mux.HandleFunc("/api/v1/registry/heartbeat", handler.Heartbeat)
	mux.HandleFunc("/api/v1/registry/instances", handler.QueryInstances)
	mux.HandleFunc("/api/v1/kv", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handler.List(w, r)
		case http.MethodDelete:
			handler.DeletePrefix(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/v1/kv/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			handler.PutKV(w, r)
		case http.MethodGet:
			handler.Get(w, r)
		case http.MethodDelete:
			handler.DeleteKV(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
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

// PutKV 处理单 key 写入请求。
func (NoopRegistryHandler) PutKV(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "put kv not implemented", http.StatusNotImplemented)
}

// Get 处理单 key 查询请求。
func (NoopRegistryHandler) Get(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "get not implemented", http.StatusNotImplemented)
}

// DeleteKV 处理单 key 删除请求。
func (NoopRegistryHandler) DeleteKV(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "delete kv not implemented", http.StatusNotImplemented)
}

// List 处理前缀扫描请求。
func (NoopRegistryHandler) List(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "list not implemented", http.StatusNotImplemented)
}

// DeletePrefix 处理按前缀删除请求。
func (NoopRegistryHandler) DeletePrefix(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "delete prefix not implemented", http.StatusNotImplemented)
}
