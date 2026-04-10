package grpctransport

import "context"

// SnapshotStream 描述内部快照流的最小能力。
//
// 快照传输和普通 Raft message 的特点不同：
// 1. 数据量大。
// 2. 需要分片上传和下载。
// 3. 往往和独立的安装流程绑定。
//
// 因此这里把快照能力单独抽出来，避免调用方把两类流量混在一起处理。
type SnapshotStream interface {
	Install(ctx context.Context, chunks []SnapshotChunk) error
	Download(ctx context.Context, term, index uint64) ([]SnapshotChunk, error)
}
