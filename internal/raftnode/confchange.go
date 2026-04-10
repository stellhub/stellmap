package raftnode

import "time"

// ConfChangeType 描述成员变更类型。
//
// 设计原因：
//   - 它是控制面成员变更请求的上层语义枚举，用来表达“想做什么变更”，
//     而不是直接暴露底层 raftpb.ConfChangeType/ConfChangeV2 细节。
//   - 这样上层控制面可以用更稳定、更贴近业务语义的方式构造请求，
//     由 raftnode 内部再映射为真正的 Raft 成员变更协议。
type ConfChangeType string

const (
	// ConfChangeAddLearner 表示把一个新节点以 learner 身份加入集群。
	//
	// 典型场景：
	// - 新节点第一次加入集群时，先作为 learner 追日志，不立即参与投票。
	ConfChangeAddLearner ConfChangeType = "add_learner"
	// ConfChangePromoteLearner 表示把一个已经追平日志的 learner 提升为正式 voter。
	//
	// 典型场景：
	// - learner 已经追上大部分日志，可以开始参与投票和法定人数计算。
	ConfChangePromoteLearner ConfChangeType = "promote_learner"
	// ConfChangeRemoveNode 表示把一个节点从当前集群中移除。
	//
	// 典型场景：
	// - 节点下线、替换机器、缩容、剔除故障节点。
	ConfChangeRemoveNode ConfChangeType = "remove_node"
)

// ConfChangeRequest 表示一次成员变更请求。
//
// 设计原因：
//   - 这是控制面和 raftnode 之间的成员变更输入模型。
//   - 它不直接暴露底层 protobuf 结构，而是保留最核心的业务信息：
//     变更类型、目标节点、附加上下文和创建时间。
//
// 使用位置：
// - 控制面 HTTP handler 会构造它并调用 ApplyConfChange
// - raftnode 内部再把它映射为 raftpb.ConfChangeV2
type ConfChangeRequest struct {
	// Type 是本次成员变更的目标动作。
	Type ConfChangeType
	// NodeID 是本次成员变更作用的目标节点 ID。
	NodeID uint64
	// Context 是附带在成员变更日志里的原始上下文数据。
	//
	// 典型用途：
	// - 当前项目会在里面携带成员的 HTTP/gRPC 地址等控制面元数据，
	//   这样变更提交后，所有节点都能根据日志更新自己的地址簿。
	Context []byte
	// CreatedAt 是该成员变更请求的创建时间。
	//
	// 说明：
	// - 当前主要用于保留请求形成时刻，便于排查和未来扩展；
	//   底层 Raft 提交时不会直接依赖它做一致性判断。
	CreatedAt time.Time
}
