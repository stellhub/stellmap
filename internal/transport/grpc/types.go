package grpctransport

import stellmapv1 "github.com/stellhub/stellmap/api/gen/go/stellmap/v1"

// RaftEnvelope 表示一条节点间传输的内部 Raft 消息。
//
// 这里故意没有直接把 raftpb.Message 暴露到 transport 层，而是收敛成一个更轻量的信封结构：
// 1. From / To 用于快速标识发送方和接收方。
// 2. Payload 保存已经编码好的 raftpb.Message 字节流。
//
// 这样可以把“Raft 消息如何编码”与“消息如何通过 gRPC 发送”解耦，transport 层只负责搬运。
type RaftEnvelope struct {
	// From 是发送节点 ID。
	From uint64
	// To 是目标节点 ID。
	To uint64
	// Payload 是已经序列化后的 raftpb.Message。
	// 发送侧通常来自 raftnode.Ready() 中的 message.Raw.Marshal() 结果。
	Payload []byte
}

// RaftMessageBatch 表示一批待发送的 Raft 消息。
//
// 在实际运行中，同一个 Ready 往往会产出多条发往不同节点的消息。
// peerTransport 会先按目标节点分组，然后把同一目标节点的一组消息打包成 batch，
// 以减少 RPC 次数和 gRPC 调用开销。
type RaftMessageBatch struct {
	Messages []RaftEnvelope
}

// SnapshotMetadata 表示快照传输时附带的元信息。
//
// 快照体积可能很大，因此传输时会切成多个 chunk。元信息会跟随 chunk 一起传递，
// 以便接收端在流式接收过程中也能知道当前正在安装的是哪一份快照。
type SnapshotMetadata struct {
	// Term 是该快照对应的最后日志任期。
	Term uint64
	// Index 是该快照覆盖到的最后日志索引。
	Index uint64
	// ConfState 是快照对应的成员视图。
	// 这里直接保存序列化后的字节，避免 transport 层直接依赖 raftpb.ConfState。
	ConfState []byte
	// Checksum 用于校验快照文件完整性。
	Checksum string
	// FileSize 是快照完整字节大小。
	// 接收端和下载端都可以用它做完整性与观测信息展示。
	FileSize uint64
}

// SnapshotChunk 表示快照传输分片。
//
// 大快照不会一次性装进单个 RPC 消息里，而是拆成多个 chunk 通过流式 RPC 传输。
// Offset + EOF 用于保证接收端能按顺序拼接，并明确知道何时安装完成。
type SnapshotChunk struct {
	// Metadata 描述当前快照的全局信息。
	Metadata SnapshotMetadata
	// Data 是当前分片携带的实际快照字节。
	Data []byte
	// Offset 是当前分片在整份快照中的起始偏移。
	Offset uint64
	// EOF 标识当前分片是否为最后一个分片。
	EOF bool
}

// toProtoRaftBatch 将本地领域模型转换成 protobuf 请求结构。
//
// 发送侧会在真正发 gRPC 前调用它，把内部定义的 batch 转成生成代码需要的 pb 类型。
func toProtoRaftBatch(batch RaftMessageBatch) *stellmapv1.RaftMessageBatch {
	items := make([]*stellmapv1.RaftEnvelope, 0, len(batch.Messages))
	for _, message := range batch.Messages {
		items = append(items, &stellmapv1.RaftEnvelope{
			From:    message.From,
			To:      message.To,
			Payload: append([]byte(nil), message.Payload...),
		})
	}

	return &stellmapv1.RaftMessageBatch{Messages: items}
}

// fromProtoRaftBatch 把 protobuf 请求结构还原为本地领域模型。
//
// 服务端收到 gRPC 请求后会调用它，再把结果交给上层 service 处理。
func fromProtoRaftBatch(batch *stellmapv1.RaftMessageBatch) RaftMessageBatch {
	if batch == nil {
		return RaftMessageBatch{}
	}

	items := make([]RaftEnvelope, 0, len(batch.Messages))
	for _, message := range batch.Messages {
		if message == nil {
			continue
		}
		items = append(items, RaftEnvelope{
			From:    message.From,
			To:      message.To,
			Payload: append([]byte(nil), message.Payload...),
		})
	}

	return RaftMessageBatch{Messages: items}
}

// toProtoSnapshotMetadata 将本地快照元信息转换成 protobuf 消息。
func toProtoSnapshotMetadata(meta SnapshotMetadata) *stellmapv1.SnapshotMetadata {
	return &stellmapv1.SnapshotMetadata{
		Term:      meta.Term,
		Index:     meta.Index,
		ConfState: append([]byte(nil), meta.ConfState...),
		Checksum:  meta.Checksum,
		FileSize:  meta.FileSize,
	}
}

// fromProtoSnapshotMetadata 将 protobuf 快照元信息转换成本地结构。
func fromProtoSnapshotMetadata(meta *stellmapv1.SnapshotMetadata) SnapshotMetadata {
	if meta == nil {
		return SnapshotMetadata{}
	}

	return SnapshotMetadata{
		Term:      meta.Term,
		Index:     meta.Index,
		ConfState: append([]byte(nil), meta.ConfState...),
		Checksum:  meta.Checksum,
		FileSize:  meta.FileSize,
	}
}

// toProtoSnapshotChunk 将本地快照分片转换成安装快照流使用的 protobuf 消息。
func toProtoSnapshotChunk(chunk SnapshotChunk) *stellmapv1.InstallSnapshotChunk {
	return &stellmapv1.InstallSnapshotChunk{
		Metadata: toProtoSnapshotMetadata(chunk.Metadata),
		Data:     append([]byte(nil), chunk.Data...),
		Offset:   chunk.Offset,
		Eof:      chunk.EOF,
	}
}

// fromProtoInstallChunk 将安装快照流中的 protobuf 分片还原为本地结构。
func fromProtoInstallChunk(chunk *stellmapv1.InstallSnapshotChunk) SnapshotChunk {
	if chunk == nil {
		return SnapshotChunk{}
	}

	return SnapshotChunk{
		Metadata: fromProtoSnapshotMetadata(chunk.Metadata),
		Data:     append([]byte(nil), chunk.Data...),
		Offset:   chunk.Offset,
		EOF:      chunk.Eof,
	}
}

// fromProtoDownloadChunk 将下载快照流中的 protobuf 分片还原为本地结构。
func fromProtoDownloadChunk(chunk *stellmapv1.DownloadSnapshotChunk) SnapshotChunk {
	if chunk == nil {
		return SnapshotChunk{}
	}

	return SnapshotChunk{
		Metadata: fromProtoSnapshotMetadata(chunk.Metadata),
		Data:     append([]byte(nil), chunk.Data...),
		Offset:   chunk.Offset,
		EOF:      chunk.Eof,
	}
}
