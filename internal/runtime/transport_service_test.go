package runtime

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/stellaraxis/starmap/internal/snapshot"
	grpctransport "github.com/stellaraxis/starmap/internal/transport/grpc"
	raftpb "go.etcd.io/raft/v3/raftpb"
)

func TestSplitSnapshotChunksSplitsAndCopiesData(t *testing.T) {
	meta := grpctransport.SnapshotMetadata{Term: 2, Index: 5}
	data := []byte("abcdef")

	chunks := splitSnapshotChunks(meta, data, 2)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	if chunks[0].Offset != 0 || chunks[1].Offset != 2 || chunks[2].Offset != 4 {
		t.Fatalf("unexpected chunk offsets: %+v", chunks)
	}
	if !chunks[2].EOF {
		t.Fatalf("expected last chunk eof")
	}

	chunks[0].Data[0] = 'z'
	if data[0] != 'a' {
		t.Fatalf("chunk data should be copied")
	}
}

func TestSplitSnapshotChunksWithEmptyDataReturnsSingleEOFChunk(t *testing.T) {
	chunks := splitSnapshotChunks(grpctransport.SnapshotMetadata{Term: 1, Index: 1}, nil, 0)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if !chunks[0].EOF || chunks[0].Offset != 0 {
		t.Fatalf("unexpected empty-data chunk: %+v", chunks[0])
	}
}

func TestMarshalConfStateRoundTrip(t *testing.T) {
	confState := raftpb.ConfState{
		Voters:   []uint64{1, 2},
		Learners: []uint64{3},
	}

	data := marshalConfState(confState)
	if len(data) == 0 {
		t.Fatalf("expected marshaled conf state")
	}

	var restored raftpb.ConfState
	if err := restored.Unmarshal(data); err != nil {
		t.Fatalf("unmarshal conf state failed: %v", err)
	}
	if len(restored.Voters) != 2 || restored.Voters[0] != 1 || restored.Learners[0] != 3 {
		t.Fatalf("unexpected restored conf state: %+v", restored)
	}
}

func TestInternalTransportServiceDownloadSnapshot(t *testing.T) {
	ctx := context.Background()
	store := snapshot.NewMemoryStore()
	meta, err := store.Create(ctx, snapshot.Metadata{
		Term:      3,
		Index:     9,
		ConfState: []byte("conf"),
	}, func(w io.Writer) error {
		_, writeErr := w.Write([]byte("snapshot-data"))
		return writeErr
	})
	if err != nil {
		t.Fatalf("create memory snapshot failed: %v", err)
	}

	service := NewInternalTransportService(nil, store)
	chunks, err := service.DownloadSnapshot(ctx, meta.Term, meta.Index)
	if err != nil {
		t.Fatalf("download snapshot failed: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if !bytes.Equal(chunks[0].Data, []byte("snapshot-data")) {
		t.Fatalf("unexpected chunk data: %q", string(chunks[0].Data))
	}
	if chunks[0].Metadata.Term != 3 || chunks[0].Metadata.Index != 9 || !chunks[0].EOF {
		t.Fatalf("unexpected chunk metadata: %+v", chunks[0])
	}

	if _, err := service.DownloadSnapshot(ctx, 7, 8); err == nil {
		t.Fatalf("expected mismatched snapshot error")
	}
}

func TestInternalTransportServiceInstallSnapshotChunkBuffersAndValidatesOffset(t *testing.T) {
	service := NewInternalTransportService(nil, nil)
	ctx := context.Background()
	meta := grpctransport.SnapshotMetadata{Term: 1, Index: 2}

	if err := service.InstallSnapshotChunk(ctx, grpctransport.SnapshotChunk{
		Metadata: meta,
		Data:     []byte("abc"),
		Offset:   0,
	}); err != nil {
		t.Fatalf("install first chunk failed: %v", err)
	}

	session := service.install[snapshotKey(1, 2)]
	if session == nil || !bytes.Equal(session.buf, []byte("abc")) {
		t.Fatalf("expected buffered session, got %+v", session)
	}

	err := service.InstallSnapshotChunk(ctx, grpctransport.SnapshotChunk{
		Metadata: meta,
		Data:     []byte("tail"),
		Offset:   1,
	})
	if err == nil {
		t.Fatalf("expected offset mismatch error")
	}
}
