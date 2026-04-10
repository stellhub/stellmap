package registry

import "github.com/chenwenlong-java/StarMap/internal/storage"

// CleanupCursor 记录下一轮后台过期清理应该从哪个 key 开始继续扫描。
type CleanupCursor struct {
	ScanStart []byte
}

// Next 返回当前这轮后台清理应该从哪个 key 开始扫描。
func (c *CleanupCursor) Next(defaultStart []byte) []byte {
	if len(c.ScanStart) == 0 {
		return append([]byte(nil), defaultStart...)
	}

	return append([]byte(nil), c.ScanStart...)
}

// Advance 根据本轮扫描结果推进或重置扫描游标。
func (c *CleanupCursor) Advance(defaultStart []byte, items []storage.KV, limit int) {
	if len(items) == 0 || limit <= 0 || len(items) < limit {
		c.ScanStart = append([]byte(nil), defaultStart...)
		return
	}

	c.ScanStart = nextScanStartAfter(items[len(items)-1].Key)
}

func nextScanStartAfter(key []byte) []byte {
	return append(append([]byte(nil), key...), 0)
}
