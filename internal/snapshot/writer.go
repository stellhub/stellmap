package snapshot

import "io"

// Writer 定义快照写入能力。
type Writer interface {
	WriteChunk(chunk []byte) (int, error)
	Close() error
}

// NopWriter 是最小快照 writer 骨架。
type NopWriter struct {
	w io.Writer
}

// NewWriter 创建一个最小写入器。
func NewWriter(w io.Writer) *NopWriter {
	return &NopWriter{w: w}
}

// WriteChunk 写入一个快照分片。
func (w *NopWriter) WriteChunk(chunk []byte) (int, error) {
	return w.w.Write(chunk)
}

// Close 关闭写入器。
func (w *NopWriter) Close() error {
	return nil
}
