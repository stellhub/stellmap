package grpctransport

import (
	"context"
	"io"

	starmapv1 "github.com/chenwenlong-java/StarMap/api/gen/go/starmap/v1"
	"google.golang.org/grpc"
)

// Service 描述内部 gRPC 服务端需要实现的领域能力。
//
// 这个接口位于“gRPC handler”与“业务实现”之间：
// 1. gRPC Server 负责 protobuf 编解码和流控制。
// 2. Service 负责真正处理 Raft 消息和快照安装逻辑。
//
// 这样可以避免生成代码类型扩散到主业务逻辑中。
type Service interface {
	SendRaftMessages(ctx context.Context, batch RaftMessageBatch) error
	InstallSnapshotChunk(ctx context.Context, chunk SnapshotChunk) error
	DownloadSnapshot(ctx context.Context, term, index uint64) ([]SnapshotChunk, error)
}

// Server 是内部 gRPC 服务端骨架。
//
// 它把生成出来的 protobuf server 接口适配到本地定义的 Service 上，
// 让 `starmapd` 可以只实现更贴近业务语义的处理逻辑。
type Server struct {
	// service 是真正处理消息的业务实现。
	service Service
	// 下面两个嵌入字段用于兼容 protobuf 生成代码的未实现服务接口。
	starmapv1.UnimplementedRaftTransportServer
	starmapv1.UnimplementedSnapshotServiceServer
}

// NewServer 创建一个内部 gRPC 服务端骨架。
//
// 调用方通常是 `cmd/starmapd/main.go`，会在创建 grpc.Server 后调用 RegisterHandlers 注册。
func NewServer(service Service) *Server {
	return &Server{service: service}
}

// RegisterHandlers 将内部 transport 的两个 gRPC 服务注册到 grpc.Server 上。
//
// 当前会注册：
// 1. RaftTransport：承载普通 Raft message 批量发送。
// 2. SnapshotService：承载快照上传与下载。
func (s *Server) RegisterHandlers(server grpc.ServiceRegistrar) {
	starmapv1.RegisterRaftTransportServer(server, s)
	starmapv1.RegisterSnapshotServiceServer(server, s)
}

// Send 处理一批 Raft 消息。
//
// 这是普通 Raft 复制消息的服务端入口。收到 gRPC 请求后，会先把 protobuf 类型转换为
// 本地 RaftMessageBatch，再交给 service 处理。
func (s *Server) Send(ctx context.Context, batch *starmapv1.RaftMessageBatch) (*starmapv1.RaftMessageAck, error) {
	if err := s.service.SendRaftMessages(ctx, fromProtoRaftBatch(batch)); err != nil {
		return nil, err
	}

	return &starmapv1.RaftMessageAck{}, nil
}

// Install 处理快照上传流。
//
// 发送端会把一份大快照切成多个 InstallSnapshotChunk 顺序发过来。
// 服务端这里逐片接收，并把每个 chunk 交给上层 service 聚合和安装。
func (s *Server) Install(stream starmapv1.SnapshotService_InstallServer) error {
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&starmapv1.InstallSnapshotResponse{})
		}
		if err != nil {
			return err
		}
		if err := s.service.InstallSnapshotChunk(stream.Context(), fromProtoInstallChunk(chunk)); err != nil {
			return err
		}
		if chunk.Eof {
			return stream.SendAndClose(&starmapv1.InstallSnapshotResponse{
				Term:  chunk.Metadata.GetTerm(),
				Index: chunk.Metadata.GetIndex(),
			})
		}
	}
}

// Download 处理快照下载流。
//
// 当对端需要某份快照时，会发起这个流式 RPC。Server 会先向 service 请求完整 chunk 列表，
// 再逐片写回给客户端。
func (s *Server) Download(request *starmapv1.DownloadSnapshotRequest, stream starmapv1.SnapshotService_DownloadServer) error {
	chunks, err := s.service.DownloadSnapshot(stream.Context(), request.GetTerm(), request.GetIndex())
	if err != nil {
		return err
	}

	for _, chunk := range chunks {
		if err := stream.Send(&starmapv1.DownloadSnapshotChunk{
			Metadata: toProtoSnapshotMetadata(chunk.Metadata),
			Data:     append([]byte(nil), chunk.Data...),
			Offset:   chunk.Offset,
			Eof:      chunk.EOF,
		}); err != nil {
			return err
		}
	}

	return nil
}
