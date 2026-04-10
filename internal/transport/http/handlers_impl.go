package httptransport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/chenwenlong-java/StarMap/internal/raftnode"
	"github.com/chenwenlong-java/StarMap/internal/registry"
	"github.com/chenwenlong-java/StarMap/internal/runtime"
	"github.com/chenwenlong-java/StarMap/internal/storage"
)

// RegistryAPI 实现对外 HTTP 数据面。
type RegistryAPI struct {
	node          *raftnode.RaftNode
	httpAddr      string
	book          *runtime.AddressBook
	requestTimout time.Duration
}

// HealthAPI 实现对外健康检查。
type HealthAPI struct {
	node     *raftnode.RaftNode
	httpAddr string
	book     *runtime.AddressBook
}

// ControlAPI 实现控制面 HTTP 接口。
type ControlAPI struct {
	node          *raftnode.RaftNode
	clusterID     uint64
	adminAddr     string
	book          *runtime.AddressBook
	peerTransport *runtime.PeerTransport
	requestTimout time.Duration
}

// NewRegistryHandler 创建注册中心 HTTP 数据面 handler。
func NewRegistryHandler(node *raftnode.RaftNode, httpAddr string, book *runtime.AddressBook, requestTimeout time.Duration) *RegistryAPI {
	return &RegistryAPI{
		node:          node,
		httpAddr:      httpAddr,
		book:          book,
		requestTimout: requestTimeout,
	}
}

// NewHealthHandler 创建健康检查 handler。
func NewHealthHandler(node *raftnode.RaftNode, httpAddr string, book *runtime.AddressBook) *HealthAPI {
	return &HealthAPI{
		node:     node,
		httpAddr: httpAddr,
		book:     book,
	}
}

// NewControlHandler 创建控制面 handler。
func NewControlHandler(node *raftnode.RaftNode, clusterID uint64, adminAddr string, book *runtime.AddressBook, peerTransport *runtime.PeerTransport, requestTimeout time.Duration) *ControlAPI {
	return &ControlAPI{
		node:          node,
		clusterID:     clusterID,
		adminAddr:     adminAddr,
		book:          book,
		peerTransport: peerTransport,
		requestTimout: requestTimeout,
	}
}

// Register 处理实例注册。
func (h *RegistryAPI) Register(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}

	var request RegisterRequestDTO
	if err := decodeJSONBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	input := registryRegisterInputFromDTO(request)
	registry.NormalizeInstanceIdentity(&input.Namespace, &input.Service, &input.InstanceID)
	if err := registry.NormalizeRegisterInput(&input); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if input.Namespace == "" || input.Service == "" || input.InstanceID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "namespace, service and instanceId are required")
		return
	}
	if !h.ensureWritable(w) {
		return
	}

	now := time.Now().Unix()
	value, err := json.Marshal(registry.NewValue(input, now))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "marshal_failed", err.Error())
		return
	}

	if err := h.propose(r.Context(), storage.Command{
		Operation: storage.OperationPut,
		Key:       registry.Key(input.Namespace, input.Service, input.InstanceID),
		Value:     value,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "propose_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, SuccessResponse{
		Code:    "ok",
		Message: "instance registered",
	})
}

// Deregister 处理实例注销。
func (h *RegistryAPI) Deregister(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}

	var request DeregisterRequestDTO
	if err := decodeJSONBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	registry.NormalizeInstanceIdentity(&request.Namespace, &request.Service, &request.InstanceID)
	if request.Namespace == "" || request.Service == "" || request.InstanceID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "namespace, service and instanceId are required")
		return
	}
	if !h.ensureWritable(w) {
		return
	}

	if err := h.propose(r.Context(), storage.Command{
		Operation: storage.OperationDelete,
		Key:       registry.Key(request.Namespace, request.Service, request.InstanceID),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "propose_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, SuccessResponse{
		Code:    "ok",
		Message: "instance deregistered",
	})
}

