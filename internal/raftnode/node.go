package raftnode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stellhub/stellmap/internal/snapshot"
	"github.com/stellhub/stellmap/internal/storage"
	"github.com/stellhub/stellmap/internal/wal"
	"go.etcd.io/raft/v3"
	raftpb "go.etcd.io/raft/v3/raftpb"
)

var (
	errNodeClosed     = errors.New("raftnode: node closed")
	errNodeNotStarted = errors.New("raftnode: node not started")
	errNodeStarted    = errors.New("raftnode: node already started")
)

// Node 定义最小 Raft 节点能力集合。
//
// 设计上，它不是完整的注册中心服务接口，而是“共识内核接口”：
//  1. 只暴露生命周期、日志提案、线性一致读屏障、成员变更、Leader 转移、
//     Ready 事件输出和状态观测这些 Raft 最核心的能力。
//  2. 不把 HTTP、gRPC、Pebble 查询、快照安装等外围能力全部塞进来，
//     避免接口膨胀成一个大而全的服务对象。
//  3. 上层服务可以围绕它组装出数据面、控制面和内部复制面，但不需要反向污染
//     Raft 内核本身的抽象边界。
type Node interface {
	// Start 启动一个 Raft 节点。
	//
	// 含义：
	// - 打开 WAL、Pebble、Snapshot
	// - 执行崩溃恢复
	// - 创建底层 raft.Node
	// - 启动 tick/ready 后台事件循环
	//
	// 典型调用场景：
	// - 服务进程启动时由 cmd/stellmapd 调用
	// - 单元测试或集成测试里用于拉起节点
	//
	// 这样设计的原因：
	// - 节点启动不是单纯“开一个 goroutine”，而是恢复、持久化、状态机、
	//   后台循环的组合动作，必须收口成一个统一入口，避免上层分别控制这些细节。
	Start(ctx context.Context) error
	// Stop 优雅停止一个 Raft 节点。
	//
	// 含义：
	// - 停止 tick/ready 后台循环
	// - 关闭持久化资源
	// - 等待内部 goroutine 退出
	//
	// 典型调用场景：
	// - 服务进程退出时由 cmd/stellmapd 调用
	// - 测试中用于停机、重启和恢复验证
	//
	// 这样设计的原因：
	// - 停止节点不仅仅是停止心跳，还涉及资源释放和并发收敛，因此需要一个幂等、
	//   可等待完成的统一停止入口。
	Stop(ctx context.Context) error
	// Propose 提交一条待复制的日志命令。
	//
	// 含义：
	// - 把一段原始二进制数据作为普通 Raft 日志提案提交给当前节点
	// - 后续会经过复制、提交、应用到状态机
	//
	// 典型调用场景：
	// - 对外 HTTP 写请求最终会间接走到这里
	// - 测试里也会直接调用它写入命令
	//
	// 这样设计的原因：
	// - 接口只接受 []byte，而不是强绑定某种业务结构，
	//   这样 Raft 层只关心“复制日志”，不关心业务命令的编码格式。
	Propose(ctx context.Context, data []byte) error
	// ReadIndex 为一次线性一致读申请一个可安全读取的最小索引。
	//
	// 含义：
	// - 向底层 Raft 申请一个 read barrier
	// - 返回一个索引，表示本地状态机至少追到这个位置后，读取才是线性一致的
	//
	// 典型调用场景：
	// - 不直接对 HTTP 暴露，通常由上层的 LinearizableRead/Get/Scan 间接调用
	//
	// 这样设计的原因：
	// - 一致性读天然分成两步：先拿 read index，再等 AppliedIndex 追平。
	//   把屏障能力单独抽出来，比把读存储逻辑和共识屏障耦合在一起更清晰。
	ReadIndex(ctx context.Context, reqCtx []byte) (uint64, error)
	// TransferLeadership 请求把 Leader 主动转移给目标节点。
	//
	// 含义：
	// - 向底层 Raft 发起一次 Leader 转移动作
	//
	// 典型调用场景：
	// - 控制面运维操作，例如 stellmapctl leader transfer
	//
	// 这样设计的原因：
	// - Leader 转移是一个明确的运维动作，不应该通过直接修改状态实现，
	//   也不应该和成员变更混在一起，因此单独暴露为接口方法。
	TransferLeadership(ctx context.Context, targetNodeID uint64) error
	// ApplyConfChange 提交一次成员变更请求。
	//
	// 含义：
	// - 把 add learner、promote learner、remove node 这类成员变更写入 Raft 日志
	// - 最终通过共识提交后生效
	//
	// 典型调用场景：
	// - 控制面运维操作，例如 stellmapctl member add-learner / promote / remove
	//
	// 这样设计的原因：
	// - 成员变更本身就是共识数据，不能直接改内存成员列表，
	//   必须走 Raft 提交路径，所以单独暴露一个成员变更入口。
	ApplyConfChange(ctx context.Context, change ConfChangeRequest) error
	// Ready 返回一条只读通道，用于消费节点已经整理好的 ready 批次。
	//
	// 含义：
	// - Ready 中包含待发送消息、已提交日志、快照元信息等内容
	// - 当前实现里，本地持久化和状态机应用已经在节点内部处理过，
	//   外部主要消费它来做内部 transport 转发和控制面联动
	//
	// 典型调用场景：
	// - cmd/stellmapd 会持续读取这个通道，把内部复制消息转发给其他节点
	//
	// 这样设计的原因：
	// - 不直接把底层 raft.Node.Ready() 裸暴露给外部，
	//   而是先在节点内部完成本地持久化，再向外输出更高层的事件批次，
	//   这样 transport 层职责更单一。
	Ready() <-chan Ready
	// Status 返回当前节点的只读状态快照。
	//
	// 含义：
	// - 提供节点 ID、LeaderID、角色、AppliedIndex、CommitIndex、启动/停止状态等信息
	//
	// 典型调用场景：
	// - 健康检查
	// - 控制面状态查询
	// - 测试里等待选主、等待 apply 进度
	//
	// 这样设计的原因：
	// - 上层经常需要观测节点状态，但不应该直接碰内部锁或底层 raft.Node.Status()，
	//   用一个稳定、收敛过的状态结构更适合服务层和测试层使用。
	Status() Status
}

