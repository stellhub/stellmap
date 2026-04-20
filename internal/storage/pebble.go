package storage

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
)

var (
	errStoreNotOpen = errors.New("storage: pebble store not open")
)

const (
	metaAppliedIndexKey = "__meta__/applied_index"
	metaAppliedTermKey  = "__meta__/applied_term"
	metaMembersPrefix   = "__meta__/members/"
)

type snapshotPayload struct {
	Data    []KV            `json:"data"`
	Members []MemberAddress `json:"members,omitempty"`
}

// PebbleStore 是基于 Pebble 的真实状态机实现。
//
// 它在整个系统里的职责是：
// 1. 作为 Raft 已提交日志的状态机承载层，存放纯 KV 数据。
// 2. 持久化少量状态机元数据，例如 applied index / applied term。
// 3. 持久化动态成员地址，保证节点重启或 snapshot 恢复后地址簿不丢失。
// 4. 提供快照导出与恢复能力，让 raftnode 能把状态机和 snapshot / WAL 串成闭环。
//
// 之所以选 Pebble，是因为它本质上就是高性能嵌入式 KV 引擎，很适合作为单进程 CP
// 注册中心的状态机后端。Raft Log 不放在这里，而是交给 WAL，避免日志和状态机职责混淆。
type PebbleStore struct {
	// mu 保护 db 指针的打开与关闭，避免并发生命周期操作导致野指针或重复关闭。
	mu sync.RWMutex
	// path 是 Pebble 数据目录。
	// 所有状态机 KV 和元数据键都会保存在这个目录下。
	path string
	// db 是底层 Pebble 实例。
	// 真正的读写、批处理、迭代器和快照恢复都通过它执行。
	db *pebble.DB
}

// NewPebbleStore 创建真实 Pebble 存储实现。
//
// 如果调用方没有显式传 path，这里会生成一个临时目录，主要用于测试和局部调试。
// 生产环境通常会传入稳定的数据目录。
func NewPebbleStore(path string) *PebbleStore {
	if path == "" {
		path = filepath.Join(os.TempDir(), "stellmap-pebble-"+time.Now().Format("20060102150405.000000000"))
	}

	return &PebbleStore{path: path}
}

// Open 打开存储。
//
// 这是状态机生命周期的起点，通常由 raftnode.Start 在 bootstrap 阶段调用。
// 打开之后，Apply / Get / Scan / Snapshot / Restore 等操作才能工作。
func (s *PebbleStore) Open(ctx context.Context) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db != nil {
		return nil
	}
	if err := os.MkdirAll(s.path, 0o755); err != nil {
		return err
	}

	db, err := pebble.Open(s.path, &pebble.Options{})
	if err != nil {
		return err
	}
	s.db = db

	return nil
}

// Close 关闭存储。
//
// 它通常在节点优雅退出时被调用，负责关闭底层 Pebble 句柄并释放文件资源。
func (s *PebbleStore) Close(ctx context.Context) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil

	return err
}

// StateMachine 返回自身作为状态机实现。
//
// 这个薄封装主要是为了让外部可以把 PebbleStore 作为 StateMachine 注入，而不暴露更多细节。
func (s *PebbleStore) StateMachine() StateMachine {
	return s
}

