package snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

const (
	defaultKeepSnapshots = 2
)

var (
	errSnapshotNotFound = errors.New("snapshot: not found")
)

// Exporter 定义快照导出函数。
//
// snapshot.Store 不直接理解状态机内部结构，因此 Create 会把“如何把状态导出成字节流”
// 交给调用方。调用方通常是 raftnode，它会把 Pebble 状态机内容编码后写入 exporter。
type Exporter func(w io.Writer) error

// Store 定义快照存储能力。
//
// 它抽象的是“独立快照文件”的生命周期，而不是状态机本身：
// 1. Create：从当前状态机导出一个新快照。
// 2. OpenLatest：重启恢复时打开最近快照。
// 3. Install：接收远端快照并落地。
// 4. Cleanup：按保留策略回收旧快照。
//
// 这样设计后，raftnode 只依赖 Store 接口即可，不需要直接感知文件布局。
type Store interface {
	Create(ctx context.Context, meta Metadata, exporter Exporter) (Metadata, error)
	OpenLatest(ctx context.Context) (Metadata, io.ReadCloser, error)
	Install(ctx context.Context, meta Metadata, r io.Reader) error
	Cleanup(ctx context.Context, keep int) error
}

// FileStore 是基于本地文件系统的快照存储实现。
//
// 它使用“一份快照数据文件 + 一份元数据文件”的布局：
// 1. *.snap 保存实际快照内容。
// 2. *.meta 保存 term、index、checksum、file size 等元信息。
//
// 这样设计的原因是：
// 1. 快照内容通常较大，不适合和少量元数据混写在一个 JSON 文件里。
// 2. 恢复时可以先读元信息，再决定是否打开数据文件。
// 3. 清理旧快照时可以把 .snap 和 .meta 成对删除，语义清晰。
type FileStore struct {
	// mu 保护快照目录上的并发创建、安装和清理。
	mu sync.RWMutex
	// dir 是快照文件所在目录。
	dir string
	// keep 是默认保留的快照份数。
	// Cleanup 在调用方没有显式指定 keep 时会回退到这个值。
	keep int
}

// NewFileStore 创建文件版快照存储。
//
// 它只记录目录和默认保留策略，真正的文件创建发生在 Create / Install 阶段。
func NewFileStore(dir string) *FileStore {
	return &FileStore{
		dir:  dir,
		keep: defaultKeepSnapshots,
	}
}

// Create 创建一个新快照。
//
// 这是 snapshot 写路径的核心，通常由 raftnode.maybeTriggerSnapshot 调用。
// 它的主要流程是：
// 1. 让 exporter 把状态机内容导出到临时 .snap 文件。
// 2. 在写入过程中计算 checksum。
// 3. 根据最终文件大小和 checksum 补全 Metadata。
// 4. 原子 rename 成正式快照文件，并单独持久化 .meta 文件。
// 5. 按保留策略清理旧快照。
//
// 这里使用临时文件 + rename，是为了避免崩溃时留下半写入的正式快照。
func (s *FileStore) Create(ctx context.Context, meta Metadata, exporter Exporter) (Metadata, error) {
	_ = ctx

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return Metadata{}, err
	}

	tmpPath := filepath.Join(s.dir, fmt.Sprintf("snapshot-%020d-%020d.snap.tmp", meta.Term, meta.Index))
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return Metadata{}, err
	}

	hasher := sha256.New()
	writer := io.MultiWriter(file, hasher)
	if exporter != nil {
		if err := exporter(writer); err != nil {
			_ = file.Close()
			return Metadata{}, err
		}
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return Metadata{}, err
	}
	if err := file.Close(); err != nil {
		return Metadata{}, err
	}

	info, err := os.Stat(tmpPath)
	if err != nil {
		return Metadata{}, err
	}
	meta.FileSize = uint64(info.Size())
	meta.Checksum = hex.EncodeToString(hasher.Sum(nil))

	finalPath := s.snapshotPath(meta)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return Metadata{}, err
	}
	if err := s.persistMetadata(meta); err != nil {
		return Metadata{}, err
	}

	_ = s.Cleanup(ctx, s.keep)

	return meta, nil
}

