package storage

import (
	"bytes"
	"context"
	"testing"
)

func TestMemoryStateMachineApplyScanSnapshotAndRestore(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStateMachine()

	if _, err := store.Apply(ctx, Command{Operation: OperationPut, Key: []byte("/a"), Value: []byte("1")}); err != nil {
		t.Fatalf("put /a failed: %v", err)
	}
	if _, err := store.Apply(ctx, Command{Operation: OperationPut, Key: []byte("/b"), Value: []byte("2")}); err != nil {
		t.Fatalf("put /b failed: %v", err)
	}
	if _, err := store.Apply(ctx, Command{Operation: OperationDeletePrefix, Prefix: []byte("/b")}); err != nil {
		t.Fatalf("delete prefix /b failed: %v", err)
	}

	value, err := store.Get(ctx, []byte("/a"))
	if err != nil || string(value) != "1" {
		t.Fatalf("unexpected get /a result value=%q err=%v", string(value), err)
	}
	value, err = store.Get(ctx, []byte("/b"))
	if err != nil || len(value) != 0 {
		t.Fatalf("expected /b deleted, value=%q err=%v", string(value), err)
	}

	items, err := store.Scan(ctx, []byte("/"), []byte("/z"), 10)
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if len(items) != 1 || string(items[0].Key) != "/a" {
		t.Fatalf("unexpected scan result: %+v", items)
	}

	var snapshot bytes.Buffer
	if err := store.Snapshot(ctx, &snapshot); err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}

	restored := NewMemoryStateMachine()
	if err := restored.Restore(ctx, bytes.NewReader(snapshot.Bytes())); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	restoredValue, err := restored.Get(ctx, []byte("/a"))
	if err != nil || string(restoredValue) != "1" {
		t.Fatalf("unexpected restored value=%q err=%v", string(restoredValue), err)
	}

	appliedIndex, err := store.AppliedIndex(ctx)
	if err != nil || appliedIndex != 3 {
		t.Fatalf("unexpected applied index=%d err=%v", appliedIndex, err)
	}
}

func TestHasPrefix(t *testing.T) {
	if !hasPrefix([]byte("abcdef"), []byte("abc")) {
		t.Fatalf("expected prefix match")
	}
	if hasPrefix([]byte("ab"), []byte("abc")) {
		t.Fatalf("expected prefix mismatch")
	}
	if hasPrefix([]byte("abcdef"), []byte("abd")) {
		t.Fatalf("expected prefix mismatch")
	}
}
