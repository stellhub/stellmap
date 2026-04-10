package snapshot

import (
	"bytes"
	"context"
	"io"
	"sync"
)

// MemoryStore 是内存版快照存储实现。
//
// 它主要用于测试和早期联调，目标是用最小代价模拟 FileStore 的行为：
// 1. 只保留最近一个快照。
// 2. 支持 Create / OpenLatest / Install / Cleanup 这组基础语义。
// 3. 不触碰磁盘，因此适合单元测试中的快速验证。
type MemoryStore struct {
	// mu 保护 latest 和 data，避免并发创建、安装、读取快照时出现数据竞争。
	mu sync.RWMutex
	// latest 记录最近一次创建或安装的快照元信息。
	latest Metadata
	// data 保存最近一个快照文件的完整内容。
	data []byte
}

// NewMemoryStore 创建内存版快照存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

// Create 创建一个新快照。
//
// 调用方会传入 exporter，由 exporter 把状态机内容写入这里的缓冲区。
// 最终只保留最近的一份快照及其元信息。
func (s *MemoryStore) Create(ctx context.Context, meta Metadata, exporter Exporter) (Metadata, error) {
	_ = ctx
	var buf bytes.Buffer
	if exporter != nil {
		if err := exporter(&buf); err != nil {
			return Metadata{}, err
		}
	}
	meta.FileSize = uint64(buf.Len())

	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest = meta
	s.data = append(s.data[:0], buf.Bytes()...)

	return meta, nil
}

// OpenLatest 打开最新快照。
//
// 它通常在节点重启恢复时被调用，让上层拿到最近一次快照的元信息和内容。
func (s *MemoryStore) OpenLatest(ctx context.Context) (Metadata, io.ReadCloser, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.latest, io.NopCloser(bytes.NewReader(s.data)), nil
}

// Install 安装一个快照。
//
// 这一步通常对应“从远端收到 snapshot 后写入本地存储”的动作。
func (s *MemoryStore) Install(ctx context.Context, meta Metadata, r io.Reader) error {
	_ = ctx
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	meta.FileSize = uint64(len(data))
	s.latest = meta
	s.data = data

	return nil
}

// Cleanup 清理历史快照。
//
// 内存版实现只保留最新一份，因此这里不需要执行真实清理逻辑。
func (s *MemoryStore) Cleanup(ctx context.Context, keep int) error {
	_ = ctx
	_ = keep
	return nil
}