// Apply 应用一条状态机命令。
//
// 这是 Raft 日志真正“落到业务状态”的地方，通常由 raftnode.applyCommittedEntries 调用。
// 当前支持三类纯 KV 操作：
// 1. put：写入或覆盖单 key。
// 2. delete：删除单 key。
// 3. delete_prefix：按前缀批量删除。
//
// 这里使用 Pebble batch，是为了把单次状态机命令中的所有修改作为一个原子批处理提交。
// applied index / applied term 不在这里更新，而是由 raftnode 在日志 apply 成功后统一写入，
// 避免状态机层偷偷推进 Raft 语义索引。
func (s *PebbleStore) Apply(ctx context.Context, cmd Command) (Result, error) {
	_ = ctx
	db, err := s.getDB()
	if err != nil {
		return Result{}, err
	}

	batch := db.NewBatch()
	defer batch.Close()

	result := Result{}
	switch cmd.Operation {
	case OperationPut:
		if err := batch.Set(cmd.Key, cmd.Value, pebble.Sync); err != nil {
			return Result{}, err
		}
		result.Modified = 1
	case OperationDelete:
		if err := batch.Delete(cmd.Key, pebble.Sync); err != nil {
			return Result{}, err
		}
		result.Modified = 1
	case OperationDeletePrefix:
		end := append([]byte(nil), cmd.Prefix...)
		end = append(end, 0xFF)
		if err := batch.DeleteRange(cmd.Prefix, end, pebble.Sync); err != nil {
			return Result{}, err
		}
		result.Modified = 1
	default:
		return Result{}, nil
	}

	if err := batch.Commit(pebble.Sync); err != nil {
		return Result{}, err
	}

	return result, nil
}

// Get 查询单 key。
//
// 它通常被对外 HTTP 查询路径使用。对于线性一致读，上层会先走 ReadIndex，
// 再调用这里读取本地 Pebble 状态。
func (s *PebbleStore) Get(ctx context.Context, key []byte) ([]byte, error) {
	_ = ctx
	db, err := s.getDB()
	if err != nil {
		return nil, err
	}

	value, closer, err := db.Get(key)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	defer closer.Close()

	return append([]byte(nil), value...), nil
}

// Scan 按键空间扫描。
//
// 这个方法服务于前缀查询和范围扫描。它会显式跳过 __meta__/ 前缀下的内部元数据键，
// 避免把 applied index、成员地址等内部状态暴露给业务数据面。
func (s *PebbleStore) Scan(ctx context.Context, start, end []byte, limit int) ([]KV, error) {
	_ = ctx
	db, err := s.getDB()
	if err != nil {
		return nil, err
	}

	iter, err := db.NewIter(&pebble.IterOptions{
		LowerBound: start,
		UpperBound: end,
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	items := make([]KV, 0)
	for iter.First(); iter.Valid(); iter.Next() {
		key := append([]byte(nil), iter.Key()...)
		if isMetaKey(key) {
			continue
		}
		items = append(items, KV{
			Key:   key,
			Value: append([]byte(nil), iter.Value()...),
		})
		if limit > 0 && len(items) >= limit {
			break
		}
	}

	return items, iter.Error()
}

// Snapshot 导出状态机快照。
//
// 它会把当前业务 KV 和动态成员地址一起编码成快照 payload。
// 这样设计的原因是：成员地址属于节点恢复时必需的控制面元数据，
// 不能只保存在进程内存里，否则 snapshot restore 后地址簿会丢失。
func (s *PebbleStore) Snapshot(ctx context.Context, w io.Writer) error {
	_ = ctx
	items, err := s.Scan(ctx, nil, nil, 0)
	if err != nil {
		return err
	}
	members, err := s.ListMemberAddresses(ctx)
	if err != nil {
		return err
	}

	sort.Slice(items, func(i, j int) bool {
		return string(items[i].Key) < string(items[j].Key)
	})
	sort.Slice(members, func(i, j int) bool {
		return members[i].NodeID < members[j].NodeID
	})

	return json.NewEncoder(w).Encode(snapshotPayload{
		Data:    items,
		Members: members,
	})
}

// Restore 从快照恢复状态机。
//
// 它会先清掉现有业务数据和成员地址，再把快照中的内容整批写回 Pebble。
// 注意这里不会删除 applied index / applied term 这样的元数据键，
// 这些值由 raftnode 在安装快照和继续 apply 日志时单独维护。
func (s *PebbleStore) Restore(ctx context.Context, r io.Reader) error {
	_ = ctx
	db, err := s.getDB()
	if err != nil {
		return err
	}

	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	payload, err := decodeSnapshotPayload(raw)
	if err != nil {
		return err
	}

	batch := db.NewBatch()
	defer batch.Close()

	iter, err := db.NewIter(&pebble.IterOptions{})
	if err != nil {
		return err
	}
	for iter.First(); iter.Valid(); iter.Next() {
		key := append([]byte(nil), iter.Key()...)
		if isMetaKey(key) {
			continue
		}
		if err := batch.Delete(key, pebble.NoSync); err != nil {
			iter.Close()
			return err
		}
	}
	if err := iter.Close(); err != nil {
		return err
	}

	memberIter, err := db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(metaMembersPrefix),
		UpperBound: memberPrefixEnd(),
	})
	if err != nil {
		return err
	}
	for memberIter.First(); memberIter.Valid(); memberIter.Next() {
		key := append([]byte(nil), memberIter.Key()...)
		if err := batch.Delete(key, pebble.NoSync); err != nil {
			memberIter.Close()
			return err
		}
	}
	if err := memberIter.Close(); err != nil {
		return err
	}

	for _, item := range payload.Data {
		if err := batch.Set(item.Key, item.Value, pebble.NoSync); err != nil {
			return err
		}
	}
	for _, member := range payload.Members {
		if err := batch.Set(memberAddressKey(member.NodeID), mustJSON(member), pebble.NoSync); err != nil {
			return err
		}
	}

	return batch.Commit(pebble.Sync)
}