// Status 描述节点当前可观察状态。
//
// 设计原因：
//  1. 它是对外暴露的“状态快照”，用于把内部运行状态以稳定结构提供给上层。
//  2. 它刻意只保留最常用、最稳定的观测字段，而不是直接暴露底层 raft.Node.Status()，
//     这样更适合健康检查、控制面查询、日志打印和测试轮询。
//  3. 它只描述“当前时刻节点看到了什么”，不承担配置下发或控制逻辑。
//
// 使用位置：
// - HTTP 健康检查和控制面状态查询会返回它的关键信息。
// - 测试中会用它判断是否已经选主、是否已经推进到某个 AppliedIndex。
type Status struct {
	// NodeID 是当前节点自己的 Raft 节点 ID。
	NodeID uint64
	// ClusterID 是当前节点所属集群的逻辑 ID。
	ClusterID uint64
	// LeaderID 是当前节点视角下已知的 Leader 节点 ID。
	//
	// 说明：
	// - 为 0 通常表示当前还没有稳定 Leader
	// - 不同节点在短时间内可能观察到不同值，直到共识状态稳定
	LeaderID uint64
	// Role 表示当前节点所处的 Raft 角色，例如 follower、candidate、leader。
	Role Role
	// AppliedIndex 是当前节点状态机已经应用完成的最后一条日志索引。
	//
	// 用途：
	// - 线性一致读需要等待 AppliedIndex 追到指定 read index
	// - 测试中常用它判断状态机是否已经追平
	AppliedIndex uint64
	// CommitIndex 是当前节点已经提交但未必已经应用到状态机的最后一条日志索引。
	//
	// 说明：
	// - CommitIndex >= AppliedIndex 通常成立
	// - 两者之间的差值表示“已提交但尚未应用”的处理窗口
	CommitIndex uint64
	// Started 表示节点是否已经执行过 Start 并成功进入运行状态。
	Started bool
	// Stopped 表示节点是否已经执行过 Stop 并进入停止状态。
	Stopped bool
}

