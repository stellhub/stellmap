package wal

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"

	raftpb "go.etcd.io/raft/v3/raftpb"
)

const (
	defaultMaxSegmentSize int64 = 64 * 1024 * 1024
	stateFileName               = "hardstate.bin"
)

var (
	errWALNotOpen = errors.New("wal: not open")
)

// WAL 定义 Raft Log 的最小持久化能力。
//
// 它位于 raftnode 与底层持久化介质之间，负责承接这几个核心动作：
// 1. Open / Load：节点启动时恢复 HardState 和历史日志。
// 2. Append：在 Ready 流程里把新的 HardState 和日志条目落盘。
// 3. TruncatePrefix：在生成快照并压缩日志后回收旧日志。
// 4. Sync / Close：处理刷盘和生命周期管理。
//
// 之所以把接口保持得很小，是因为 Raft 对 WAL 的核心要求本质上只有“按顺序持久化 +
// 能在崩溃后恢复”。更高层的 snapshot、状态机 apply、transport 都不应该耦合到这里。
type WAL interface {
	Open(ctx context.Context) error
	Append(ctx context.Context, state raftpb.HardState, entries []raftpb.Entry) error
	Load(ctx context.Context) (raftpb.HardState, []raftpb.Entry, error)
	TruncatePrefix(ctx context.Context, index uint64) error
	Sync(ctx context.Context) error
	Close(ctx context.Context) error
}

// FileWAL 是基于本地文件系统的 WAL 实现。
//
// 它采用“HardState 单独文件 + Entry 按 segment 切分”的布局：
// 1. hardstate.bin 保存最近一次必须持久化的 HardState。
// 2. 多个 *.wal segment 保存连续的日志条目。
//
// 这样设计的原因是：
// 1. HardState 更新频率和日志条目不同，单独存储更简单。
// 2. segment 文件便于增量追加、tail rewrite 和按段回收。
// 3. 节点恢复时只需要按顺序读取 hardstate.bin 和 segment 文件即可重建状态。
type FileWAL struct {
	// mu 保护 FileWAL 的内存索引和文件状态，避免 Open / Append / TruncatePrefix 并发冲突。
	mu sync.RWMutex
	// dir 是 WAL 目录。hardstate.bin 和所有 *.wal segment 都会落在这里。
	dir string
	// codec 负责 HardState 和 Entry 的编解码。
	// FileWAL 只关心记录写入和恢复，不直接感知 protobuf 细节。
	codec Codec
	// maxSegmentSize 控制单个 segment 文件的上限。
	// 达到阈值后会自动滚动到下一个 segment，避免单文件无限膨胀。
	maxSegmentSize int64
	// state 是当前已持久化的最新 HardState 的内存镜像。
	// 它会在 Append / Load 后被更新，并在 Sync / Open 时参与恢复。
	state raftpb.HardState
	// entries 是当前 WAL 中全部日志条目的内存镜像。
	// 它不是状态机数据，而是便于快速定位覆盖区间和恢复结果的逻辑视图。
	entries []raftpb.Entry
	// segments 保存每个 segment 文件对应的日志区间。
	// Append、rewriteTail、TruncatePrefix 都会依赖它定位受影响的磁盘文件。
	segments []Segment
	// opened 表示当前实例是否已经完成 Open。
	// 这能避免调用方在未恢复完成前执行追加或读取。
	opened bool
}

// NewFileWAL 创建文件版 WAL。
//
// 它只构造对象，不会立刻访问磁盘。真正的目录创建和内容恢复会在 Open 时完成。
func NewFileWAL(dir string) *FileWAL {
	return &FileWAL{
		dir:            dir,
		codec:          ProtobufCodec{},
		maxSegmentSize: defaultMaxSegmentSize,
	}
}

// Open 打开 WAL 并加载文件内容。
//
// 这是 FileWAL 的生命周期起点，通常由 raftnode.Start 在恢复阶段调用。
// 它会完成三件事：
// 1. 确保 WAL 目录存在。
// 2. 加载 hardstate.bin，恢复最近一次持久化的 HardState。
// 3. 顺序扫描所有 *.wal segment，恢复日志条目和 segment 元信息。
//
// 之所以集中在这里恢复，是为了让后续的 Append / Load / TruncatePrefix 都建立在一致的
// 内存视图之上，避免把“文件发现”和“日志操作”混杂在一起。
func (w *FileWAL) Open(ctx context.Context) error {
	_ = ctx

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.opened {
		return nil
	}
	if err := os.MkdirAll(w.dir, 0o755); err != nil {
		return err
	}

	state, err := w.loadState()
	if err != nil {
		return err
	}
	entries, segments, err := w.loadEntries()
	if err != nil {
		return err
	}

	w.state = state
	w.entries = entries
	w.segments = segments
	w.opened = true

	return nil
}

