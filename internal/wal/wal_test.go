package wal

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	raftpb "go.etcd.io/raft/v3/raftpb"
)

func TestFileWALAppendIncrementallyAndRewriteTail(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	w := NewFileWAL(dir)
	w.maxSegmentSize = 1

	if err := w.Open(ctx); err != nil {
		t.Fatalf("open wal: %v", err)
	}

	initial := []raftpb.Entry{
		newEntry(1, 1, "entry-1"),
		newEntry(2, 1, "entry-2"),
		newEntry(3, 1, "entry-3"),
	}
	if err := w.Append(ctx, raftpb.HardState{Term: 1, Commit: 3}, initial); err != nil {
		t.Fatalf("append initial entries: %v", err)
	}
	if len(w.segments) != 3 {
		t.Fatalf("expected 3 segments after initial append, got %d", len(w.segments))
	}

	segment1Before, err := os.ReadFile(filepath.Join(dir, "00000000000000000001.wal"))
	if err != nil {
		t.Fatalf("read segment1 before rewrite: %v", err)
	}
	segment2Before, err := os.ReadFile(filepath.Join(dir, "00000000000000000002.wal"))
	if err != nil {
		t.Fatalf("read segment2 before rewrite: %v", err)
	}

	override := []raftpb.Entry{
		newEntry(3, 2, "entry-3-new"),
		newEntry(4, 2, "entry-4"),
	}
	if err := w.Append(ctx, raftpb.HardState{Term: 2, Commit: 4}, override); err != nil {
		t.Fatalf("append override entries: %v", err)
	}

	segment1After, err := os.ReadFile(filepath.Join(dir, "00000000000000000001.wal"))
	if err != nil {
		t.Fatalf("read segment1 after rewrite: %v", err)
	}
	segment2After, err := os.ReadFile(filepath.Join(dir, "00000000000000000002.wal"))
	if err != nil {
		t.Fatalf("read segment2 after rewrite: %v", err)
	}
	if !bytes.Equal(segment1Before, segment1After) {
		t.Fatalf("segment1 should remain unchanged during tail rewrite")
	}
	if !bytes.Equal(segment2Before, segment2After) {
		t.Fatalf("segment2 should remain unchanged during tail rewrite")
	}

	loadedState, loadedEntries, err := w.Load(ctx)
	if err != nil {
		t.Fatalf("load wal: %v", err)
	}
	if loadedState.Commit != 4 || loadedState.Term != 2 {
		t.Fatalf("unexpected hard state: %+v", loadedState)
	}
	if len(loadedEntries) != 4 {
		t.Fatalf("expected 4 loaded entries, got %d", len(loadedEntries))
	}
	if loadedEntries[2].Term != 2 || string(loadedEntries[2].Data) != "entry-3-new" {
		t.Fatalf("entry 3 not rewritten correctly: %+v", loadedEntries[2])
	}
	if loadedEntries[3].Index != 4 {
		t.Fatalf("expected final entry index 4, got %d", loadedEntries[3].Index)
	}
}

func TestFileWALTruncatePrefixRecyclesSegmentsIncrementally(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	w := NewFileWAL(dir)

	entries := []raftpb.Entry{
		newEntry(1, 1, "entry-aa"),
		newEntry(2, 1, "entry-bb"),
		newEntry(3, 1, "entry-cc"),
		newEntry(4, 1, "entry-dd"),
		newEntry(5, 1, "entry-ee"),
	}
	w.maxSegmentSize = twoEntrySegmentSize(t, entries[0], entries[1])

	if err := w.Open(ctx); err != nil {
		t.Fatalf("open wal: %v", err)
	}
	if err := w.Append(ctx, raftpb.HardState{Term: 1, Commit: 5}, entries); err != nil {
		t.Fatalf("append entries: %v", err)
	}
	if len(w.segments) != 3 {
		t.Fatalf("expected 3 segments before truncate, got %d", len(w.segments))
	}

	segment2Path := filepath.Join(dir, "00000000000000000002.wal")
	segment3Path := filepath.Join(dir, "00000000000000000003.wal")
	segment2Before, err := os.ReadFile(segment2Path)
	if err != nil {
		t.Fatalf("read segment2 before truncate: %v", err)
	}
	segment3Before, err := os.ReadFile(segment3Path)
	if err != nil {
		t.Fatalf("read segment3 before truncate: %v", err)
	}

	if err := w.TruncatePrefix(ctx, 4); err != nil {
		t.Fatalf("truncate prefix: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "00000000000000000001.wal")); !os.IsNotExist(err) {
		t.Fatalf("segment1 should be removed after truncate, stat err=%v", err)
	}
	segment2After, err := os.ReadFile(segment2Path)
	if err != nil {
		t.Fatalf("read segment2 after truncate: %v", err)
	}
	segment3After, err := os.ReadFile(segment3Path)
	if err != nil {
		t.Fatalf("read segment3 after truncate: %v", err)
	}
	if bytes.Equal(segment2Before, segment2After) {
		t.Fatalf("boundary segment should be rewritten after truncate")
	}
	if !bytes.Equal(segment3Before, segment3After) {
		t.Fatalf("unaffected tail segment should remain unchanged after truncate")
	}

	if len(w.segments) != 2 {
		t.Fatalf("expected 2 segments after truncate, got %d", len(w.segments))
	}
	if w.segments[0].ID != 2 || w.segments[0].FirstIndex != 4 || w.segments[0].LastIndex != 4 {
		t.Fatalf("unexpected boundary segment metadata after truncate: %+v", w.segments[0])
	}
	if w.segments[1].ID != 3 || w.segments[1].FirstIndex != 5 || w.segments[1].LastIndex != 5 {
		t.Fatalf("unexpected tail segment metadata after truncate: %+v", w.segments[1])
	}

	loadedState, loadedEntries, err := w.Load(ctx)
	if err != nil {
		t.Fatalf("load wal after truncate: %v", err)
	}
	if loadedState.Commit != 5 {
		t.Fatalf("unexpected hard state commit after truncate: %+v", loadedState)
	}
	if len(loadedEntries) != 2 {
		t.Fatalf("expected 2 loaded entries after truncate, got %d", len(loadedEntries))
	}
	if loadedEntries[0].Index != 4 || loadedEntries[1].Index != 5 {
		t.Fatalf("unexpected loaded entries after truncate: %+v", loadedEntries)
	}
}

func newEntry(index, term uint64, data string) raftpb.Entry {
	return raftpb.Entry{
		Index: index,
		Term:  term,
		Data:  []byte(data),
	}
}

func twoEntrySegmentSize(t *testing.T, entries ...raftpb.Entry) int64 {
	t.Helper()

	var size int64
	codec := ProtobufCodec{}
	for _, entry := range entries {
		record, err := codec.EncodeEntry(entry)
		if err != nil {
			t.Fatalf("encode entry: %v", err)
		}
		size += int64(4 + len(record))
	}

	return size
}
