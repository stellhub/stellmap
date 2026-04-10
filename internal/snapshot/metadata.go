package snapshot

// Metadata 描述快照元信息。
type Metadata struct {
	Term      uint64
	Index     uint64
	ConfState []byte
	Checksum  string
	FileSize  uint64
}
