package wal

import "context"

// Repairer 定义 WAL 修复能力。
type Repairer interface {
	Repair(ctx context.Context) error
}
