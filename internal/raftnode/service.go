package raftnode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/stellhub/stellmap/internal/snapshot"
	"github.com/stellhub/stellmap/internal/storage"
	"go.etcd.io/raft/v3"
	raftpb "go.etcd.io/raft/v3/raftpb"
)

const linearizableReadPollInterval = 10 * time.Millisecond

// Step 将一条内部 Raft 消息交给本地节点处理。
//
// 设计原因：
//   - 这是对底层 raft.Node.Step 的受控暴露，供内部 transport 层使用。
//   - 它没有放进最小 Node 接口里，因为它属于“内部复制面能力”，
//     不是上层业务和控制面最常使用的接口。
//
// 典型调用场景：
// - 内部 gRPC 收到其他节点发来的 Raft message 后，会调用它交给本地节点处理。
func (n *RaftNode) Step(ctx context.Context, message raftpb.Message) error {
	if err := n.ensureRunning(); err != nil {
		return err
	}

	return n.raftNode.Step(ctx, message)
}

// ProposeCommand 将状态机命令编码后提交给 Raft。
//
// 设计原因：
// - 对上层服务来说，直接传 []byte 不够友好；它们更希望提交结构化的状态机命令。
// - 因此这里提供一个“命令级”辅助方法，把 storage.Command 编码后再复用 Propose。
//
// 典型调用场景：
// - HTTP 数据面写请求会构造 storage.Command 并通过它进入共识日志。
func (n *RaftNode) ProposeCommand(ctx context.Context, cmd storage.Command) error {
	data, err := json.Marshal(cmd)
	if err != nil {
		return err
	}

	return n.Propose(ctx, data)
}

// LinearizableRead 执行一次线性一致读屏障，确保本地已追上 read index。
//
// 设计原因：
//   - ReadIndex 只负责拿到一个“可安全读取的索引”，并不保证本地状态机已经追平。
//   - 所以这里把“申请 read index + 轮询等待 AppliedIndex 追平”组合成一个高层方法，
//     便于 Get/Scan 等读路径直接复用。
//
// 典型调用场景：
// - 线性一致读的前置步骤
// - 对外 HTTP Get/List 在真正读 Pebble 前会先经过它
func (n *RaftNode) LinearizableRead(ctx context.Context, reqCtx []byte) error {
	readIndex, err := n.ReadIndex(ctx, reqCtx)
	if err != nil {
		return err
	}

	ticker := time.NewTicker(linearizableReadPollInterval)
	defer ticker.Stop()

	for {
		if n.Status().AppliedIndex >= readIndex {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-n.stopCh:
			return errNodeClosed
		case <-ticker.C:
		}
	}
}

// Get 在线性一致读屏障之后读取单个 key。
//
// 设计原因：
//   - 它把“线性一致读屏障 + 状态机单 key 查询”组合到一起，
//     让上层不需要重复拼读路径模板。
//
// 典型调用场景：
// - HTTP 单 key 查询
// - 心跳续约前读取当前实例值
func (n *RaftNode) Get(ctx context.Context, key []byte) ([]byte, error) {
	if err := n.LinearizableRead(ctx, key); err != nil {
		return nil, err
	}

	return n.kvStore.Get(ctx, key)
}

// Scan 在线性一致读屏障之后执行范围扫描。
//
// 设计原因：
//   - 它把“线性一致读屏障 + 范围扫描”组合在一起，
//     便于前缀查询或批量枚举场景直接复用。
//
// 典型调用场景：
// - HTTP List/按前缀扫描
// - 控制面后续如果要做成员或元数据列举，也可复用这一路径
func (n *RaftNode) Scan(ctx context.Context, start, end []byte, limit int) ([]storage.KV, error) {
	if err := n.LinearizableRead(ctx, append(append([]byte(nil), start...), end...)); err != nil {
		return nil, err
	}

	return n.kvStore.Scan(ctx, start, end, limit)
}

// HardState 返回当前持久化硬状态快照。
//
// 设计原因：
//   - 控制面状态查询经常需要知道当前 Term、Vote、Commit，
//     但不应该直接接触底层 storage 细节。
//   - 因此这里提供一个只读包装方法，方便外部安全获取持久化硬状态。
func (n *RaftNode) HardState() raftpb.HardState {
	hardState, _, err := n.storage.InitialState()
	if err != nil {
		return raftpb.HardState{}
	}

	return hardState
}