// Heartbeat 处理实例续约。
func (h *RegistryAPI) Heartbeat(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}

	var request HeartbeatRequestDTO
	if err := decodeJSONBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	registry.NormalizeInstanceIdentity(&request.Namespace, &request.Service, &request.InstanceID)
	if request.Namespace == "" || request.Service == "" || request.InstanceID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "namespace, service and instanceId are required")
		return
	}
	if request.LeaseTTLSeconds < 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "leaseTtlSeconds must be greater than or equal to 0")
		return
	}
	if !h.ensureWritable(w) {
		return
	}

	key := registry.Key(request.Namespace, request.Service, request.InstanceID)
	ctx, cancel := context.WithTimeout(r.Context(), h.requestTimout)
	defer cancel()

	current, err := h.node.Get(ctx, key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read_failed", err.Error())
		return
	}
	if len(current) == 0 {
		writeError(w, http.StatusNotFound, "not_found", "instance not found")
		return
	}

	var existing registry.Value
	if err := json.Unmarshal(current, &existing); err != nil {
		writeError(w, http.StatusInternalServerError, "unmarshal_failed", err.Error())
		return
	}
	if request.LeaseTTLSeconds > 0 {
		existing.LeaseTTLSeconds = request.LeaseTTLSeconds
	} else if existing.LeaseTTLSeconds <= 0 {
		existing.LeaseTTLSeconds = registry.EffectiveLeaseTTLSeconds(existing.LeaseTTLSeconds)
	}
	existing.LastHeartbeatUnix = time.Now().Unix()

	value, err := json.Marshal(existing)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "marshal_failed", err.Error())
		return
	}
	if err := h.propose(r.Context(), storage.Command{
		Operation: storage.OperationPut,
		Key:       key,
		Value:     value,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "propose_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, SuccessResponse{
		Code:    "ok",
		Message: "heartbeat accepted",
	})
}

// QueryInstances 按条件过滤实例候选集。
func (h *RegistryAPI) QueryInstances(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}

	query, err := registry.ParseQuery(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	start, end := prefixRange(registry.ServicePrefix(query.Namespace, query.Service))
	ctx, cancel := context.WithTimeout(r.Context(), h.requestTimout)
	defer cancel()

	items, err := h.node.Scan(ctx, start, end, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "scan_failed", err.Error())
		return
	}

	now := time.Now().Unix()
	result := make([]RegistryInstanceDTO, 0, len(items))
	for _, item := range items {
		var value registry.Value
		if err := json.Unmarshal(item.Value, &value); err != nil {
			continue
		}
		if !registry.IsAlive(value, now) {
			continue
		}
		if query.Zone != "" && value.Zone != query.Zone {
			continue
		}
		if !registry.MatchesLabelSelector(value.Labels, query.Selector) {
			continue
		}

		endpoints := registry.FilterEndpoints(value.Endpoints, query.Endpoint)
		if query.Endpoint != "" && len(endpoints) == 0 {
			continue
		}
		if query.Endpoint == "" {
			endpoints = registry.CloneEndpoints(value.Endpoints)
		}

		result = append(result, RegistryInstanceDTO{
			Namespace:         value.Namespace,
			Service:           value.Service,
			InstanceID:        value.InstanceID,
			Zone:              value.Zone,
			Labels:            registry.CloneStringMap(value.Labels),
			Metadata:          registry.CloneStringMap(value.Metadata),
			Endpoints:         endpointDTOsFromRegistry(endpoints),
			LeaseTTLSeconds:   registry.EffectiveLeaseTTLSeconds(value.LeaseTTLSeconds),
			RegisteredAtUnix:  value.RegisteredAtUnix,
			LastHeartbeatUnix: value.LastHeartbeatUnix,
		})
		if query.Limit > 0 && len(result) >= query.Limit {
			break
		}
	}

	writeJSON(w, http.StatusOK, SuccessResponse{Code: "ok", Data: result})
}

