package httptransport

import "github.com/chenwenlong-java/StarMap/internal/registry"

// ErrorResponse 定义对外统一错误响应。
type ErrorResponse struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	RequestID  string `json:"requestId,omitempty"`
	LeaderID   uint64 `json:"leaderId,omitempty"`
	LeaderAddr string `json:"leaderAddr,omitempty"`
}

// SuccessResponse 定义对外统一成功响应。
type SuccessResponse struct {
	Code      string      `json:"code"`
	Message   string      `json:"message,omitempty"`
	Data      interface{} `json:"data,omitempty"`
	RequestID string      `json:"requestId,omitempty"`
}

// EndpointDTO 表示实例对外暴露的一个协议端点。
//
// 一个实例可以同时暴露多个端点，例如：
// - `http`
// - `grpc`
// - `metrics`
//
// 这样注册中心在实例维度之上，还能明确表达每种协议的访问入口。
type EndpointDTO = registry.Endpoint

// RegisterRequestDTO 表示注册实例请求体。
type RegisterRequestDTO = registry.RegisterRequest

// DeregisterRequestDTO 表示注销实例请求体。
type DeregisterRequestDTO = registry.DeregisterRequest

// HeartbeatRequestDTO 表示续约请求体。
type HeartbeatRequestDTO = registry.HeartbeatRequest

// RegistryInstanceDTO 表示对外返回的实例候选项。
type RegistryInstanceDTO struct {
	Namespace         string            `json:"namespace"`
	Service           string            `json:"service"`
	InstanceID        string            `json:"instanceId"`
	Zone              string            `json:"zone,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
	Endpoints         []EndpointDTO     `json:"endpoints,omitempty"`
	LeaseTTLSeconds   int64             `json:"leaseTtlSeconds"`
	RegisteredAtUnix  int64             `json:"registeredAtUnix"`
	LastHeartbeatUnix int64             `json:"lastHeartbeatUnix"`
}

// KVResponseDTO 表示外部 KV 返回模型。
type KVResponseDTO struct {
	Key            string `json:"key"`
	Value          []byte `json:"value"`
	ModRevision    int64  `json:"modRevision"`
	CreateRevision int64  `json:"createRevision"`
	Version        int64  `json:"version"`
}

// HealthResponseDTO 表示健康检查返回。
type HealthResponseDTO struct {
	Status      string `json:"status"`
	LeaderID    uint64 `json:"leaderId"`
	LocalNodeID uint64 `json:"localNodeId"`
	LeaderAddr  string `json:"leaderAddr,omitempty"`
}

// MemberChangeRequestDTO 表示成员变更请求。
type MemberChangeRequestDTO struct {
	NodeID    uint64 `json:"nodeId"`
	HTTPAddr  string `json:"httpAddr,omitempty"`
	GRPCAddr  string `json:"grpcAddr,omitempty"`
	AdminAddr string `json:"adminAddr,omitempty"`
}

// LeaderTransferRequestDTO 表示 Leader 转移请求。
type LeaderTransferRequestDTO struct {
	TargetNodeID uint64 `json:"targetNodeId"`
}

// ClusterStatusDTO 表示控制面状态查询结果。
type ClusterStatusDTO struct {
	ClusterID      uint64            `json:"clusterId"`
	LocalNodeID    uint64            `json:"localNodeId"`
	LeaderID       uint64            `json:"leaderId"`
	Role           string            `json:"role"`
	Term           uint64            `json:"term"`
	Vote           uint64            `json:"vote"`
	CommitIndex    uint64            `json:"commitIndex"`
	AppliedIndex   uint64            `json:"appliedIndex"`
	Started        bool              `json:"started"`
	Stopped        bool              `json:"stopped"`
	Voters         []uint64          `json:"voters,omitempty"`
	VotersOutgoing []uint64          `json:"votersOutgoing,omitempty"`
	Learners       []uint64          `json:"learners,omitempty"`
	LearnersNext   []uint64          `json:"learnersNext,omitempty"`
	AutoLeave      bool              `json:"autoLeave,omitempty"`
	HTTPAddrs      map[uint64]string `json:"httpAddrs,omitempty"`
	GRPCAddrs      map[uint64]string `json:"grpcAddrs,omitempty"`
	AdminAddrs     map[uint64]string `json:"adminAddrs,omitempty"`
}