// RaftNode 是 Node 的最小骨架实现。
//
// 设计原因：
//  1. 它不是对 etcd-io/raft 的简单透传包装，而是把“Raft 共识内核”和当前项目需要的
//     持久化、恢复、状态机应用、Ready 事件输出、生命周期管理收敛到一个对象里。
//  2. 上层服务不需要分别理解 WAL、Snapshot、Pebble、raft.Node 之间的调用顺序，
//     只需要通过 RaftNode 这个聚合对象启动、停止、提案、读屏障和观察状态即可。
//  3. 这样设计后，Raft 相关的关键不变量都能收口在一个地方维护，例如：
//     先恢复快照，再回放 WAL；先持久化 Ready，再推进状态机；先停止后台循环，再关闭资源。
//
// 使用位置：
//   - cmd/stellmapd 会直接持有 *RaftNode，负责启动节点、消费 Ready、提供控制面和数据面服务。
//   - internal/raftnode/service.go 会在它之上补充更贴近服务层的能力，例如 ProposeCommand、
//     LinearizableRead、Get、Scan、InstallSnapshot 等。
//   - 测试里会直接构造和控制它，用于验证恢复、快照、日志压缩和多节点运行行为。
type RaftNode struct {
	// cfg 是当前节点的启动配置快照。
	//
	// 用途：
	// - 在 Start 阶段用于构造底层 raft.Config
	// - 在 run 阶段用于驱动 TickInterval
	// - 在快照和压缩逻辑中用于读取 SnapshotEntries、CompactRetainEntries 等参数
	cfg Config

	// mu 保护 status 等需要一致性读取的运行时状态。
	//
	// 用途：
	// - Status() 对外返回状态快照时会读取它
	// - handleReady()/applyCommittedEntries()/updateStatus() 更新状态时会写入它
	mu sync.RWMutex
	// status 是当前节点对外暴露的可观察状态快照。
	//
	// 用途：
	// - 健康检查和控制面状态查询
	// - 测试里等待选主、等待 AppliedIndex 推进
	status Status
	// raftNode 是底层 etcd-io/raft 的节点实例。
	//
	// 用途：
	// - 承担真正的心跳、选举、日志复制、成员变更和 Leader 转移
	// - Propose/ReadIndex/TransferLeadership/ApplyConfChange 最终都会委托给它
	raftNode raft.Node
	// storage 是传给 etcd-io/raft 的内存存储实现。
	//
	// 设计说明：
	// - 它不是最终持久化介质，而是运行时 storage。
	// - 持久化由 WAL、Snapshot、Pebble 负责；storage 用来让 raft.Node 能快速访问当前日志与快照状态。
	//
	// 用途：
	// - Start 时恢复快照和 WAL 到内存
	// - handleReady 时 append/compact/apply snapshot
	storage *raft.MemoryStorage
	// walStore 是 Raft Log 的持久化存储。
	//
	// 用途：
	// - 启动时回放已持久化日志
	// - handleReady 时持久化 HardState 和 Entries
	// - 快照压缩后截断历史前缀日志
	walStore wal.WAL
	// snapStore 是独立文件快照存储。
	//
	// 用途：
	// - 启动时优先恢复最近快照
	// - 运行中创建、安装和清理快照文件
	snapStore snapshot.Store
	// kvStore 是状态机数据的持久化存储，当前实现基于 Pebble。
	//
	// 用途：
	// - 已提交普通日志最终会应用到这里
	// - 线性一致读最终从这里读取数据
	// - 快照导出和恢复也基于它完成
	kvStore *storage.PebbleStore
	// dataDir 是当前节点解析后的工作数据目录。
	//
	// 用途：
	// - 作为 wal/snapshot/pebble 的根目录
	// - 方便测试和服务进程定位当前节点的数据位置
	dataDir string
	// readyCh 是对外暴露的 Ready 事件通道。
	//
	// 用途：
	// - 节点内部完成本地持久化后，把高层 Ready 事件发到这里
	// - cmd/stellmapd 会持续消费它，把待发送消息转发给其他节点
	readyCh chan Ready
	// stopCh 是内部停止信号通道。
	//
	// 用途：
	// - 让后台 run 循环、ReadIndex 等待流程尽快感知节点已停止
	stopCh chan struct{}
	// doneCh 表示后台循环已经完全退出。
	//
	// 用途：
	// - Stop() 会等待它关闭，从而保证节点已经真正停干净
	doneCh chan struct{}
	// cancel 是 Start 内部创建的运行上下文取消函数。
	//
	// 用途：
	// - Stop() 时触发后台 runCtx 取消
	cancel context.CancelFunc
	// waitersMu 保护 waiters 映射表。
	//
	// 用途：
	// - ReadIndex 注册等待者
	// - handleReadStates 收到结果后唤醒等待者
	waitersMu sync.Mutex
	// waiters 保存一次 ReadIndex 请求上下文到返回通道的映射关系。
	//
	// 用途：
	// - 支撑线性一致读场景下的“请求上下文 -> read index”匹配
	waiters map[string]chan uint64
	// freshNode 表示当前节点是否应按“全新节点”路径启动。
	//
	// 用途：
	// - true 时调用 raft.StartNode
	// - false 时调用 raft.RestartNode
	// - 在 bootstrapStorage 恢复到历史状态后会被置为 false
	freshNode bool

	// readIndexCounter 用于生成默认的 ReadIndex 请求上下文序号。
	//
	// 用途：
	// - 调用方没有显式提供 reqCtx 时，自动生成唯一占位值，避免不同读请求冲突
	readIndexCounter atomic.Uint64
	// started 表示节点是否已经启动。
	//
	// 用途：
	// - 防止重复 Start
	// - Propose/ReadIndex/TransferLeadership/ApplyConfChange 前会检查它
	started atomic.Bool
	// stopped 表示节点是否已经停止。
	//
	// 用途：
	// - 防止重复 Stop
	// - 让外部请求和内部等待逻辑尽快感知节点关闭
	stopped atomic.Bool
}