// AppliedIndex 返回状态机当前已应用的日志索引。
//
// 这个值不是 Pebble 自己推导出来的，而是由 raftnode 在 committed entry apply 成功后显式写入。
// 启动恢复和线性一致读等待都可能依赖它判断本地是否追上某个索引。
func (s *PebbleStore) AppliedIndex(ctx context.Context) (uint64, error) {
	_ = ctx
	db, err := s.getDB()
	if err != nil {
		return 0, err
	}

	value, closer, err := db.Get([]byte(metaAppliedIndexKey))
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	defer closer.Close()

	return decodeUint64(value), nil
}

// SetAppliedIndex 更新状态机已应用索引。
//
// 它通常在 raftnode.applyCommittedEntries 成功处理完一条日志后调用。
// 之所以单独持久化，是为了让节点重启后知道状态机已经推进到哪里。
func (s *PebbleStore) SetAppliedIndex(ctx context.Context, index uint64) error {
	_ = ctx
	db, err := s.getDB()
	if err != nil {
		return err
	}

	return db.Set([]byte(metaAppliedIndexKey), encodeUint64(index), pebble.Sync)
}

// SetAppliedTerm 更新状态机已应用任期。
//
// 它与 AppliedIndex 配套保存，主要用于恢复和观测状态，而不是直接参与业务查询。
func (s *PebbleStore) SetAppliedTerm(ctx context.Context, term uint64) error {
	_ = ctx
	db, err := s.getDB()
	if err != nil {
		return err
	}

	return db.Set([]byte(metaAppliedTermKey), encodeUint64(term), pebble.Sync)
}

// AppliedTerm 返回状态机当前已应用任期。
//
// 它通常用于恢复后的状态观测，帮助我们确认当前状态机对应的 Raft 进度。
func (s *PebbleStore) AppliedTerm(ctx context.Context) (uint64, error) {
	_ = ctx
	db, err := s.getDB()
	if err != nil {
		return 0, err
	}

	value, closer, err := db.Get([]byte(metaAppliedTermKey))
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	defer closer.Close()

	return decodeUint64(value), nil
}

// SetMemberAddress 持久化一个成员地址。
//
// 这一步通常发生在成员变更日志提交之后，或者节点启动时同步当前 address book 时。
// 成员地址被单独保存在 __meta__/members/ 前缀下，便于重启恢复和 snapshot 导出。
// 这里会一并保存公共 HTTP、内部 gRPC 和独立 admin 地址。
func (s *PebbleStore) SetMemberAddress(ctx context.Context, nodeID uint64, httpAddr, grpcAddr, adminAddr string) error {
	_ = ctx
	db, err := s.getDB()
	if err != nil {
		return err
	}
	if nodeID == 0 {
		return errors.New("storage: member node id must be greater than 0")
	}

	member := MemberAddress{
		NodeID:    nodeID,
		HTTPAddr:  httpAddr,
		GRPCAddr:  grpcAddr,
		AdminAddr: adminAddr,
	}
	return db.Set(memberAddressKey(nodeID), mustJSON(member), pebble.Sync)
}

