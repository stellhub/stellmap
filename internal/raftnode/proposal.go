package raftnode

import "time"

// Proposal 表示待提交到 Raft 日志的命令。
type Proposal struct {
	Data      []byte
	CreatedAt time.Time
}