// New 使用给定配置创建节点实例。
func New(cfg Config) (*RaftNode, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	dataDir, err := resolveDataDir(cfg)
	if err != nil {
		return nil, err
	}

	node := &RaftNode{
		cfg:       cfg,
		storage:   raft.NewMemoryStorage(),
		walStore:  wal.NewFileWAL(filepath.Join(dataDir, "wal")),
		snapStore: snapshot.NewFileStore(filepath.Join(dataDir, "snapshot")),
		kvStore:   storage.NewPebbleStore(filepath.Join(dataDir, "pebble")),
		dataDir:   dataDir,
		readyCh:   make(chan Ready, cfg.ReadyBufferSize),
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
		waiters:   make(map[string]chan uint64),
		freshNode: true,
		status: Status{
			NodeID:       cfg.NodeID,
			ClusterID:    cfg.ClusterID,
			LeaderID:     cfg.InitialLeaderID,
			Role:         RoleFollower,
			AppliedIndex: cfg.InitialAppliedIndex,
			CommitIndex:  cfg.InitialAppliedIndex,
		},
	}

	return node, nil
}

// Start 启动节点后台事件循环。
func (n *RaftNode) Start(ctx context.Context) error {
	if !n.started.CompareAndSwap(false, true) {
		return errNodeStarted
	}

	runCtx, cancel := context.WithCancel(context.Background())
	n.cancel = cancel
	if err := n.walStore.Open(runCtx); err != nil {
		return err
	}
	if err := n.kvStore.Open(runCtx); err != nil {
		return err
	}
	if err := n.bootstrapStorage(runCtx); err != nil {
		return err
	}
	if n.freshNode {
		n.raftNode = raft.StartNode(n.buildRaftConfig(), n.buildPeers())
	} else {
		n.raftNode = raft.RestartNode(n.buildRaftConfig())
	}

	go n.run(runCtx)
	go func() {
		select {
		case <-ctx.Done():
			_ = n.Stop(context.Background())
		case <-n.doneCh:
		}
	}()

	if n.freshNode {
		if err := n.raftNode.Campaign(runCtx); err != nil {
			return err
		}
	}

	return nil
}

// Stop 停止节点后台循环。
func (n *RaftNode) Stop(ctx context.Context) error {
	if !n.started.Load() {
		return errNodeNotStarted
	}
	if !n.stopped.CompareAndSwap(false, true) {
		return nil
	}

	close(n.stopCh)
	if n.cancel != nil {
		n.cancel()
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-n.doneCh:
		return nil
	}
}

// Propose 提交一条待复制命令到提案队列。
func (n *RaftNode) Propose(ctx context.Context, data []byte) error {
	if err := n.ensureRunning(); err != nil {
		return err
	}

	return n.raftNode.Propose(ctx, append([]byte(nil), data...))
}

// ReadIndex 为线性一致读申请一个最小占位索引。
func (n *RaftNode) ReadIndex(ctx context.Context, reqCtx []byte) (uint64, error) {
	if err := n.ensureRunning(); err != nil {
		return 0, err
	}

	requestCtx := append([]byte(nil), reqCtx...)
	if len(requestCtx) == 0 {
		requestCtx = []byte(fmt.Sprintf("read-index-%d", n.readIndexCounter.Add(1)))
	}
	waiter := make(chan uint64, 1)

	n.waitersMu.Lock()
	n.waiters[string(requestCtx)] = waiter
	n.waitersMu.Unlock()

	if err := n.raftNode.ReadIndex(ctx, requestCtx); err != nil {
		n.removeWaiter(requestCtx)
		return 0, err
	}

	select {
	case <-ctx.Done():
		n.removeWaiter(requestCtx)
		return 0, ctx.Err()
	case <-n.stopCh:
		n.removeWaiter(requestCtx)
		return 0, errNodeClosed
	case index := <-waiter:
		return index, nil
	}
}

// TransferLeadership 执行最小占位的 leader 转移。
func (n *RaftNode) TransferLeadership(ctx context.Context, targetNodeID uint64) error {
	if err := n.ensureRunning(); err != nil {
		return err
	}
	if targetNodeID == 0 {
		return errors.New("raftnode: target node id must be greater than 0")
	}

	n.raftNode.TransferLeadership(ctx, n.cfg.NodeID, targetNodeID)

	return nil
}

// ApplyConfChange 提交一条成员变更请求。
func (n *RaftNode) ApplyConfChange(ctx context.Context, change ConfChangeRequest) error {
	if err := n.ensureRunning(); err != nil {
		return err
	}
	if change.Type == "" {
		return errors.New("raftnode: conf change type is required")
	}
	if change.NodeID == 0 {
		return errors.New("raftnode: conf change node id must be greater than 0")
	}
	if change.CreatedAt.IsZero() {
		change.CreatedAt = time.Now()
	}

	return n.raftNode.ProposeConfChange(ctx, n.toConfChange(change))
}

// Ready 返回 ready 批次通道。
func (n *RaftNode) Ready() <-chan Ready {
	return n.readyCh
}

// Status 返回当前节点状态快照。
func (n *RaftNode) Status() Status {
	n.mu.RLock()
	defer n.mu.RUnlock()

	status := n.status
	status.Started = n.started.Load()
	status.Stopped = n.stopped.Load()

	return status
}