// DeleteMemberAddress 删除一个成员地址。
//
// 当成员被移出集群后，上层会调用这个方法把对应的持久化地址一起清掉，
// 避免节点重启后又把已经删除的成员地址恢复回来。
func (s *PebbleStore) DeleteMemberAddress(ctx context.Context, nodeID uint64) error {
	_ = ctx
	db, err := s.getDB()
	if err != nil {
		return err
	}
	if nodeID == 0 {
		return errors.New("storage: member node id must be greater than 0")
	}

	return db.Delete(memberAddressKey(nodeID), pebble.Sync)
}

// ListMemberAddresses 返回当前已持久化的成员地址集合。
//
// stellmapd 启动时会用它恢复 address book；Snapshot 导出时也会用它把成员地址带进快照。
func (s *PebbleStore) ListMemberAddresses(ctx context.Context) ([]MemberAddress, error) {
	_ = ctx
	db, err := s.getDB()
	if err != nil {
		return nil, err
	}

	iter, err := db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(metaMembersPrefix),
		UpperBound: memberPrefixEnd(),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	items := make([]MemberAddress, 0)
	for iter.First(); iter.Valid(); iter.Next() {
		var member MemberAddress
		if err := json.Unmarshal(iter.Value(), &member); err != nil {
			return nil, err
		}
		items = append(items, member)
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].NodeID < items[j].NodeID
	})
	return items, nil
}

// getDB 返回当前已打开的 Pebble 实例。
//
// 这个辅助方法的目的，是把“未 Open 就使用存储”的错误统一收口成 errStoreNotOpen。
func (s *PebbleStore) getDB() (*pebble.DB, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, errStoreNotOpen
	}

	return s.db, nil
}

// isMetaKey 判断某个 key 是否属于内部元数据键空间。
//
// Scan 和 Restore 都会用它把业务 KV 与内部元数据区分开，避免两者相互污染。
func isMetaKey(key []byte) bool {
	return len(key) >= len("__meta__/") && string(key[:len("__meta__/")]) == "__meta__/"
}

// encodeUint64 以大端序编码 uint64。
//
// applied index / applied term 这类固定宽度元数据会使用这种紧凑编码持久化到 Pebble。
func encodeUint64(value uint64) []byte {
	data := make([]byte, 8)
	binary.BigEndian.PutUint64(data, value)
	return data
}

// decodeUint64 从大端字节中解码 uint64。
func decodeUint64(data []byte) uint64 {
	if len(data) < 8 {
		return 0
	}

	return binary.BigEndian.Uint64(data)
}

// decodeSnapshotPayload 兼容解码快照 payload。
//
// 当前快照格式优先使用包含 Data + Members 的结构；
// 同时保留对旧格式（仅 []KV）的兼容读取，避免已有快照文件在升级后无法恢复。
func decodeSnapshotPayload(data []byte) (snapshotPayload, error) {
	var payload snapshotPayload
	if err := json.Unmarshal(data, &payload); err == nil {
		return payload, nil
	}

	var items []KV
	if err := json.Unmarshal(data, &items); err != nil {
		return snapshotPayload{}, err
	}

	return snapshotPayload{Data: items}, nil
}

// memberAddressKey 生成成员地址在 Pebble 中的元数据 key。
func memberAddressKey(nodeID uint64) []byte {
	return []byte(metaMembersPrefix + strconv.FormatUint(nodeID, 10))
}

// memberPrefixEnd 生成成员地址元数据前缀扫描的上界。
func memberPrefixEnd() []byte {
	return []byte(metaMembersPrefix + "\xff")
}

// mustJSON 把值编码成 JSON。
//
// 这里是一个内部辅助函数，用于把成员地址快速序列化后写入 Pebble。
// 当前调用点传入的结构都是可序列化的，因此这里选择在异常时返回 nil，
// 由上层写入时报错暴露问题，而不再在每个调用点重复处理。
func mustJSON(value interface{}) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}

	return data
}
