package runtime

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/chenwenlong-java/StarMap/internal/raftnode"
	"github.com/chenwenlong-java/StarMap/internal/snapshot"
	grpctransport "github.com/chenwenlong-java/StarMap/internal/transport/grpc"
	raftpb "go.etcd.io/raft/v3/raftpb"
)

// DefaultSnapshotChunk 是快照分片传输的默认 chunk 大小。
const DefaultSnapshotChunk = 64 * 1024

// InternalTransportService 实现节点内部 gRPC 服务。
type InternalTransportService struct {
	node          *raftnode.RaftNode
	snapshotStore snapshot.Store
	mu            sync.Mutex
	install       map[string]*snapshotInstallSession
}

type snapshotInstallSession struct {
	meta grpctransport.SnapshotMetadata
	buf  []byte
}

// NewInternalTransportService 创建内部 gRPC 传输服务实现。
func NewInternalTransportService(node *raftnode.RaftNode, snapshotStore snapshot.Store) *InternalTransportService {
	return &InternalTransportService{
		node:          node,
		snapshotStore: snapshotStore,
		install:       make(map[string]*snapshotInstallSession),
	}
}

// SendRaftMessages 将收到的一批内部 Raft 消息交给本地节点处理。
func (s *InternalTransportService) SendRaftMessages(ctx context.Context, batch grpctransport.RaftMessageBatch) error {
	for _, envelope := range batch.Messages {
		var message raftpb.Message
		if err := message.Unmarshal(envelope.Payload); err != nil {
			return err
		}
		if err := s.node.Step(ctx, message); err != nil {
			return err
		}
	}

	return nil
}

// InstallSnapshotChunk 处理来自远端节点的快照分片安装。
func (s *InternalTransportService) InstallSnapshotChunk(ctx context.Context, chunk grpctransport.SnapshotChunk) error {
	key := snapshotKey(chunk.Metadata.Term, chunk.Metadata.Index)

	s.mu.Lock()
	session, ok := s.install[key]
	if !ok {
		session = &snapshotInstallSession{meta: chunk.Metadata}
		s.install[key] = session
	}
	if chunk.Offset != uint64(len(session.buf)) {
		s.mu.Unlock()
		return fmt.Errorf("unexpected snapshot offset: got=%d want=%d", chunk.Offset, len(session.buf))
	}
	session.buf = append(session.buf, chunk.Data...)
	if !chunk.EOF {
		s.mu.Unlock()
		return nil
	}

	data := append([]byte(nil), session.buf...)
	delete(s.install, key)
	s.mu.Unlock()

	return s.node.InstallSnapshot(ctx, snapshot.Metadata{
		Term:      chunk.Metadata.Term,
		Index:     chunk.Metadata.Index,
		ConfState: append([]byte(nil), chunk.Metadata.ConfState...),
		Checksum:  chunk.Metadata.Checksum,
		FileSize:  chunk.Metadata.FileSize,
	}, data)
}

// DownloadSnapshot 返回指定快照的分片数据。
func (s *InternalTransportService) DownloadSnapshot(ctx context.Context, term, index uint64) ([]grpctransport.SnapshotChunk, error) {
	meta, reader, err := s.snapshotStore.OpenLatest(ctx)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	if term != 0 && index != 0 && (meta.Term != term || meta.Index != index) {
		return nil, fmt.Errorf("snapshot term=%d index=%d not found", term, index)
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}

	return splitSnapshotChunks(grpctransport.SnapshotMetadata{
		Term:      meta.Term,
		Index:     meta.Index,
		ConfState: append([]byte(nil), meta.ConfState...),
		Checksum:  meta.Checksum,
		FileSize:  meta.FileSize,
	}, data, DefaultSnapshotChunk), nil
}

func snapshotKey(term, index uint64) string {
	return fmt.Sprintf("%d-%d", term, index)
}