// run 是节点启动后的主事件循环。
//
// 设计原因：
// 1. etcd-io/raft 本身只是状态机库，不会自己开线程推进时间，也不会自己处理 Ready。
// 2. 因此需要一个明确的主循环统一负责：
//   - 按 TickInterval 推进逻辑时钟
//   - 持续消费底层 raft.Node.Ready()
//   - 在停止或上下文取消时有序退出
//
// 3. 这样可以把 Raft 时间推进、事件处理和生命周期控制集中到一个地方维护。
//
// 调用时机：
// - Start() 成功创建底层 raft.Node 后，会启动一个 goroutine 运行它。
//
// 处理流程：
// 1. 周期性调用 raftNode.Tick()，驱动心跳和选举超时前进
// 2. 消费 raftNode.Ready() 并交给 handleReady 处理
// 3. 在 stopCh 或 ctx.Done() 触发时退出，最后关闭 doneCh/readyCh 并停止底层 raft.Node
func (n *RaftNode) run(ctx context.Context) {
	ticker := time.NewTicker(n.cfg.TickInterval)
	defer ticker.Stop()
	defer close(n.doneCh)
	defer close(n.readyCh)
	defer n.closeStores()
	defer n.raftNode.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-n.stopCh:
			return
		case <-ticker.C:
			n.raftNode.Tick()
		case rd := <-n.raftNode.Ready():
			n.handleReady(rd)
		}
	}
}

func (n *RaftNode) closeStores() {
	_ = n.kvStore.Close(context.Background())
	_ = n.walStore.Close(context.Background())
}

func (n *RaftNode) ensureRunning() error {
	if !n.started.Load() {
		return errNodeNotStarted
	}
	if n.stopped.Load() {
		return errNodeClosed
	}

	return nil
}

// handleReady 处理底层 etcd-io/raft 产生的一批 Ready 数据。
//
// 设计原因：
// 1. Ready 是 Raft 当前批次“需要处理的工作集合”，里面同时包含：
//   - 快照
//   - HardState
//   - 新日志
//   - 已提交日志
//   - ReadState
//   - 待发送消息
//
// 2. 这些内容必须按严格顺序处理，否则可能破坏恢复语义或一致性：
//   - 先安装快照
//   - 再持久化 HardState/Entries
//   - 再更新运行时 storage
//   - 再应用 committed entries 到状态机
//   - 最后触发快照压缩、向外发出高层 Ready、调用 Advance
//
// 3. 把这套顺序收口在一个方法里，可以避免上层错误介入本地持久化流程。
//
// 调用时机：
// - run() 从 raftNode.Ready() 读到底层 Ready 后立即调用。
func (n *RaftNode) handleReady(rd raft.Ready) {
	if !raft.IsEmptySnap(rd.Snapshot) {
		if len(rd.Snapshot.Data) > 0 {
			_ = n.kvStore.Restore(context.Background(), bytes.NewReader(rd.Snapshot.Data))
		}
		_ = n.snapStore.Install(context.Background(), snapshot.Metadata{
			Term:      rd.Snapshot.Metadata.Term,
			Index:     rd.Snapshot.Metadata.Index,
			ConfState: mustMarshalConfState(rd.Snapshot.Metadata.ConfState),
		}, bytes.NewReader(rd.Snapshot.Data))
		_ = n.storage.ApplySnapshot(rd.Snapshot)
	}
	if !raft.IsEmptyHardState(rd.HardState) || len(rd.Entries) > 0 {
		_ = n.walStore.Append(context.Background(), rd.HardState, rd.Entries)
	}
	if len(rd.Entries) > 0 {
		_ = n.storage.Append(rd.Entries)
	}
	if !raft.IsEmptyHardState(rd.HardState) {
		n.storage.SetHardState(rd.HardState)
	}
	n.handleReadStates(rd.ReadStates)
	n.applyCommittedEntries(rd.CommittedEntries)
	n.updateStatus(rd)
	_ = n.maybeTriggerSnapshot(context.Background())
	n.emitReady(convertReady(rd))
	n.raftNode.Advance()
}

func (n *RaftNode) emitReady(ready Ready) {
	select {
	case <-n.stopCh:
		return
	case n.readyCh <- ready:
	default:
	}
}

func (n *RaftNode) handleReadStates(states []raft.ReadState) {
	n.waitersMu.Lock()
	defer n.waitersMu.Unlock()

	for _, state := range states {
		if ch, ok := n.waiters[string(state.RequestCtx)]; ok {
			ch <- state.Index
			close(ch)
			delete(n.waiters, string(state.RequestCtx))
		}
	}
}