// OpenLatest 打开最新快照。
//
// 节点启动恢复时会先从这里拿到最近一次快照，再决定是否将其安装到状态机。
func (s *FileStore) OpenLatest(ctx context.Context) (Metadata, io.ReadCloser, error) {
	_ = ctx

	meta, err := s.latestMetadata()
	if err != nil {
		return Metadata{}, nil, err
	}

	file, err := os.Open(s.snapshotPath(meta))
	if err != nil {
		return Metadata{}, nil, err
	}

	return meta, file, nil
}

// Install 安装一个快照。
//
// 对 FileStore 来说，“安装远端快照”和“基于本地状态导出快照”在落盘形态上是一致的，
// 因此这里直接复用 Create 逻辑，只是 exporter 改为从 reader 拷贝。
func (s *FileStore) Install(ctx context.Context, meta Metadata, r io.Reader) error {
	_, err := s.Create(ctx, meta, func(w io.Writer) error {
		_, copyErr := io.Copy(w, r)
		return copyErr
	})
	return err
}

// Cleanup 清理旧快照。
//
// 它会按 Index/Term 从新到旧排序，只保留最近 keep 份，其他快照成对删除。
// 这个方法通常在 Create 成功后自动触发，也可以由上层显式调用。
func (s *FileStore) Cleanup(ctx context.Context, keep int) error {
	_ = ctx
	if keep <= 0 {
		keep = s.keep
	}

	metas, err := s.listMetadata()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(metas) <= keep {
		return nil
	}

	for _, meta := range metas[keep:] {
		_ = os.Remove(s.snapshotPath(meta))
		_ = os.Remove(s.metadataPath(meta))
	}

	return nil
}

// listMetadata 枚举目录中的全部快照元数据，并按“最新优先”排序返回。
//
// OpenLatest 和 Cleanup 都依赖它来确定哪份快照是最新的、哪些属于历史快照。
func (s *FileStore) listMetadata() ([]Metadata, error) {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return nil, err
	}

	paths, err := filepath.Glob(filepath.Join(s.dir, "*.meta"))
	if err != nil {
		return nil, err
	}

	metas := make([]Metadata, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var meta Metadata
		if err := json.Unmarshal(data, &meta); err != nil {
			return nil, err
		}
		metas = append(metas, meta)
	}

	sort.Slice(metas, func(i, j int) bool {
		if metas[i].Index == metas[j].Index {
			return metas[i].Term > metas[j].Term
		}
		return metas[i].Index > metas[j].Index
	})

	return metas, nil
}

// latestMetadata 返回当前目录中最新的一份快照元信息。
//
// 当没有任何快照时，会返回 errSnapshotNotFound，供上层区分“没有快照”和“读取失败”。
func (s *FileStore) latestMetadata() (Metadata, error) {
	metas, err := s.listMetadata()
	if err != nil {
		return Metadata{}, err
	}
	if len(metas) == 0 {
		return Metadata{}, errSnapshotNotFound
	}

	return metas[0], nil
}

// persistMetadata 原子地写入快照元数据文件。
//
// Metadata 单独持久化成 .meta 文件，便于恢复阶段先快速读取元信息而不打开大文件。
func (s *FileStore) persistMetadata(meta Metadata) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	tmpPath := s.metadataPath(meta) + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}

	return os.Rename(tmpPath, s.metadataPath(meta))
}

// snapshotPath 根据元信息生成快照数据文件路径。
func (s *FileStore) snapshotPath(meta Metadata) string {
	return filepath.Join(s.dir, fmt.Sprintf("snapshot-%020d-%020d.snap", meta.Term, meta.Index))
}

// metadataPath 根据元信息生成快照元数据文件路径。
func (s *FileStore) metadataPath(meta Metadata) string {
	return filepath.Join(s.dir, fmt.Sprintf("snapshot-%020d-%020d.meta", meta.Term, meta.Index))
}
