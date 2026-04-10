package wal

// Segment 描述一个 WAL segment 的元信息。
//
// FileWAL 会把连续的 Raft Log 切成多个 segment 文件，而不是把所有日志都写进单个大文件。
// 这样设计有几个直接好处：
// 1. 日志追加时只会触碰最后一个 segment，顺序写更稳定。
// 2. 日志压缩时可以按 segment 粒度直接回收整段旧文件。
// 3. 日志覆盖或冲突时，只需要重写受影响的 tail，而不是整库重写。
//
// 这个结构体不会直接暴露给上层业务，而是供 FileWAL 在 Open / Append / TruncatePrefix /
// 恢复和测试中维护“磁盘文件 <-> 日志索引区间”的映射关系。
type Segment struct {
	// ID 是 segment 的单调递增编号，同时也是文件名的一部分。
	// 它主要用于保证磁盘文件的顺序稳定，便于按字典序恢复。
	ID uint64
	// FirstIndex 是当前 segment 覆盖的第一条日志索引。
	// 在压缩和 tail rewrite 时，会用它判断某个 segment 是否受影响。
	FirstIndex uint64
	// LastIndex 是当前 segment 覆盖的最后一条日志索引。
	// Open / TruncatePrefix / rewriteTail 都会基于它快速定位边界 segment。
	LastIndex uint64
	// Path 是 segment 文件在本地磁盘上的绝对路径。
	// 追加、重写、删除和恢复都会使用它直接操作文件。
	Path string
}
