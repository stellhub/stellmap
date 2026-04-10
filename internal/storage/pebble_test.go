package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestPebbleStoreApplyGetScanAndMetadata(t *testing.T) {
	ctx := context.Background()
	store := NewPebbleStore(t.TempDir())
	if err := store.Open(ctx); err != nil {
		t.Fatalf("open pebble store failed: %v", err)
	}
	defer store.Close(ctx)

	if _, err := store.Apply(ctx, Command{Operation: OperationPut, Key: []byte("/svc/a"), Value: []byte("1")}); err != nil {
		t.Fatalf("put /svc/a failed: %v", err)
	}
	if _, err := store.Apply(ctx, Command{Operation: OperationPut, Key: []byte("/svc/b"), Value: []byte("2")}); err != nil {
		t.Fatalf("put /svc/b failed: %v", err)
	}
	if _, err := store.Apply(ctx, Command{Operation: OperationDeletePrefix, Prefix: []byte("/svc/b")}); err != nil {
		t.Fatalf("delete prefix failed: %v", err)
	}

	if err := store.SetAppliedIndex(ctx, 12); err != nil {
		t.Fatalf("set applied index failed: %v", err)
	}
	if err := store.SetAppliedTerm(ctx, 3); err != nil {
		t.Fatalf("set applied term failed: %v", err)
	}
	if err := store.SetMemberAddress(ctx, 2, "127.0.0.1:8081", "127.0.0.1:19091", "127.0.0.1:18081"); err != nil {
		t.Fatalf("set member address failed: %v", err)
	}

	value, err := store.Get(ctx, []byte("/svc/a"))
	if err != nil || string(value) != "1" {
		t.Fatalf("unexpected get result value=%q err=%v", string(value), err)
	}

	items, err := store.Scan(ctx, nil, nil, 10)
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if len(items) != 1 || string(items[0].Key) != "/svc/a" {
		t.Fatalf("unexpected scan items: %+v", items)
	}

	index, err := store.AppliedIndex(ctx)
	if err != nil || index != 12 {
		t.Fatalf("unexpected applied index=%d err=%v", index, err)
	}
	term, err := store.AppliedTerm(ctx)
	if err != nil || term != 3 {
		t.Fatalf("unexpected applied term=%d err=%v", term, err)
	}

	members, err := store.ListMemberAddresses(ctx)
	if err != nil {
		t.Fatalf("list member addresses failed: %v", err)
	}
	if len(members) != 1 || members[0].NodeID != 2 {
		t.Fatalf("unexpected members: %+v", members)
	}
}

func TestPebbleStoreSnapshotAndRestoreKeepsAppliedMetadata(t *testing.T) {
	ctx := context.Background()
	store := NewPebbleStore(t.TempDir())
	if err := store.Open(ctx); err != nil {
		t.Fatalf("open pebble store failed: %v", err)
	}
	defer store.Close(ctx)

	if _, err := store.Apply(ctx, Command{Operation: OperationPut, Key: []byte("/svc/a"), Value: []byte("1")}); err != nil {
		t.Fatalf("put /svc/a failed: %v", err)
	}
	if err := store.SetMemberAddress(ctx, 2, "127.0.0.1:8081", "127.0.0.1:19091", "127.0.0.1:18081"); err != nil {
		t.Fatalf("set member address failed: %v", err)
	}
	if err := store.SetAppliedIndex(ctx, 20); err != nil {
		t.Fatalf("set applied index failed: %v", err)
	}
	if err := store.SetAppliedTerm(ctx, 4); err != nil {
		t.Fatalf("set applied term failed: %v", err)
	}

	var snapshot bytes.Buffer
	if err := store.Snapshot(ctx, &snapshot); err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}

	if _, err := store.Apply(ctx, Command{Operation: OperationDelete, Key: []byte("/svc/a")}); err != nil {
		t.Fatalf("delete /svc/a failed: %v", err)
	}
	if err := store.DeleteMemberAddress(ctx, 2); err != nil {
		t.Fatalf("delete member address failed: %v", err)
	}

	if err := store.Restore(ctx, bytes.NewReader(snapshot.Bytes())); err != nil {
		t.Fatalf("restore failed: %v", err)
	}

	value, err := store.Get(ctx, []byte("/svc/a"))
	if err != nil || string(value) != "1" {
		t.Fatalf("unexpected restored value=%q err=%v", string(value), err)
	}
	members, err := store.ListMemberAddresses(ctx)
	if err != nil {
		t.Fatalf("list restored members failed: %v", err)
	}
	if len(members) != 1 || members[0].NodeID != 2 {
		t.Fatalf("unexpected restored members: %+v", members)
	}

	index, err := store.AppliedIndex(ctx)
	if err != nil || index != 20 {
		t.Fatalf("restore should keep applied index, got %d err=%v", index, err)
	}
	term, err := store.AppliedTerm(ctx)
	if err != nil || term != 4 {
		t.Fatalf("restore should keep applied term, got %d err=%v", term, err)
	}
}

func TestPebbleStoreDecodeLegacySnapshotPayload(t *testing.T) {
	raw, err := json.Marshal([]KV{
		{Key: []byte("/a"), Value: []byte("1")},
	})
	if err != nil {
		t.Fatalf("marshal legacy payload failed: %v", err)
	}

	payload, err := decodeSnapshotPayload(raw)
	if err != nil {
		t.Fatalf("decode legacy payload failed: %v", err)
	}
	if len(payload.Data) != 1 || string(payload.Data[0].Key) != "/a" {
		t.Fatalf("unexpected decoded payload: %+v", payload)
	}
}

func TestPebbleStoreRequiresOpenAndValidMemberID(t *testing.T) {
	ctx := context.Background()
	store := NewPebbleStore(t.TempDir())

	if _, err := store.Get(ctx, []byte("/a")); !errors.Is(err, errStoreNotOpen) {
		t.Fatalf("expected errStoreNotOpen, got %v", err)
	}

	if err := store.Open(ctx); err != nil {
		t.Fatalf("open pebble store failed: %v", err)
	}
	defer store.Close(ctx)

	if err := store.SetMemberAddress(ctx, 0, "", "", ""); err == nil {
		t.Fatalf("expected invalid member node id error")
	}
}
