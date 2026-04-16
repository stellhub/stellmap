package httptransport

import "net/http"

const (
	routeAdminStatus            = "/admin/v1/status"
	routeAdminReplicationStatus = "/admin/v1/replication/status"
	routeAdminAddLearner        = "/admin/v1/members/add-learner"
	routeAdminPromoteLearner    = "/admin/v1/members/promote"
	routeAdminRemoveMember      = "/admin/v1/members/remove"
	routeAdminTransferLeader    = "/admin/v1/leader/transfer"
)

func registerControlRoutes(mux *http.ServeMux, handler ControlHandler) {
	mux.HandleFunc(routeAdminStatus, handler.Status)
	mux.HandleFunc(routeAdminReplicationStatus, handler.ReplicationStatus)
	mux.HandleFunc(routeAdminAddLearner, handler.AddLearner)
	mux.HandleFunc(routeAdminPromoteLearner, handler.PromoteLearner)
	mux.HandleFunc(routeAdminRemoveMember, handler.RemoveMember)
	mux.HandleFunc(routeAdminTransferLeader, handler.TransferLeader)
}
