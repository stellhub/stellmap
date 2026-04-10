package wal

import (
	"context"
	"errors"
	"testing"

	raftpb "go.etcd.io/raft/v3/raftpb"
)

func TestProtobufCodecRoundTrip(t *testing.T) {
	codec := ProtobufCodec{}
	state := raftpb.HardState{Term: 2, Vote: 1, Commit: 5}
	entry := raftpb.Entry{Index: 3, Term: 2, Data: []byte("entry-3")}

	stateData, err := codec.EncodeState(state)
	if err != nil {
		t.Fatalf("encode state failed: %v", err)
	}
	decodedState, err := codec.DecodeState(stateData)
	if err != nil {
		t.Fatalf("decode state failed: %v", err)
	}
	if decodedState != state {
		t.Fatalf("unexpected decoded state: %+v", decodedState)
	}

	entryData, err := codec.EncodeEntry(entry)
	if err != nil {
		t.Fatalf("encode entry failed: %v", err)
	}
	decodedEntry, err := codec.DecodeEntry(entryData)
	if err != nil {
		t.Fatalf("decode entry failed: %v", err)
	}
	if decodedEntry.Index != entry.Index || decodedEntry.Term != entry.Term || string(decodedEntry.Data) != "entry-3" {
		t.Fatalf("unexpected decoded entry: %+v", decodedEntry)
	}
}

func TestMemoryWALAppendLoadAndTruncate(t *testing.T) {
	ctx := context.Background()
	w := NewMemoryWAL()
	if err := w.Open(ctx); err != nil {
		t.Fatalf("open memory wal failed: %v", err)
	}

	if err := w.Append(ctx, raftpb.HardState{Term: 1, Commit: 2}, []raftpb.Entry{
		newEntry(1, 1, "one"),
		newEntry(2, 1, "two"),
	}); err != nil {
		t.Fatalf("append initial entries failed: %v", err)
	}
	if err := w.Append(ctx, raftpb.HardState{Term: 2, Commit: 3}, []raftpb.Entry{
		newEntry(2, 2, "two-new"),
		newEntry(3, 2, "three"),
	}); err != nil {
		t.Fatalf("append override entries failed: %v", err)
	}

	state, entries, err := w.Load(ctx)
	if err != nil {
		t.Fatalf("load memory wal failed: %v", err)
	}
	if state.Term != 2 || state.Commit != 3 {
		t.Fatalf("unexpected hard state: %+v", state)
	}
	if len(entries) != 3 || string(entries[1].Data) != "two-new" || entries[2].Index != 3 {
		t.Fatalf("unexpected entries: %+v", entries)
	}

	if err := w.TruncatePrefix(ctx, 3); err != nil {
		t.Fatalf("truncate prefix failed: %v", err)
	}
	_, entries, err = w.Load(ctx)
	if err != nil {
		t.Fatalf("load after truncate failed: %v", err)
	}
	if len(entries) != 1 || entries[0].Index != 3 {
		t.Fatalf("unexpected truncated entries: %+v", entries)
	}

	if err := w.Close(ctx); err != nil {
		t.Fatalf("close memory wal failed: %v", err)
	}
}

func TestFileWALRequiresOpenAndCanReopen(t *testing.T) {
	ctx := context.Background()
	w := NewFileWAL(t.TempDir())

	if _, _, err := w.Load(ctx); !errors.Is(err, errWALNotOpen) {
		t.Fatalf("expected load to require open, got %v", err)
	}
	if err := w.Append(ctx, raftpb.HardState{}, nil); !errors.Is(err, errWALNotOpen) {
		t.Fatalf("expected append to require open, got %v", err)
	}
	if err := w.TruncatePrefix(ctx, 1); !errors.Is(err, errWALNotOpen) {
		t.Fatalf("expected truncate to require open, got %v", err)
	}
	if err := w.Sync(ctx); !errors.Is(err, errWALNotOpen) {
		t.Fatalf("expected sync to require open, got %v", err)
	}

	if err := w.Open(ctx); err != nil {
		t.Fatalf("open file wal failed: %v", err)
	}
	if err := w.Append(ctx, raftpb.HardState{Term: 3, Commit: 2}, []raftpb.Entry{
		newEntry(1, 3, "one"),
		newEntry(2, 3, "two"),
	}); err != nil {
		t.Fatalf("append file wal entries failed: %v", err)
	}
	if err := w.Close(ctx); err != nil {
		t.Fatalf("close file wal failed: %v", err)
	}

	reopened := NewFileWAL(w.dir)
	if err := reopened.Open(ctx); err != nil {
		t.Fatalf("reopen file wal failed: %v", err)
	}
	state, entries, err := reopened.Load(ctx)
	if err != nil {
		t.Fatalf("load reopened wal failed: %v", err)
	}
	if state.Term != 3 || state.Commit != 2 || len(entries) != 2 || entries[1].Index != 2 {
		t.Fatalf("unexpected reopened state=%+v entries=%+v", state, entries)
	}
}

func TestMergeEntriesAndHelpers(t *testing.T) {
	merged := mergeEntries([]raftpb.Entry{
		newEntry(1, 1, "one"),
		newEntry(2, 1, "two"),
	}, []raftpb.Entry{
		newEntry(2, 2, "two-new"),
		newEntry(3, 2, "three"),
	})
	if len(merged) != 3 || string(merged[1].Data) != "two-new" || merged[2].Index != 3 {
		t.Fatalf("unexpected merged entries: %+v", merged)
	}

	if nextSegmentID(nil) != 1 || nextSegmentID([]Segment{{ID: 7}}) != 8 {
		t.Fatalf("unexpected next segment id")
	}
	if !isEmptyHardState(raftpb.HardState{}) || isEmptyHardState(raftpb.HardState{Term: 1}) {
		t.Fatalf("unexpected empty hard state detection")
	}
}