// Append 追加状态与日志。
//
// 这是 Ready 持久化链路里的关键一步，通常由 raftnode.handleReady 间接调用。
// 它的处理逻辑分三种情况：
// 1. 只有 HardState 变化：只更新 stateFile。
// 2. 新日志与当前 tail 连续：直接增量追加到最后一个 segment 或新建 segment。
// 3. 新日志和旧 tail 重叠：说明发生了覆盖，需要重写受影响的 tail。
//
// 这样设计的目的，是让正常路径始终走顺序追加，把重写成本限制在真正发生冲突的场景。
func (w *FileWAL) Append(ctx context.Context, state raftpb.HardState, entries []raftpb.Entry) error {
	_ = ctx

	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.opened {
		return errWALNotOpen
	}

	if !isEmptyHardState(state) {
		w.state = state
	}
	if err := w.persistState(); err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}

	if len(w.entries) == 0 {
		w.entries = make([]raftpb.Entry, 0, len(entries))
	}

	if len(w.entries) == 0 || entries[0].Index == w.entries[len(w.entries)-1].Index+1 {
		if err := w.appendEntriesIncremental(entries, nextSegmentID(w.segments), true); err != nil {
			return err
		}
		w.entries = append(w.entries, cloneEntries(entries)...)
		return nil
	}

	if entries[0].Index > w.entries[len(w.entries)-1].Index+1 {
		return fmt.Errorf("wal: gap detected, first incoming index=%d last existing index=%d", entries[0].Index, w.entries[len(w.entries)-1].Index)
	}

	merged := mergeEntries(w.entries, entries)
	return w.rewriteTail(entries[0].Index, merged)
}

// Load 加载当前 WAL 内容。
//
// 它主要在节点启动恢复时被调用，让上层一次性拿到当前 HardState 和全部日志。
// 返回的是内存镜像的拷贝，避免调用方误改 FileWAL 内部状态。
func (w *FileWAL) Load(ctx context.Context) (raftpb.HardState, []raftpb.Entry, error) {
	_ = ctx

	w.mu.RLock()
	defer w.mu.RUnlock()

	if !w.opened {
		return raftpb.HardState{}, nil, errWALNotOpen
	}

	entries := make([]raftpb.Entry, len(w.entries))
	copy(entries, w.entries)

	return w.state, entries, nil
}

