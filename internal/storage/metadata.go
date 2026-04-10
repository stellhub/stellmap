package storage

// Metadata 描述状态机本地持久化元信息。
type Metadata struct {
	AppliedIndex  uint64
	AppliedTerm   uint64
	SnapshotIndex uint64
	SnapshotTerm  uint64
}
