package raftnode

import raftpb "go.etcd.io/raft/v3/raftpb"

// Role 表示节点角色。
//
// 它是对底层 raft.StateType 的上层收敛表示，主要用于对外展示和状态判断。
type Role string

const (
	// RoleFollower 表示 follower 角色。
	RoleFollower Role = "follower"
	// RoleCandidate 表示 candidate 角色。
	RoleCandidate Role = "candidate"
	// RoleLeader 表示 leader 角色。
	RoleLeader Role = "leader"
	// RoleLearner 表示 learner 角色。
	RoleLearner Role = "learner"
)

// SoftState 描述不需要持久化的内存状态。
//
// 设计原因：
// - 这类状态会频繁变化，重启后也不要求恢复，因此不应该写入 WAL。
// - 它更适合做运行期观测，例如当前 Leader 是谁、当前节点是什么角色。
type SoftState struct {
	// LeaderID 是当前节点视角下已知的 Leader 节点 ID。
	LeaderID uint64
	// Role 是当前节点的运行角色。
	Role Role
}

// HardState 描述最小持久化状态。
//
// 设计原因：
// - 它是崩溃恢复必须保留的最小核心状态。
// - 一旦丢失，节点重启后就无法正确继续任期、投票和提交位置。
type HardState struct {
	// Term 是当前节点持久化保存的任期号。
	Term uint64
	// Vote 是当前任期内本节点已经投票给了哪个节点。
	Vote uint64
	// Commit 是当前已提交的最后一条日志索引。
	Commit uint64
}

// LogEntry 表示一条最小日志记录。
//
// 设计原因：
// 1. 它是对 raftpb.Entry 的上层包装，方便外部逻辑只读取最常用字段。
// 2. 同时保留 Raw，避免上层在需要底层完整结构时再次反向转换。
//
// 使用位置：
// - Ready.Entries / Ready.CommittedEntries 中会携带它
// - 控制面和 transport 层可以直接读取 Index、Term、Type
// - 需要完整 protobuf 结构时则使用 Raw
type LogEntry struct {
	// Index 是该日志条目的全局顺序索引。
	Index uint64
	// Term 是生成该日志条目时对应的任期。
	Term uint64
	// Type 是日志类型的字符串表示，例如 EntryNormal、EntryConfChangeV2。
	Type string
	// Data 是日志承载的原始业务数据或控制数据。
	//
	// 说明：
	// - 普通业务写入时通常是编码后的状态机命令
	// - 成员变更时通常是 ConfChange 数据
	Data []byte
	// Raw 是底层原始 raftpb.Entry，供需要完整字段的逻辑继续使用。
	Raw raftpb.Entry
}

// OutboundMessage 表示待发送的复制消息。
//
// 设计原因：
// 1. 它把内部复制层最关心的信息抽出来了：发给谁、从谁来、消息类型、序列化载荷。
// 2. 同时保留 Raw，便于 transport 层在特殊场景下直接使用原始 raftpb.Message。
//
// 使用位置：
// - Ready.Messages 会携带它
// - cmd/starmapd 会消费这些消息并通过内部 gRPC 发给其他节点
type OutboundMessage struct {
	// To 是目标节点 ID。
	To uint64
	// From 是源节点 ID。
	From uint64
	// Type 是消息类型的字符串表示，例如 MsgApp、MsgVote、MsgHeartbeat、MsgSnap。
	Type string
	// Payload 是已经序列化好的原始二进制消息体，便于 transport 直接转发。
	Payload []byte
	// Raw 是底层原始 raftpb.Message。
	Raw raftpb.Message
}

// SnapshotMetadata 描述快照元信息。
//
// 设计原因：
// - 快照本体可能很大，不适合每次都在高层事件里直接携带全部数据。
// - 大多数场景下，上层更关心的是这份快照“覆盖到哪、属于哪个任期、对应什么成员配置”。
//
// 使用位置：
// - Ready.Snapshot 会携带它，提示外部当前 ready 批次中包含快照事件
// - transport 和诊断逻辑可以据此判断是否需要发送或安装快照
type SnapshotMetadata struct {
	// Term 是生成该快照时对应的任期。
	Term uint64
	// Index 是该快照覆盖到的最后一条已应用日志索引。
	Index uint64
	// ConfState 是快照时刻成员配置的序列化结果。
	ConfState []byte
	// Checksum 是快照内容校验值。
	//
	// 当前本地 ready 事件里通常可能为空，真正的文件快照元数据由 snapshot.Store 管理。
	Checksum string
}

// Ready 表示一批待持久化、待应用、待发送的数据。
//
// 设计原因：
//  1. 底层 raft.Node.Ready() 给出的是一个“当前批次需要处理的工作集合”。
//  2. 当前项目没有把底层 Ready 裸暴露出去，而是先在节点内部完成本地持久化、
//     状态机应用、快照处理，再整理成更适合上层使用的 Ready 结构。
//  3. 这样 transport 层和控制面只需要关心“这一批还要往外发什么、外部能观察到什么”，
//     不必直接介入 WAL 和本地状态机的细节。
//
// 使用位置：
// - cmd/starmapd 会持续消费 Ready 通道
// - 主要使用其中的 Messages 做内部复制转发
// - 也会读取 CommittedEntries 做控制面地址簿同步等联动逻辑
type Ready struct {
	// SoftState 是当前批次对应的易失性状态变化。
	SoftState SoftState
	// HardState 是当前批次对应的持久化状态变化。
	HardState HardState
	// Entries 是当前批次新增的未提交日志条目。
	//
	// 说明：
	// - 它们已经在节点内部被持久化到 WAL，并追加到运行时 storage
	Entries []LogEntry
	// CommittedEntries 是当前批次已经提交并已在节点内部应用过的日志条目。
	//
	// 说明：
	// - 业务状态机命令已经应用到 Pebble
	// - 控制面可据此做成员地址同步等后续联动
	CommittedEntries []LogEntry
	// Messages 是当前批次仍需要发送给其他节点的复制消息。
	Messages []OutboundMessage
	// Snapshot 表示当前批次是否伴随一份快照事件。
	//
	// 为空表示本批次没有快照。
	Snapshot *SnapshotMetadata
	// MustSync 表示这一批处理在底层语义上是否要求强同步持久化。
	//
	// 当前实现内部已经自行处理持久化，它主要保留为上层观测信息。
	MustSync bool
}
