package grpctransport

import "context"

// RaftStream 描述内部 Raft 消息流的最小能力。
//
// 它把“普通 Raft 消息转发”从具体客户端实现中单独抽出来，便于后续在测试中替换，
// 或在更高层只依赖发送能力而不暴露完整 PeerClient。
type RaftStream interface {
	Send(ctx context.Context, batch RaftMessageBatch) error
}
