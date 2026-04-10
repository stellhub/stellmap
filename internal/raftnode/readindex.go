package raftnode

import "time"

// ReadState 表示一次线性一致读请求的确认结果。
type ReadState struct {
	Index       uint64
	RequestCtx  []byte
	ConfirmedAt time.Time
}
