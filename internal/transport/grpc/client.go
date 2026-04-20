package grpctransport

import (
	"context"
	"io"

	stellmapv1 "github.com/stellhub/stellmap/api/gen/go/stellmap/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Client 描述内部 gRPC 客户端需要具备的最小能力。
//
// 它把节点间通信需要的能力收敛成三件事：
// 1. Send：发送普通 Raft 消息。
// 2. InstallSnapshot：上传快照。
// 3. DownloadSnapshot：下载快照。
//
// 更高层的 peerTransport 只依赖这些动作，不需要关心 protobuf 生成代码细节。
type Client interface {
	Send(ctx context.Context, batch RaftMessageBatch) error
	InstallSnapshot(ctx context.Context, chunks []SnapshotChunk) error
	DownloadSnapshot(ctx context.Context, term, index uint64) ([]SnapshotChunk, error)
}

// PeerClient 是节点间 gRPC 客户端实现。
//
// 它持有到底层 grpc.ClientConn 以及两个 protobuf client：
// 1. RaftTransportClient 负责普通 Raft 消息。
// 2. SnapshotServiceClient 负责快照上传和下载。
type PeerClient struct {
	// target 是对端 gRPC 地址，主要用于日志和调试。
	target string
	// conn 是底层 gRPC 连接。
	conn *grpc.ClientConn
	// raftClient 用于发送普通 Raft 消息。
	raftClient stellmapv1.RaftTransportClient
	// snapshotClient 用于安装和下载快照。
	snapshotClient stellmapv1.SnapshotServiceClient
}

// NewPeerClient 创建一个对端客户端。
//
// 它通常由 `peerTransport.client()` 在首次向某个节点发消息时懒加载创建。
func NewPeerClient(target string) (*PeerClient, error) {
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	return &PeerClient{
		target:         target,
		conn:           conn,
		raftClient:     stellmapv1.NewRaftTransportClient(conn),
		snapshotClient: stellmapv1.NewSnapshotServiceClient(conn),
	}, nil
}

// Send 发送一批普通 Raft 消息。
//
// 这是最常走的内部复制路径。peerTransport 会把一个 Ready 中发往同一目标节点的消息打包后，
// 调用这个方法执行一次 RPC。
func (c *PeerClient) Send(ctx context.Context, batch RaftMessageBatch) error {
	_, err := c.raftClient.Send(ctx, toProtoRaftBatch(batch))
	if err != nil {
		return err
	}

	return nil
}

// InstallSnapshot 上传快照分片。
//
// 它会创建一个客户端流，然后顺序发送全部 chunk，最后等待服务端确认安装完成。
func (c *PeerClient) InstallSnapshot(ctx context.Context, chunks []SnapshotChunk) error {
	stream, err := c.snapshotClient.Install(ctx)
	if err != nil {
		return err
	}

	for _, chunk := range chunks {
		if err := stream.Send(toProtoSnapshotChunk(chunk)); err != nil {
			return err
		}
	}

	_, err = stream.CloseAndRecv()
	if err != nil {
		return err
	}

	return nil
}

// DownloadSnapshot 下载指定快照。
//
// 该方法会持续接收服务端流返回的 chunk，直到遇到 EOF 或最后一个分片。
func (c *PeerClient) DownloadSnapshot(ctx context.Context, term, index uint64) ([]SnapshotChunk, error) {
	stream, err := c.snapshotClient.Download(ctx, &stellmapv1.DownloadSnapshotRequest{
		Term:  term,
		Index: index,
	})
	if err != nil {
		return nil, err
	}

	var chunks []SnapshotChunk
	for {
		chunk, recvErr := stream.Recv()
		if recvErr != nil {
			if recvErr == io.EOF {
				break
			}
			return nil, recvErr
		}
		chunks = append(chunks, fromProtoDownloadChunk(chunk))
		if chunk.Eof {
			break
		}
	}

	return chunks, nil
}

// Close 关闭底层 gRPC 连接。
//
// peerTransport 在节点下线、地址变更或服务整体停止时会调用它回收连接。
func (c *PeerClient) Close() error {
	if c.conn == nil {
		return nil
	}

	return c.conn.Close()
}