// applyCommittedEntries 把已经提交的日志条目应用到本地状态机。
//
// 设计原因：
// 1. “日志已提交”和“状态机已应用”不是一回事，中间必须有一个明确的 apply 步骤。
// 2. 这里统一负责处理不同类型的 committed entry：
//   - 成员变更日志：调用底层 ApplyConfChange 让配置正式生效
//   - 普通业务日志：反序列化为状态机命令并应用到 Pebble
//     3. 应用完成后，再统一更新 AppliedIndex/AppliedTerm 和对外 status，
//     这样线性一致读和恢复逻辑都有稳定依据。
//
// 调用时机：
// - handleReady() 在本地持久化当前批次后调用。
func (n *RaftNode) applyCommittedEntries(entries []raftpb.Entry) {
	if len(entries) == 0 {
		return
	}

	var appliedIndex uint64
	var appliedTerm uint64
	for _, entry := range entries {
		if entry.Type == raftpb.EntryConfChange {
			var cc raftpb.ConfChange
			if err := cc.Unmarshal(entry.Data); err == nil {
				_ = n.raftNode.ApplyConfChange(cc)
			}
			appliedIndex = entry.Index
			appliedTerm = entry.Term
			continue
		}
		if entry.Type == raftpb.EntryConfChangeV2 {
			var cc raftpb.ConfChangeV2
			if err := cc.Unmarshal(entry.Data); err == nil {
				_ = n.raftNode.ApplyConfChange(cc)
			}
			appliedIndex = entry.Index
			appliedTerm = entry.Term
			continue
		}
		if entry.Type == raftpb.EntryNormal && len(entry.Data) > 0 {
			var cmd storage.Command
			if err := json.Unmarshal(entry.Data, &cmd); err == nil {
				_, _ = n.kvStore.Apply(context.Background(), cmd)
			}
		}
		appliedIndex = entry.Index
		appliedTerm = entry.Term
	}

	if appliedIndex > 0 {
		_ = n.kvStore.SetAppliedIndex(context.Background(), appliedIndex)
		_ = n.kvStore.SetAppliedTerm(context.Background(), appliedTerm)
	}

	n.mu.Lock()
	n.status.AppliedIndex = appliedIndex
	n.mu.Unlock()
}

func (n *RaftNode) updateStatus(rd raft.Ready) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if rd.SoftState != nil {
		n.status.LeaderID = rd.SoftState.Lead
		n.status.Role = toRole(rd.SoftState.RaftState)
	}
	if !raft.IsEmptyHardState(rd.HardState) {
		n.status.CommitIndex = rd.HardState.Commit
	}
	if len(rd.CommittedEntries) > 0 {
		n.status.AppliedIndex = rd.CommittedEntries[len(rd.CommittedEntries)-1].Index
	}
}

func (n *RaftNode) removeWaiter(reqCtx []byte) {
	n.waitersMu.Lock()
	defer n.waitersMu.Unlock()

	if ch, ok := n.waiters[string(reqCtx)]; ok {
		close(ch)
		delete(n.waiters, string(reqCtx))
	}
}

func (n *RaftNode) buildRaftConfig() *raft.Config {
	return &raft.Config{
		ID:              n.cfg.NodeID,
		ElectionTick:    n.cfg.ElectionTick,
		HeartbeatTick:   n.cfg.HeartbeatTick,
		Storage:         n.storage,
		MaxSizePerMsg:   n.cfg.MaxSizePerMsg,
		MaxInflightMsgs: n.cfg.MaxInflightMsgs,
	}
}

func (n *RaftNode) buildPeers() []raft.Peer {
	peers := make([]raft.Peer, 0, len(n.cfg.Peers))
	for _, peer := range n.cfg.Peers {
		peers = append(peers, raft.Peer{ID: peer.ID})
	}

	return peers
}

func (n *RaftNode) toConfChange(change ConfChangeRequest) raftpb.ConfChangeI {
	switch change.Type {
	case ConfChangeAddLearner:
		return raftpb.ConfChangeV2{
			Changes: []raftpb.ConfChangeSingle{{
				Type:   raftpb.ConfChangeAddLearnerNode,
				NodeID: change.NodeID,
			}},
			Context: change.Context,
		}
	case ConfChangePromoteLearner:
		return raftpb.ConfChangeV2{
			Changes: []raftpb.ConfChangeSingle{{
				Type:   raftpb.ConfChangeAddNode,
				NodeID: change.NodeID,
			}},
			Context: change.Context,
		}
	default:
		return raftpb.ConfChangeV2{
			Changes: []raftpb.ConfChangeSingle{{
				Type:   raftpb.ConfChangeRemoveNode,
				NodeID: change.NodeID,
			}},
			Context: change.Context,
		}
	}
}

func toRole(state raft.StateType) Role {
	switch state {
	case raft.StateLeader:
		return RoleLeader
	case raft.StateCandidate:
		return RoleCandidate
	case raft.StateFollower:
		return RoleFollower
	case raft.StatePreCandidate:
		return RoleCandidate
	default:
		return RoleFollower
	}
}