func registryRegisterInputFromDTO(request RegisterRequestDTO) registry.RegisterInput {
	return registry.RegisterInput{
		Namespace:       request.Namespace,
		Service:         request.Service,
		InstanceID:      request.InstanceID,
		Zone:            request.Zone,
		Labels:          request.Labels,
		Metadata:        request.Metadata,
		Endpoints:       registryEndpointsFromDTO(request.Endpoints),
		LeaseTTLSeconds: request.LeaseTTLSeconds,
	}
}

func registryEndpointsFromDTO(items []EndpointDTO) []registry.Endpoint {
	if len(items) == 0 {
		return nil
	}

	result := make([]registry.Endpoint, 0, len(items))
	for _, item := range items {
		result = append(result, registry.Endpoint{
			Name:     item.Name,
			Protocol: item.Protocol,
			Host:     item.Host,
			Port:     item.Port,
			Path:     item.Path,
			Weight:   item.Weight,
		})
	}

	return result
}

func endpointDTOsFromRegistry(items []registry.Endpoint) []EndpointDTO {
	if len(items) == 0 {
		return nil
	}

	result := make([]EndpointDTO, 0, len(items))
	for _, item := range items {
		result = append(result, EndpointDTO{
			Name:     item.Name,
			Protocol: item.Protocol,
			Host:     item.Host,
			Port:     item.Port,
			Path:     item.Path,
			Weight:   item.Weight,
		})
	}

	return result
}

// PutKV 处理单 key 写入。
func (h *RegistryAPI) PutKV(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPut) {
		return
	}
	if !h.ensureWritable(w) {
		return
	}

	key, err := pathKey(r.URL.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	value, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := h.propose(r.Context(), storage.Command{
		Operation: storage.OperationPut,
		Key:       []byte(key),
		Value:     value,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "propose_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, SuccessResponse{Code: "ok", Message: "kv written"})
}

// Get 处理单 key 读取。
func (h *RegistryAPI) Get(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}

	key, err := pathKey(r.URL.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.requestTimout)
	defer cancel()

	value, err := h.node.Get(ctx, []byte(key))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read_failed", err.Error())
		return
	}
	if len(value) == 0 {
		writeError(w, http.StatusNotFound, "not_found", "key not found")
		return
	}

	writeJSON(w, http.StatusOK, SuccessResponse{
		Code: "ok",
		Data: KVResponseDTO{Key: key, Value: value},
	})
}

// DeleteKV 处理单 key 删除。
func (h *RegistryAPI) DeleteKV(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodDelete) {
		return
	}
	if !h.ensureWritable(w) {
		return
	}

	key, err := pathKey(r.URL.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := h.propose(r.Context(), storage.Command{
		Operation: storage.OperationDelete,
		Key:       []byte(key),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "propose_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, SuccessResponse{Code: "ok", Message: "kv deleted"})
}

// List 处理前缀扫描。
func (h *RegistryAPI) List(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}

	prefix := r.URL.Query().Get("prefix")
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	start, end := prefixRange(prefix)
	ctx, cancel := context.WithTimeout(r.Context(), h.requestTimout)
	defer cancel()

	items, err := h.node.Scan(ctx, start, end, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "scan_failed", err.Error())
		return
	}

	result := make([]KVResponseDTO, 0, len(items))
	for _, item := range items {
		result = append(result, KVResponseDTO{Key: string(item.Key), Value: append([]byte(nil), item.Value...)})
	}

	writeJSON(w, http.StatusOK, SuccessResponse{Code: "ok", Data: result})
}

