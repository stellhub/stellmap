package httptransport

import "net/http"

func registerControlRoutes(mux *http.ServeMux, handler ControlHandler) {
	mux.HandleFunc("/admin/v1/status", handler.Status)
	mux.HandleFunc("/admin/v1/members/add-learner", handler.AddLearner)
	mux.HandleFunc("/admin/v1/members/promote", handler.PromoteLearner)
	mux.HandleFunc("/admin/v1/members/remove", handler.RemoveMember)
	mux.HandleFunc("/admin/v1/leader/transfer", handler.TransferLeader)
}
