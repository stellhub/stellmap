package raftnode

import (
	"errors"
	"time"
)

const (
	defaultTickInterval            = 100 * time.Millisecond
	defaultElectionTick            = 10
	defaultHeartbeatTick           = 1
	defaultReadyBufferSize         = 16
	defaultLinearizableReadTimeout = 3 * time.Second
	defaultMaxSizePerMsg           = 1024 * 1024
	defaultMaxInflightMsgs         = 256
	defaultSnapshotEntries         = 64
	defaultCompactRetainEntries    = 16
	defaultKeepSnapshots           = 2
)

// Peer 描述一个初始 Raft 成员。
type Peer struct {
	// ID 是集群内唯一的 Raft 节点 ID。
	ID uint64
}

// Config 描述 Raft 节点的最小启动配置。
type Config struct {
	// NodeID 是当前节点在 Raft 集群中的唯一标识。
	NodeID uint64
	// ClusterID 是当前节点所属集群的逻辑 ID，用于区分不同集群实例。
	ClusterID uint64
	// DataDir 是当前节点的数据目录，用于存放 WAL、Snapshot、Pebble 等持久化数据。
	DataDir string
	// Peers 是节点启动时已知的初始成员列表，单节点启动时默认只包含自己。
	Peers []Peer
	// TickInterval 是 Raft 内部逻辑时钟的推进间隔，每到一个 tick 会驱动选举和心跳状态机前进。
	TickInterval time.Duration
	// ElectionTick 是选举超时需要经过的 tick 数，必须大于 HeartbeatTick。
	// 因为 HeartbeatTick 表示的是Leader多久告诉follow我还活着，ElectionTick 是Leader多久没做健康检测就开始选举
	ElectionTick int
	// HeartbeatTick 是 Leader 发送心跳所使用的 tick 周期。
	HeartbeatTick int
	// ReadyBufferSize 是对外暴露的 Ready 通道缓冲区大小，用于承接待持久化、待发送、待应用的数据批次。
	ReadyBufferSize int
	// LinearizableReadTimeout 是线性一致读等待 ReadIndex 和 AppliedIndex 追平时使用的超时时间。
	LinearizableReadTimeout time.Duration
	// MaxSizePerMsg 是单条 Raft 网络消息允许携带的最大数据量。
	MaxSizePerMsg uint64
	// MaxInflightMsgs 是每个复制目标允许并发在途的消息数量上限，用于控制复制窗口。
	MaxInflightMsgs int
	// SnapshotEntries 是触发生成快照所需的最小已应用日志条数增量。
	SnapshotEntries uint64
	// CompactRetainEntries 是日志压缩时在快照之后仍然保留的日志条数。
	CompactRetainEntries uint64
	// KeepSnapshots 是本地磁盘上最多保留的快照文件数量。
	KeepSnapshots int
	// InitialAppliedIndex 是节点启动时的初始已应用索引，通常用于测试或特殊恢复场景。
	InitialAppliedIndex uint64
	// InitialLeaderID 是节点启动时预设的 Leader ID 占位值，真实运行后会被 Raft 状态覆盖。
	InitialLeaderID uint64
}

// Validate 校验配置是否合法。
//
// 设计原因：
//   - Raft 节点一旦启动，很多配置错误都会直接导致运行不稳定，
//     例如心跳/选举参数不合理、缓冲区为 0、快照阈值无效等。
//   - 因此需要在启动前尽早失败，而不是让服务带着错误配置运行。
//
// 校验范围：
// - 节点基础身份是否合法
// - 时间参数和缓冲区是否为正数
// - 选举超时是否严格大于心跳周期
// - 快照与持久化相关阈值是否有效
func (c Config) Validate() error {
	if c.NodeID == 0 {
		return errors.New("raftnode: node id must be greater than 0")
	}
	if c.ElectionTick <= c.HeartbeatTick {
		return errors.New("raftnode: election tick must be greater than heartbeat tick")
	}
	if c.TickInterval <= 0 {
		return errors.New("raftnode: tick interval must be greater than 0")
	}
	if c.ReadyBufferSize <= 0 {
		return errors.New("raftnode: ready buffer size must be greater than 0")
	}
	if c.LinearizableReadTimeout <= 0 {
		return errors.New("raftnode: linearizable read timeout must be greater than 0")
	}
	if c.MaxSizePerMsg == 0 {
		return errors.New("raftnode: max size per msg must be greater than 0")
	}
	if c.MaxInflightMsgs <= 0 {
		return errors.New("raftnode: max inflight msgs must be greater than 0")
	}
	if c.SnapshotEntries == 0 {
		return errors.New("raftnode: snapshot entries must be greater than 0")
	}
	if c.KeepSnapshots <= 0 {
		return errors.New("raftnode: keep snapshots must be greater than 0")
	}

	return nil
}

// WithDefaults 为缺失字段填充默认值。
//
// 设计原因：
//   - 上层在构造节点配置时，不需要每次都显式填完所有运行参数。
//   - 对于那些有明确工程默认值的字段，统一在这里补齐，
//     可以让调用方只关注真正需要覆盖的部分。
//
// 处理内容：
// - 为 tick、选举、心跳、read timeout、消息大小、快照阈值等参数填默认值
// - 当调用方没有显式提供 Peers 且已经设置了 NodeID 时，默认把自己作为单节点初始成员
//
// 调用时机：
// - New(cfg) 一开始就会先调用它，再进行 Validate 校验
func (c Config) WithDefaults() Config {
	if c.TickInterval <= 0 {
		c.TickInterval = defaultTickInterval
	}
	if c.ElectionTick <= 0 {
		c.ElectionTick = defaultElectionTick
	}
	if c.HeartbeatTick <= 0 {
		c.HeartbeatTick = defaultHeartbeatTick
	}
	if c.ReadyBufferSize <= 0 {
		c.ReadyBufferSize = defaultReadyBufferSize
	}
	if c.LinearizableReadTimeout <= 0 {
		c.LinearizableReadTimeout = defaultLinearizableReadTimeout
	}
	if c.MaxSizePerMsg == 0 {
		c.MaxSizePerMsg = defaultMaxSizePerMsg
	}
	if c.MaxInflightMsgs <= 0 {
		c.MaxInflightMsgs = defaultMaxInflightMsgs
	}
	if c.SnapshotEntries == 0 {
		c.SnapshotEntries = defaultSnapshotEntries
	}
	if c.CompactRetainEntries == 0 {
		c.CompactRetainEntries = defaultCompactRetainEntries
	}
	if c.KeepSnapshots <= 0 {
		c.KeepSnapshots = defaultKeepSnapshots
	}
	if len(c.Peers) == 0 && c.NodeID != 0 {
		c.Peers = []Peer{{ID: c.NodeID}}
	}

	return c
}