// DeletePrefix 处理按前缀删除。
func (h *RegistryAPI) DeletePrefix(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodDelete) {
		return
	}
	if !h.ensureWritable(w) {
		return
	}

	prefix := r.URL.Query().Get("prefix")
	if prefix == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "prefix is required")
		return
	}
	if err := h.propose(r.Context(), storage.Command{
		Operation: storage.OperationDeletePrefix,
		Prefix:    []byte(prefix),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "propose_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, SuccessResponse{Code: "ok", Message: "prefix deleted"})
}

// Healthz 返回进程存活状态。
func (h *HealthAPI) Healthz(w http.ResponseWriter, r *http.Request) {
	status := h.node.Status()
	writeJSON(w, http.StatusOK, SuccessResponse{
		Code: "ok",
		Data: HealthResponseDTO{
			Status:      "ok",
			LeaderID:    status.LeaderID,
			LocalNodeID: status.NodeID,
			LeaderAddr:  h.leaderHTTPAddr(status.LeaderID),
		},
	})
}

// Readyz 返回服务就绪状态。
func (h *HealthAPI) Readyz(w http.ResponseWriter, r *http.Request) {
	status := h.node.Status()
	if !status.Started || status.Stopped || (status.LeaderID == 0 && status.Role != raftnode.RoleLeader) {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "raft node is not ready")
		return
	}

	writeJSON(w, http.StatusOK, SuccessResponse{
		Code: "ok",
		Data: HealthResponseDTO{
			Status:      "ready",
			LeaderID:    status.LeaderID,
			LocalNodeID: status.NodeID,
			LeaderAddr:  h.leaderHTTPAddr(status.LeaderID),
		},
	})
}

// Status 返回当前节点视角下的集群状态。
func (h *ControlAPI) Status(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}

	status := h.node.Status()
	hardState := h.node.HardState()
	confState := h.node.ConfState()

	writeJSON(w, http.StatusOK, SuccessResponse{
		Code: "ok",
		Data: ClusterStatusDTO{
			ClusterID:      h.clusterID,
			LocalNodeID:    status.NodeID,
			LeaderID:       status.LeaderID,
			Role:           string(status.Role),
			Term:           hardState.Term,
			Vote:           hardState.Vote,
			CommitIndex:    status.CommitIndex,
			AppliedIndex:   status.AppliedIndex,
			Started:        status.Started,
			Stopped:        status.Stopped,
			Voters:         append([]uint64(nil), confState.Voters...),
			VotersOutgoing: append([]uint64(nil), confState.VotersOutgoing...),
			Learners:       append([]uint64(nil), confState.Learners...),
			LearnersNext:   append([]uint64(nil), confState.LearnersNext...),
			AutoLeave:      confState.AutoLeave,
			HTTPAddrs:      h.book.SnapshotHTTP(),
			GRPCAddrs:      h.book.SnapshotGRPC(),
			AdminAddrs:     h.book.SnapshotAdmin(),
		},
	})
}

