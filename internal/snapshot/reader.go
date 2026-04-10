package snapshot

import "io"

// Reader 定义快照读取能力。
type Reader interface {
	ReadChunk(p []byte) (int, error)
}

// NopReader 是最小快照 reader 骨架。
type NopReader struct {
	r io.Reader
}

// NewReader 创建一个最小读取器。
func NewReader(r io.Reader) *NopReader {
	return &NopReader{r: r}
}

// ReadChunk 读取一个快照分片。
func (r *NopReader) ReadChunk(p []byte) (int, error) {
	return r.r.Read(p)
}