func convertReady(rd raft.Ready) Ready {
	ready := Ready{
		HardState: HardState{
			Term:   rd.HardState.Term,
			Vote:   rd.HardState.Vote,
			Commit: rd.HardState.Commit,
		},
		Entries:          convertEntries(rd.Entries),
		CommittedEntries: convertEntries(rd.CommittedEntries),
		Messages:         convertMessages(rd.Messages),
		MustSync:         rd.MustSync,
	}
	if rd.SoftState != nil {
		ready.SoftState = SoftState{
			LeaderID: rd.SoftState.Lead,
			Role:     toRole(rd.SoftState.RaftState),
		}
	}
	if !raft.IsEmptySnap(rd.Snapshot) {
		ready.Snapshot = &SnapshotMetadata{
			Term:      rd.Snapshot.Metadata.Term,
			Index:     rd.Snapshot.Metadata.Index,
			ConfState: mustMarshalConfState(rd.Snapshot.Metadata.ConfState),
		}
	}

	return ready
}

func convertEntries(entries []raftpb.Entry) []LogEntry {
	result := make([]LogEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, LogEntry{
			Index: entry.Index,
			Term:  entry.Term,
			Type:  entry.Type.String(),
			Data:  append([]byte(nil), entry.Data...),
			Raw:   entry,
		})
	}

	return result
}

func convertMessages(messages []raftpb.Message) []OutboundMessage {
	result := make([]OutboundMessage, 0, len(messages))
	for _, message := range messages {
		payload, _ := message.Marshal()
		result = append(result, OutboundMessage{
			To:      message.To,
			From:    message.From,
			Type:    message.Type.String(),
			Payload: payload,
			Raw:     message,
		})
	}

	return result
}

func mustMarshalConfState(confState raftpb.ConfState) []byte {
	data, err := confState.Marshal()
	if err != nil {
		return nil
	}

	return data
}

func resolveDataDir(cfg Config) (string, error) {
	if cfg.DataDir != "" {
		if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
			return "", err
		}
		return cfg.DataDir, nil
	}

	return os.MkdirTemp("", fmt.Sprintf("stellmap-node-%d-*", cfg.NodeID))
}

// bootstrapStorage 负责在节点启动时恢复本地存储状态。
//
// 设计原因：
// 1. 节点重启后不能直接把 raft.Node 当成全新节点启动，必须先恢复已有状态。
// 2. 恢复顺序必须固定为：
//   - 先恢复最新 snapshot
//   - 再回放 WAL 中 snapshot 之后的日志
//   - 再把已提交但尚未反映到 Pebble 的日志重新应用到状态机
//
// 3. 只有这样，raft.MemoryStorage、Pebble、WAL 三者才能在启动后保持一致。
//
// 调用时机：
// - Start() 中，在创建 raft.StartNode/RestartNode 之前调用。
//
// 处理内容：
// - 尝试打开并恢复最新快照
// - 从 WAL 加载 HardState 和 Entries
// - 把状态恢复到内存 storage
// - 必要时把已提交日志重新应用到 Pebble
// - 判断当前节点应走 freshNode 还是 restartNode 路径
func (n *RaftNode) bootstrapStorage(ctx context.Context) error {
	var snapshotIndex uint64
	meta, reader, err := n.snapStore.OpenLatest(ctx)
	if err == nil && reader != nil {
		defer reader.Close()

		data, readErr := io.ReadAll(reader)
		if readErr != nil {
			return readErr
		}
		if meta.Index > 0 {
			if restoreErr := n.kvStore.Restore(ctx, bytes.NewReader(data)); restoreErr != nil {
				return restoreErr
			}
			_ = n.kvStore.SetAppliedIndex(ctx, meta.Index)
			_ = n.kvStore.SetAppliedTerm(ctx, meta.Term)
			if err := n.storage.ApplySnapshot(raftpb.Snapshot{
				Data: data,
				Metadata: raftpb.SnapshotMetadata{
					Index:     meta.Index,
					Term:      meta.Term,
					ConfState: unmarshalConfState(meta.ConfState),
				},
			}); err != nil && !errors.Is(err, raft.ErrSnapOutOfDate) {
				return err
			}
			snapshotIndex = meta.Index
		}
	}

	hardState, entries, err := n.walStore.Load(ctx)
	if err != nil {
		return err
	}

	if !raft.IsEmptyHardState(hardState) {
		n.storage.SetHardState(hardState)
	}
	if len(entries) > 0 {
		if err := n.storage.Append(entries); err != nil {
			return err
		}
	}
	if !raft.IsEmptyHardState(hardState) || len(entries) > 0 || snapshotIndex > 0 {
		n.freshNode = false
	}
	if !raft.IsEmptyHardState(hardState) && hardState.Commit > snapshotIndex {
		for _, entry := range entries {
			if entry.Index <= snapshotIndex || entry.Index > hardState.Commit {
				continue
			}
			if entry.Type == raftpb.EntryNormal && len(entry.Data) > 0 {
				var cmd storage.Command
				if err := json.Unmarshal(entry.Data, &cmd); err == nil {
					_, _ = n.kvStore.Apply(ctx, cmd)
				}
			}
			_ = n.kvStore.SetAppliedIndex(ctx, entry.Index)
			_ = n.kvStore.SetAppliedTerm(ctx, entry.Term)
		}
	}
	n.status.AppliedIndex = hardState.Commit
	n.status.CommitIndex = hardState.Commit

	return nil
}