// TruncatePrefix 截断指定索引之前的日志。
//
// 它通常在 snapshot 生成成功并完成状态机持久化后被调用，用于回收旧日志。
// 当前实现采用更细粒度的回收策略：
// 1. 完全落在截断点之前的 segment 直接删除。
// 2. 跨越截断点的边界 segment 只重写剩余部分。
// 3. 截断点之后、未受影响的 tail segment 原样保留。
//
// 这样能避免每次 compaction 都重写全部剩余日志，降低 I/O 成本。
func (w *FileWAL) TruncatePrefix(ctx context.Context, index uint64) error {
	_ = ctx

	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.opened {
		return errWALNotOpen
	}

	if len(w.entries) == 0 {
		return w.persistState()
	}
	if index <= w.entries[0].Index {
		return w.persistState()
	}

	filtered := make([]raftpb.Entry, 0, len(w.entries))
	for _, entry := range w.entries {
		if entry.Index >= index {
			filtered = append(filtered, entry)
		}
	}

	if len(filtered) == 0 {
		for _, segment := range w.segments {
			if err := os.Remove(segment.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		w.entries = nil
		w.segments = nil
		return w.persistState()
	}

	boundaryIdx := sort.Search(len(w.segments), func(i int) bool {
		return w.segments[i].LastIndex >= index
	})
	if boundaryIdx >= len(w.segments) {
		for _, segment := range w.segments {
			if err := os.Remove(segment.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		w.entries = nil
		w.segments = nil
		return w.persistState()
	}

	for _, segment := range w.segments[:boundaryIdx] {
		if err := os.Remove(segment.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	newSegments := append([]Segment(nil), w.segments[boundaryIdx:]...)
	boundarySegment := &newSegments[0]
	if boundarySegment.FirstIndex < index {
		boundaryEntries := make([]raftpb.Entry, 0)
		for _, entry := range filtered {
			if entry.Index < index {
				continue
			}
			if entry.Index > boundarySegment.LastIndex {
				break
			}
			boundaryEntries = append(boundaryEntries, entry)
		}
		if len(boundaryEntries) == 0 {
			return fmt.Errorf("wal: boundary segment %s has no entries after truncate index=%d", boundarySegment.Path, index)
		}
		if err := rewriteSegmentFile(boundarySegment.Path, boundaryEntries, w.codec); err != nil {
			return err
		}
		boundarySegment.FirstIndex = boundaryEntries[0].Index
		boundarySegment.LastIndex = boundaryEntries[len(boundaryEntries)-1].Index
	}

	w.entries = filtered
	w.segments = newSegments

	return w.persistState()
}

// Sync 将当前内存状态刷到磁盘。
//
// 当前 FileWAL 的追加路径本身已经在写记录时执行 fsync，因此这里主要确保 HardState
// 也被持久化。这个方法保留出来，是为了让调用方拥有一个显式的“刷盘点”。
func (w *FileWAL) Sync(ctx context.Context) error {
	_ = ctx

	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.opened {
		return errWALNotOpen
	}

	return w.persistState()
}

// Close 关闭 WAL。
//
// 由于 FileWAL 采用“操作即打开文件、完成即关闭文件”的方式，不长期持有文件句柄，
// 所以 Close 主要是切换生命周期状态，而不是大量资源回收。
func (w *FileWAL) Close(ctx context.Context) error {
	_ = ctx

	w.mu.Lock()
	defer w.mu.Unlock()
	w.opened = false

	return nil
}

func (w *FileWAL) loadState() (raftpb.HardState, error) {
	path := filepath.Join(w.dir, stateFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return raftpb.HardState{}, nil
		}
		return raftpb.HardState{}, err
	}

	return w.codec.DecodeState(data)
}

func (w *FileWAL) loadEntries() ([]raftpb.Entry, []Segment, error) {
	pattern := filepath.Join(w.dir, "*.wal")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(paths)

	var entries []raftpb.Entry
	segments := make([]Segment, 0, len(paths))
	for _, path := range paths {
		fileEntries, err := readSegment(path, w.codec)
		if err != nil {
			return nil, nil, err
		}
		if len(fileEntries) == 0 {
			continue
		}
		entries = append(entries, fileEntries...)
		segments = append(segments, Segment{
			ID:         parseSegmentID(path),
			FirstIndex: fileEntries[0].Index,
			LastIndex:  fileEntries[len(fileEntries)-1].Index,
			Path:       path,
		})
	}

	return entries, segments, nil
}

func (w *FileWAL) persistState() error {
	path := filepath.Join(w.dir, stateFileName)
	tmpPath := path + ".tmp"

	data, err := w.codec.EncodeState(w.state)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}

	return os.Rename(tmpPath, path)
}

// readSegment 顺序读取单个 segment 文件中的全部日志条目。
//
// 它会按“4 字节长度前缀 + payload”的记录格式逐条读取记录，
// 并交给 codec 解码成 raftpb.Entry。Open 恢复和测试校验都会用到它。
func readSegment(path string, codec Codec) ([]raftpb.Entry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var entries []raftpb.Entry
	for {
		var lenBuf [4]byte
		_, err := io.ReadFull(file, lenBuf[:])
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return nil, err
		}
		recordLen := binary.BigEndian.Uint32(lenBuf[:])
		record := make([]byte, recordLen)
		if _, err := io.ReadFull(file, record); err != nil {
			return nil, err
		}
		entry, err := codec.DecodeEntry(record)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// mergeEntries 按 Raft 日志覆盖语义合并已有日志和新日志。
//
// 它的核心规则是：一旦发现 incoming 的首条索引与 existing 重叠，
// 从重叠点开始的旧 tail 都应该被新日志替换。
// 这个函数会被内存版 WAL 和文件版 WAL 共同复用。
func mergeEntries(existing, incoming []raftpb.Entry) []raftpb.Entry {
	if len(incoming) == 0 {
		result := make([]raftpb.Entry, len(existing))
		copy(result, existing)
		return result
	}
	if len(existing) == 0 {
		result := make([]raftpb.Entry, len(incoming))
		copy(result, incoming)
		return result
	}

	result := make([]raftpb.Entry, len(existing))
	copy(result, existing)

	firstIncomingIndex := incoming[0].Index
	pos := sort.Search(len(result), func(i int) bool {
		return result[i].Index >= firstIncomingIndex
	})
	if pos < len(result) {
		result = result[:pos]
	}
	result = append(result, incoming...)

	return result
}

// cloneEntries 复制一份日志切片，避免调用方或内部逻辑共享底层数组。
func cloneEntries(entries []raftpb.Entry) []raftpb.Entry {
	items := make([]raftpb.Entry, len(entries))
	copy(items, entries)
	return items
}

// nextSegmentID 返回下一个可用的 segment 编号。
//
// segment 文件名依赖这个编号保持单调递增，从而让恢复时按字典序读取即可得到正确顺序。
func nextSegmentID(segments []Segment) uint64 {
	if len(segments) == 0 {
		return 1
	}
	return segments[len(segments)-1].ID + 1
}

// appendEntriesIncremental 将一批连续日志按 segment 规则增量追加到磁盘。
//
// 它是 FileWAL 正常写路径的核心：
// 1. 优先尝试继续写入最后一个 segment。
// 2. 当最后一个 segment 超过大小阈值时，自动滚动到新 segment。
// 3. 每追加一条记录，都同步更新内存中的 segment 元信息。
//
// appendToLast 用于控制是否允许复用现有最后一个 segment。
func (w *FileWAL) appendEntriesIncremental(entries []raftpb.Entry, startSegmentID uint64, appendToLast bool) error {
	if len(entries) == 0 {
		return nil
	}

	segments := append([]Segment(nil), w.segments...)
	nextID := startSegmentID
	var lastSegment *Segment
	if appendToLast && len(segments) > 0 && startSegmentID == nextSegmentID(w.segments) {
		lastSegment = &segments[len(segments)-1]
	}

	for _, entry := range entries {
		record, err := w.codec.EncodeEntry(entry)
		if err != nil {
			return err
		}
		recordSize := int64(4 + len(record))

		if lastSegment == nil {
			segment, err := w.createSegment(nextID, entry.Index)
			if err != nil {
				return err
			}
			segments = append(segments, segment)
			lastSegment = &segments[len(segments)-1]
			nextID++
		}

		currentSize, err := fileSize(lastSegment.Path)
		if err != nil {
			return err
		}
		if currentSize > 0 && currentSize+recordSize > w.maxSegmentSize {
			segment, err := w.createSegment(nextID, entry.Index)
			if err != nil {
				return err
			}
			segments = append(segments, segment)
			lastSegment = &segments[len(segments)-1]
			nextID++
		}

		if err := appendRecord(lastSegment.Path, record); err != nil {
			return err
		}
		if lastSegment.FirstIndex == 0 {
			lastSegment.FirstIndex = entry.Index
		}
		lastSegment.LastIndex = entry.Index
	}

	w.segments = segments
	return nil
}

// rewriteTail 从某个重叠索引开始重写 WAL tail。
//
// 当 Append 发现 incoming 与现有 tail 重叠时，会调用这个方法。
// 它的策略是：
// 1. 找到第一个受影响的 segment。
// 2. 保留它之前完全不受影响的 segment 和日志。
// 3. 删除受影响 segment 之后的旧文件。
// 4. 从重写起点开始重新按 segment 规则写入 mergedEntries 的 tail。
//
// 这样比整库重写更节省 I/O，同时仍然满足 Raft 日志覆盖语义。
func (w *FileWAL) rewriteTail(overlapIndex uint64, mergedEntries []raftpb.Entry) error {
	if len(w.segments) == 0 {
		w.entries = nil
		w.segments = nil
		if err := w.appendEntriesIncremental(mergedEntries, 1, false); err != nil {
			return err
		}
		w.entries = cloneEntries(mergedEntries)
		return nil
	}

	segmentIdx := sort.Search(len(w.segments), func(i int) bool {
		return w.segments[i].LastIndex >= overlapIndex
	})
	if segmentIdx >= len(w.segments) {
		if err := w.appendEntriesIncremental(mergedEntries, nextSegmentID(w.segments), true); err != nil {
			return err
		}
		w.entries = cloneEntries(mergedEntries)
		return nil
	}

	rewriteFrom := w.segments[segmentIdx].FirstIndex
	startSegmentID := w.segments[segmentIdx].ID

	preservedSegments := append([]Segment(nil), w.segments[:segmentIdx]...)
	for _, segment := range w.segments[segmentIdx:] {
		if err := os.Remove(segment.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	preservedEntries := make([]raftpb.Entry, 0, len(w.entries))
	for _, entry := range w.entries {
		if entry.Index < rewriteFrom {
			preservedEntries = append(preservedEntries, entry)
		}
	}
	rewriteEntries := make([]raftpb.Entry, 0, len(mergedEntries))
	for _, entry := range mergedEntries {
		if entry.Index >= rewriteFrom {
			rewriteEntries = append(rewriteEntries, entry)
		}
	}

	w.segments = preservedSegments
	w.entries = preservedEntries

	if err := w.appendEntriesIncremental(rewriteEntries, startSegmentID, false); err != nil {
		return err
	}
	w.entries = append(w.entries, cloneEntries(rewriteEntries)...)

	return nil
}

// rewriteAllSegments 按当前内存中的 entries 完整重建所有 segment。
//
// 这是一个偏保守的辅助能力，当前主要保留给需要全量重建的场景或测试辅助，
// 正常的 Append / TruncatePrefix 路径已经尽量避免调用它。
func (w *FileWAL) rewriteAllSegments() error {
	pattern := filepath.Join(w.dir, "*.wal")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	entries := cloneEntries(w.entries)
	w.segments = nil
	if len(entries) == 0 {
		return nil
	}

	return w.appendEntriesIncremental(entries, 1, false)
}

// createSegment 创建一个新的空 segment 文件，并返回对应的元信息。
//
// 它通常在 appendEntriesIncremental 发现需要滚动新 segment 时调用。
func (w *FileWAL) createSegment(id uint64, firstIndex uint64) (Segment, error) {
	path := filepath.Join(w.dir, fmt.Sprintf("%020d.wal", id))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return Segment{}, err
	}
	if err := file.Close(); err != nil {
		return Segment{}, err
	}

	return Segment{
		ID:         id,
		FirstIndex: firstIndex,
		LastIndex:  firstIndex - 1,
		Path:       path,
	}, nil
}

// appendRecord 以追加方式向指定 segment 写入一条记录。
//
// 记录格式固定为：
// 4 字节大端长度前缀 + 实际 payload
// 这种格式实现简单，也便于顺序扫描恢复。
func appendRecord(path string, record []byte) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(record)))
	if _, err := file.Write(lenBuf[:]); err != nil {
		return err
	}
	if _, err := file.Write(record); err != nil {
		return err
	}

	return file.Sync()
}

// rewriteSegmentFile 原子地重写一个 segment 文件内容。
//
// 它会先写入临时文件，再 rename 覆盖旧文件，避免重写过程中留下半写入状态。
// TruncatePrefix 重写边界 segment 时会用到它。
func rewriteSegmentFile(path string, entries []raftpb.Entry, codec Codec) error {
	tmpPath := path + ".tmp"
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		record, err := codec.EncodeEntry(entry)
		if err != nil {
			_ = file.Close()
			return err
		}

		var lenBuf [4]byte
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(record)))
		if _, err := file.Write(lenBuf[:]); err != nil {
			_ = file.Close()
			return err
		}
		if _, err := file.Write(record); err != nil {
			_ = file.Close()
			return err
		}
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}

	return os.Rename(tmpPath, path)
}

// fileSize 返回文件当前大小。
//
// appendEntriesIncremental 会用它判断当前 segment 是否还能继续接收新记录。
func fileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// isEmptyHardState 判断 HardState 是否是零值。
//
// Append 会用它区分“这次没有新的 HardState”与“显式更新 HardState”两种情况。
func isEmptyHardState(state raftpb.HardState) bool {
	return state.Term == 0 && state.Vote == 0 && state.Commit == 0
}

// parseSegmentID 从 segment 文件名中解析出编号。
//
// Open 恢复时需要根据文件名还原 Segment.ID，便于后续继续顺序分配新文件。
func parseSegmentID(path string) uint64 {
	var id uint64
	_, _ = fmt.Sscanf(filepath.Base(path), "%d.wal", &id)
	return id
}