// AddLearner 触发一次 learner 加入。
func (h *ControlAPI) AddLearner(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	if !h.ensureLeader(w) {
		return
	}

	var request MemberChangeRequestDTO
	if err := decodeJSONBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if request.NodeID == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "nodeId is required")
		return
	}

	if err := h.peerTransport.UpsertPeer(request.NodeID, request.HTTPAddr, request.GRPCAddr, request.AdminAddr); err != nil {
		writeError(w, http.StatusInternalServerError, "peer_update_failed", err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.requestTimout)
	defer cancel()
	if err := h.node.ApplyConfChange(ctx, raftnode.ConfChangeRequest{
		Type:    raftnode.ConfChangeAddLearner,
		NodeID:  request.NodeID,
		Context: mustMarshalJSON(request),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "conf_change_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, SuccessResponse{Code: "ok", Message: "learner add requested"})
}

// PromoteLearner 提升 learner 为 voter。
func (h *ControlAPI) PromoteLearner(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	if !h.ensureLeader(w) {
		return
	}

	var request MemberChangeRequestDTO
	if err := decodeJSONBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if request.NodeID == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "nodeId is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.requestTimout)
	defer cancel()
	if err := h.node.ApplyConfChange(ctx, raftnode.ConfChangeRequest{
		Type:    raftnode.ConfChangePromoteLearner,
		NodeID:  request.NodeID,
		Context: mustMarshalJSON(request),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "conf_change_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, SuccessResponse{Code: "ok", Message: "learner promote requested"})
}

// RemoveMember 移除集群成员。
func (h *ControlAPI) RemoveMember(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	if !h.ensureLeader(w) {
		return
	}

	var request MemberChangeRequestDTO
	if err := decodeJSONBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if request.NodeID == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "nodeId is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.requestTimout)
	defer cancel()
	if err := h.node.ApplyConfChange(ctx, raftnode.ConfChangeRequest{
		Type:    raftnode.ConfChangeRemoveNode,
		NodeID:  request.NodeID,
		Context: mustMarshalJSON(request),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "conf_change_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, SuccessResponse{Code: "ok", Message: "member remove requested"})
}

// TransferLeader 主动把 leader 转移到目标节点。
func (h *ControlAPI) TransferLeader(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	if !h.ensureLeader(w) {
		return
	}

	var request LeaderTransferRequestDTO
	if err := decodeJSONBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if request.TargetNodeID == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "targetNodeId is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.requestTimout)
	defer cancel()
	if err := h.node.TransferLeadership(ctx, request.TargetNodeID); err != nil {
		writeError(w, http.StatusInternalServerError, "transfer_leader_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, SuccessResponse{Code: "ok", Message: "leader transfer requested"})
}

func (h *RegistryAPI) propose(parent context.Context, cmd storage.Command) error {
	ctx, cancel := context.WithTimeout(parent, h.requestTimout)
	defer cancel()
	return h.node.ProposeCommand(ctx, cmd)
}

func (h *RegistryAPI) ensureWritable(w http.ResponseWriter) bool {
	status := h.node.Status()
	if status.Role == raftnode.RoleLeader && status.LeaderID == status.NodeID {
		return true
	}

	writeNotLeader(w, status.LeaderID, h.leaderHTTPAddr(status.LeaderID))
	return false
}

func (h *RegistryAPI) leaderHTTPAddr(leaderID uint64) string {
	if leaderID == 0 {
		return ""
	}
	if leaderID == h.node.Status().NodeID {
		return h.httpAddr
	}

	return h.book.HTTPAddr(leaderID)
}

func (h *HealthAPI) leaderHTTPAddr(leaderID uint64) string {
	if leaderID == 0 {
		return ""
	}
	if leaderID == h.node.Status().NodeID {
		return h.httpAddr
	}

	return h.book.HTTPAddr(leaderID)
}

func (h *ControlAPI) ensureLeader(w http.ResponseWriter) bool {
	status := h.node.Status()
	if status.Role == raftnode.RoleLeader && status.LeaderID == status.NodeID {
		return true
	}

	writeNotLeader(w, status.LeaderID, h.leaderAdminAddr(status.LeaderID))
	return false
}

func (h *ControlAPI) leaderAdminAddr(leaderID uint64) string {
	if leaderID == 0 {
		return ""
	}
	if leaderID == h.node.Status().NodeID {
		return h.adminAddr
	}

	return h.book.AdminAddr(leaderID)
}

func allowMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}

	w.Header().Set("Allow", method)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

func decodeJSONBody(r *http.Request, target interface{}) error {
	defer r.Body.Close()

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, ErrorResponse{Code: code, Message: message})
}

func writeNotLeader(w http.ResponseWriter, leaderID uint64, leaderAddr string) {
	writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{
		Code:       "not_leader",
		Message:    fmt.Sprintf("current node is not leader, leaderId=%d leaderAddr=%s", leaderID, leaderAddr),
		LeaderID:   leaderID,
		LeaderAddr: leaderAddr,
	})
}

func pathKey(path string) (string, error) {
	key := strings.TrimPrefix(path, "/api/v1/kv/")
	if key == "" || key == path {
		return "", fmt.Errorf("kv key is required")
	}

	decoded, err := url.PathUnescape(key)
	if err != nil {
		return "", err
	}
	return decoded, nil
}

func parseLimit(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}

	limit, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	if limit < 0 {
		return 0, fmt.Errorf("limit must be greater than or equal to 0")
	}
	return limit, nil
}

func prefixRange(prefix string) ([]byte, []byte) {
	if prefix == "" {
		return nil, nil
	}

	start := []byte(prefix)
	end := append(append([]byte(nil), start...), 0xFF)
	return start, end
}

func mustMarshalJSON(value interface{}) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return data
}