// ConfState 返回当前成员配置快照。
//
// 设计原因：
//   - 控制面需要知道当前 voters、learners、joint consensus 状态，
//     但这些信息本质上属于 Raft 运行和恢复状态的一部分。
//   - 这里统一通过只读方法暴露，避免外部直接操作 storage。
func (n *RaftNode) ConfState() raftpb.ConfState {
	_, confState, err := n.storage.InitialState()
	if err != nil {
		return raftpb.ConfState{}
	}

	return confState
}

// SetMemberAddress 持久化一个成员地址。
//
// 设计原因：
// - 成员地址属于控制面元数据，不应该只存在于内存地址簿中。
// - 通过这个方法把地址写入 Pebble 元数据区，可以支撑重启恢复和快照恢复。
//
// 典型调用场景：
// - 成员变更日志提交后，控制面会把该成员的 HTTP/gRPC/admin 地址持久化下来
// - 节点启动后也会把当前地址簿回写到持久化层
func (n *RaftNode) SetMemberAddress(ctx context.Context, nodeID uint64, httpAddr, grpcAddr, adminAddr string) error {
	return n.kvStore.SetMemberAddress(ctx, nodeID, httpAddr, grpcAddr, adminAddr)
}

// DeleteMemberAddress 删除一个成员地址。
//
// 设计原因：
//   - 当节点被移出集群后，其地址信息也应从持久化元数据中清除，
//     避免后续重启恢复出陈旧成员。
func (n *RaftNode) DeleteMemberAddress(ctx context.Context, nodeID uint64) error {
	return n.kvStore.DeleteMemberAddress(ctx, nodeID)
}

// ListMemberAddresses 返回当前已持久化的成员地址。
//
// 设计原因：
// - 节点启动后需要先恢复地址簿，控制面状态查询也需要把成员地址返回给外部。
// - 因此需要一个统一的只读入口来枚举已经落盘的成员地址元数据。
func (n *RaftNode) ListMemberAddresses(ctx context.Context) ([]storage.MemberAddress, error) {
	return n.kvStore.ListMemberAddresses(ctx)
}

// InstallSnapshot 安装一个通过外部 transport 传入的快照。
//
// 设计原因：
//   - 这是“内部快照传输层”和“本地 Raft 节点恢复逻辑”之间的桥接方法。
//   - transport 层拿到的是快照元信息和字节流，而真正安装快照需要同时更新：
//     snapshot.Store、Pebble 状态机、raft.MemoryStorage、WAL 截断和节点状态。
//   - 这些步骤必须统一收口，否则容易破坏恢复顺序。
//
// 典型调用场景：
// - 内部 gRPC 收到其他节点发送的快照分片并组装完成后，会调用它执行本地安装。
func (n *RaftNode) InstallSnapshot(ctx context.Context, meta snapshot.Metadata, data []byte) error {
	if err := n.ensureRunning(); err != nil {
		return err
	}

	if err := n.snapStore.Install(ctx, meta, bytes.NewReader(data)); err != nil {
		return err
	}
	if len(data) > 0 {
		if err := n.kvStore.Restore(ctx, bytes.NewReader(data)); err != nil {
			return err
		}
	}

	snap := raftpb.Snapshot{
		Data: data,
		Metadata: raftpb.SnapshotMetadata{
			Term:      meta.Term,
			Index:     meta.Index,
			ConfState: unmarshalConfState(meta.ConfState),
		},
	}
	if err := n.storage.ApplySnapshot(snap); err != nil && !errors.Is(err, raft.ErrSnapOutOfDate) {
		return err
	}
	if err := n.kvStore.SetAppliedIndex(ctx, meta.Index); err != nil {
		return err
	}
	if err := n.kvStore.SetAppliedTerm(ctx, meta.Term); err != nil {
		return err
	}
	if err := n.walStore.TruncatePrefix(ctx, meta.Index); err != nil {
		return err
	}

	n.mu.Lock()
	if n.status.AppliedIndex < meta.Index {
		n.status.AppliedIndex = meta.Index
	}
	if n.status.CommitIndex < meta.Index {
		n.status.CommitIndex = meta.Index
	}
	n.mu.Unlock()

	return nil
}
