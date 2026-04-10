package wal

import (
	"context"
	"sync"

	raftpb "go.etcd.io/raft/v3/raftpb"
)

// MemoryWAL 是内存版 WAL 实现。
//
// 它的定位不是生产实现，而是一个便于测试和局部验证的轻量替身：
// 1. 不触碰磁盘，适合单元测试或早期骨架联调。
// 2. 语义尽量对齐 FileWAL，方便在测试中替换。
// 3. 只模拟“追加 / 加载 / 截断”的结果，不模拟 segment 文件管理。
//
// 当测试只关心 RaftNode 的行为，而不关心真实文件恢复、segment 回收时，
// MemoryWAL 能让测试更快、更稳定。
type MemoryWAL struct {
	// mu 保护内存中的 HardState 和 entries，避免并发读写冲突。
	mu sync.RWMutex
	// state 保存最近一次追加的 HardState。
	// 它对应 FileWAL 中单独持久化的 hardstate.bin。
	state raftpb.HardState
	// entries 保存当前内存中的全部日志条目。
	// 它相当于把所有 segment 展平后的逻辑视图。
	entries []raftpb.Entry
	// opened 表示当前实例是否已经 Open。
	// 它主要用于和 FileWAL 的生命周期语义保持一致。
	opened bool
}

// NewMemoryWAL 创建内存版 WAL。
func NewMemoryWAL() *MemoryWAL {
	return &MemoryWAL{}
}

// Open 打开 WAL。
//
// 对 MemoryWAL 来说，这一步只是切换生命周期状态；
// 对调用方来说，它意味着可以开始执行 Append / Load / TruncatePrefix。
func (w *MemoryWAL) Open(ctx context.Context) error {
	_ = ctx
	w.mu.Lock()
	defer w.mu.Unlock()
	w.opened = true
	return nil
}

// Append 追加状态与日志。
//
// 这里复用了 mergeEntries 逻辑，因此在遇到重叠日志时也会按 Raft 语义覆盖旧 tail。
// 它通常在测试里由 raftnode.handleReady 间接调用。
func (w *MemoryWAL) Append(ctx context.Context, state raftpb.HardState, entries []raftpb.Entry) error {
	_ = ctx
	w.mu.Lock()
	defer w.mu.Unlock()
	w.state = state
	w.entries = mergeEntries(w.entries, entries)
	return nil
}

// Load 加载当前内容。
//
// 这一步通常出现在节点重启恢复时，调用方会先拿到 HardState，再拿到日志条目，
// 然后决定是以 fresh node 还是 restart node 的方式拉起 Raft。
func (w *MemoryWAL) Load(ctx context.Context) (raftpb.HardState, []raftpb.Entry, error) {
	_ = ctx
	w.mu.RLock()
	defer w.mu.RUnlock()
	items := make([]raftpb.Entry, len(w.entries))
	copy(items, w.entries)
	return w.state, items, nil
}

// TruncatePrefix 截断指定索引之前的日志。
//
// 这对应生产实现中的“日志压缩后回收旧日志”动作。
// MemoryWAL 只保留逻辑结果，不模拟 segment 文件删除。
func (w *MemoryWAL) TruncatePrefix(ctx context.Context, index uint64) error {
	_ = ctx
	w.mu.Lock()
	defer w.mu.Unlock()

	filtered := make([]raftpb.Entry, 0, len(w.entries))
	for _, entry := range w.entries {
		if entry.Index >= index {
			filtered = append(filtered, entry)
		}
	}
	w.entries = filtered

	return nil
}

// Sync 刷盘占位。
//
// 生产版 WAL 会把内存中的状态刷到磁盘；内存版没有真实 I/O，所以这里是空操作。
func (w *MemoryWAL) Sync(ctx context.Context) error {
	_ = ctx
	return nil
}

// Close 关闭 WAL。
//
// 它主要用于和 FileWAL 保持统一的资源生命周期语义，便于在测试里替换实现。
func (w *MemoryWAL) Close(ctx context.Context) error {
	_ = ctx
	w.mu.Lock()
	defer w.mu.Unlock()
	w.opened = false
	return nil
}
