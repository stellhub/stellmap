package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/stellhub/stellmap/internal/raftnode"
	grpctransport "github.com/stellhub/stellmap/internal/transport/grpc"
	raftpb "go.etcd.io/raft/v3/raftpb"
)

// PeerTransport 负责把 Ready 中的内部消息转发给远端节点。
type PeerTransport struct {
	selfID    uint64
	book      *AddressBook
	chunkSize int
	mu        sync.Mutex
	clients   map[uint64]*grpctransport.PeerClient
}

// NewPeerTransport 创建一个新的对端转发器。
func NewPeerTransport(selfID uint64, book *AddressBook, chunkSize int) *PeerTransport {
	if chunkSize <= 0 {
		chunkSize = DefaultSnapshotChunk
	}

	return &PeerTransport{
		selfID:    selfID,
		book:      book,
		chunkSize: chunkSize,
		clients:   make(map[uint64]*grpctransport.PeerClient),
	}
}

// Forward 将 Ready 中的内部消息按目标节点转发出去。
func (t *PeerTransport) Forward(ctx context.Context, ready raftnode.Ready) error {
	grouped := make(map[uint64][]grpctransport.RaftEnvelope)
	var forwardErr error

	for _, message := range ready.Messages {
		if message.To == 0 || message.To == t.selfID {
			continue
		}
		if message.Raw.Type == raftpb.MsgSnap {
			if err := t.sendSnapshot(ctx, message.Raw); err != nil {
				forwardErr = errors.Join(forwardErr, fmt.Errorf("send snapshot to node %d: %w", message.To, err))
			}
			continue
		}

		grouped[message.To] = append(grouped[message.To], grpctransport.RaftEnvelope{
			From:    message.From,
			To:      message.To,
			Payload: append([]byte(nil), message.Payload...),
		})
	}

	for targetID, items := range grouped {
		client, err := t.client(targetID)
		if err != nil {
			forwardErr = errors.Join(forwardErr, fmt.Errorf("create peer client for node %d: %w", targetID, err))
			continue
		}
		if err := client.Send(ctx, grpctransport.RaftMessageBatch{Messages: items}); err != nil {
			forwardErr = errors.Join(forwardErr, fmt.Errorf("send raft batch to node %d: %w", targetID, err))
		}
	}

	return forwardErr
}

// Close 关闭所有已建立的对端 gRPC 客户端。
func (t *PeerTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	var closeErr error
	for id, client := range t.clients {
		if err := client.Close(); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close peer client %d: %w", id, err))
		}
	}
	t.clients = make(map[uint64]*grpctransport.PeerClient)

	return closeErr
}

// UpsertPeer 更新或新增一个节点的地址，并重置对应的客户端连接缓存。
func (t *PeerTransport) UpsertPeer(nodeID uint64, httpAddr, grpcAddr, adminAddr string) error {
	t.book.Set(nodeID, httpAddr, grpcAddr, adminAddr)

	t.mu.Lock()
	defer t.mu.Unlock()

	if client, ok := t.clients[nodeID]; ok {
		if err := client.Close(); err != nil {
			return err
		}
		delete(t.clients, nodeID)
	}

	return nil
}

// RemovePeer 从地址簿和连接缓存中删除一个节点。
func (t *PeerTransport) RemovePeer(nodeID uint64) error {
	t.book.Delete(nodeID)

	t.mu.Lock()
	defer t.mu.Unlock()

	if client, ok := t.clients[nodeID]; ok {
		if err := client.Close(); err != nil {
			return err
		}
		delete(t.clients, nodeID)
	}

	return nil
}

func (t *PeerTransport) client(targetID uint64) (*grpctransport.PeerClient, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if client, ok := t.clients[targetID]; ok {
		return client, nil
	}

	addr := t.book.GRPCAddr(targetID)
	if addr == "" {
		return nil, fmt.Errorf("grpc address for node %d not configured", targetID)
	}

	client, err := grpctransport.NewPeerClient(addr)
	if err != nil {
		return nil, err
	}
	t.clients[targetID] = client

	return client, nil
}

func (t *PeerTransport) sendSnapshot(ctx context.Context, message raftpb.Message) error {
	if message.To == 0 || message.To == t.selfID {
		return nil
	}

	client, err := t.client(message.To)
	if err != nil {
		return err
	}
	if len(message.Snapshot.Data) == 0 {
		return fmt.Errorf("snapshot message to node %d has empty snapshot data", message.To)
	}

	meta := grpctransport.SnapshotMetadata{
		Term:      message.Snapshot.Metadata.Term,
		Index:     message.Snapshot.Metadata.Index,
		ConfState: marshalConfState(message.Snapshot.Metadata.ConfState),
		FileSize:  uint64(len(message.Snapshot.Data)),
	}

	return client.InstallSnapshot(ctx, splitSnapshotChunks(meta, message.Snapshot.Data, t.chunkSize))
}

func splitSnapshotChunks(meta grpctransport.SnapshotMetadata, data []byte, chunkSize int) []grpctransport.SnapshotChunk {
	if chunkSize <= 0 {
		chunkSize = DefaultSnapshotChunk
	}
	if len(data) == 0 {
		return []grpctransport.SnapshotChunk{{
			Metadata: meta,
			Offset:   0,
			EOF:      true,
		}}
	}

	chunks := make([]grpctransport.SnapshotChunk, 0, (len(data)/chunkSize)+1)
	for offset := 0; offset < len(data); offset += chunkSize {
		end := offset + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunks = append(chunks, grpctransport.SnapshotChunk{
			Metadata: meta,
			Data:     append([]byte(nil), data[offset:end]...),
			Offset:   uint64(offset),
			EOF:      end == len(data),
		})
	}

	return chunks
}

func marshalConfState(confState raftpb.ConfState) []byte {
	data, err := confState.Marshal()
	if err != nil {
		return nil
	}

	return data
}