// maybeTriggerSnapshot 根据当前已应用进度决定是否生成快照并压缩日志。
//
// 设计原因：
//  1. Raft 日志会持续增长，如果不定期快照和压缩，恢复时间和磁盘占用都会越来越差。
//  2. 但快照也有成本，因此不应该每应用一条日志就生成，而是按阈值触发。
//  3. 这里统一负责把“状态机快照生成、snapshot.Store 落盘、内存 storage 压缩、
//     WAL 前缀截断、旧快照清理”串成一个完整闭环。
//
// 调用时机：
// - handleReady() 在应用完 committed entries 之后调用。
//
// 触发条件：
// - 当前 AppliedIndex 与上一次快照索引的差值达到 SnapshotEntries 阈值。
//
// 处理流程：
// 1. 从 Pebble 导出状态机快照
// 2. 在 raft.MemoryStorage 中创建快照
// 3. 将快照写入 snapshot.Store
// 4. 按 CompactRetainEntries 计算压缩点
// 5. 压缩内存 storage 并截断 WAL 历史前缀
// 6. 按 KeepSnapshots 清理过旧快照
func (n *RaftNode) maybeTriggerSnapshot(ctx context.Context) error {
	appliedIndex, err := n.kvStore.AppliedIndex(ctx)
	if err != nil {
		return err
	}
	if appliedIndex == 0 {
		return nil
	}

	currentSnapshot, err := n.storage.Snapshot()
	if err != nil && !errors.Is(err, raft.ErrSnapshotTemporarilyUnavailable) {
		return err
	}
	lastSnapshotIndex := currentSnapshot.Metadata.Index
	if appliedIndex-lastSnapshotIndex < n.cfg.SnapshotEntries {
		return nil
	}

	var buf bytes.Buffer
	if err := n.kvStore.Snapshot(ctx, &buf); err != nil {
		return err
	}

	confState := n.currentConfState()
	snap, err := n.storage.CreateSnapshot(appliedIndex, &confState, buf.Bytes())
	if err != nil {
		if errors.Is(err, raft.ErrSnapOutOfDate) {
			return nil
		}
		return err
	}

	_, err = n.snapStore.Create(ctx, snapshot.Metadata{
		Term:      snap.Metadata.Term,
		Index:     snap.Metadata.Index,
		ConfState: mustMarshalConfState(snap.Metadata.ConfState),
		Checksum:  "",
		FileSize:  uint64(buf.Len()),
	}, func(w io.Writer) error {
		_, copyErr := io.Copy(w, bytes.NewReader(buf.Bytes()))
		return copyErr
	})
	if err != nil {
		return err
	}

	compactIndex := appliedIndex
	if n.cfg.CompactRetainEntries > 0 && appliedIndex > n.cfg.CompactRetainEntries {
		compactIndex = appliedIndex - n.cfg.CompactRetainEntries
	}

	firstIndex, firstErr := n.storage.FirstIndex()
	if firstErr != nil {
		return firstErr
	}
	if compactIndex >= firstIndex {
		if err := n.storage.Compact(compactIndex); err != nil && !errors.Is(err, raft.ErrCompacted) {
			return err
		}
		if err := n.walStore.TruncatePrefix(ctx, compactIndex); err != nil {
			return err
		}
	}

	return n.snapStore.Cleanup(ctx, n.cfg.KeepSnapshots)
}

func (n *RaftNode) currentConfState() raftpb.ConfState {
	if n.raftNode != nil {
		status := n.raftNode.Status()
		return raftpb.ConfState{
			Voters:         status.Config.Voters[0].Slice(),
			VotersOutgoing: status.Config.Voters[1].Slice(),
			Learners:       toSortedIDs(status.Config.Learners),
			LearnersNext:   toSortedIDs(status.Config.LearnersNext),
			AutoLeave:      status.Config.AutoLeave,
		}
	}

	voters := make([]uint64, 0, len(n.cfg.Peers))
	for _, peer := range n.cfg.Peers {
		voters = append(voters, peer.ID)
	}

	return raftpb.ConfState{Voters: voters}
}

func unmarshalConfState(data []byte) raftpb.ConfState {
	if len(data) == 0 {
		return raftpb.ConfState{}
	}
	var state raftpb.ConfState
	if err := state.Unmarshal(data); err != nil {
		return raftpb.ConfState{}
	}

	return state
}

func toSortedIDs(items map[uint64]struct{}) []uint64 {
	if len(items) == 0 {
		return nil
	}
	ids := make([]uint64, 0, len(items))
	for id := range items {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}
