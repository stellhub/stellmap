package snapshot

import (
	"context"
	"io"
)

// Installer 定义快照安装能力。
type Installer interface {
	Install(ctx context.Context, meta Metadata, r io.Reader) error
}
