package raftnode

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stellhub/stellmap/internal/snapshot"
	"github.com/stellhub/stellmap/internal/storage"
	"github.com/stellhub/stellmap/internal/wal"
)

func TestRaftNodeRestartAfterSnapshotAndCompaction(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := t.TempDir()
	cfg := Config{
		NodeID:               1,
		ClusterID:            100,
		DataDir:              dataDir,
		TickInterval:         10 * time.Millisecond,
		ElectionTick:         3,
		HeartbeatTick:        1,
		SnapshotEntries:      3,
		CompactRetainEntries: 1,
		KeepSnapshots:        2,
	}

	node, err := New(cfg)
	if err != nil {
		t.Fatalf("new node: %v", err)
	}
	if err := node.Start(ctx); err != nil {
		t.Fatalf("start node: %v", err)
	}
	waitForLeader(t, node, 2*time.Second)

	for i := 1; i <= 6; i++ {
		proposePut(t, node, fmt.Sprintf("/svc/%d", i), fmt.Sprintf("value-%d", i))
	}
	waitForAppliedAtLeast(t, node, 6, 2*time.Second)

	if err := node.Stop(ctx); err != nil {
		t.Fatalf("stop node: %v", err)
	}

	snapStore := snapshot.NewFileStore(filepath.Join(dataDir, "snapshot"))
	meta, reader, err := snapStore.OpenLatest(ctx)
	if err != nil {
		t.Fatalf("open latest snapshot: %v", err)
	}
	_ = reader.Close()
	if meta.Index == 0 {
		t.Fatalf("expected snapshot to be created")
	}

	walStore := wal.NewFileWAL(filepath.Join(dataDir, "wal"))
	if err := walStore.Open(ctx); err != nil {
		t.Fatalf("open wal: %v", err)
	}
	hardState, entries, err := walStore.Load(ctx)
	if err != nil {
		t.Fatalf("load wal: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected retained wal entries after compaction")
	}
	if entries[0].Index <= 1 {
		t.Fatalf("expected wal to be compacted, first retained index=%d", entries[0].Index)
	}
	if hardState.Commit < meta.Index {
		t.Fatalf("hard state commit=%d should not be behind snapshot index=%d", hardState.Commit, meta.Index)
	}
	if err := walStore.Close(ctx); err != nil {
		t.Fatalf("close wal: %v", err)
	}

	restarted, err := New(cfg)
	if err != nil {
		t.Fatalf("new restarted node: %v", err)
	}
	if err := restarted.Start(ctx); err != nil {
		t.Fatalf("start restarted node: %v", err)
	}
	waitForLeader(t, restarted, 2*time.Second)

	before := restarted.Status().AppliedIndex
	proposePut(t, restarted, "/svc/restarted", "value-restarted")
	waitForAppliedAtLeast(t, restarted, before+1, 2*time.Second)

	if err := restarted.Stop(ctx); err != nil {
		t.Fatalf("stop restarted node: %v", err)
	}

	store := storage.NewPebbleStore(filepath.Join(dataDir, "pebble"))
	if err := store.Open(ctx); err != nil {
		t.Fatalf("open pebble store: %v", err)
	}
	defer func() {
		if err := store.Close(ctx); err != nil {
			t.Fatalf("close pebble store: %v", err)
		}
	}()

	for i := 1; i <= 6; i++ {
		key := []byte(fmt.Sprintf("/svc/%d", i))
		expected := []byte(fmt.Sprintf("value-%d", i))
		actual, err := store.Get(ctx, key)
		if err != nil {
			t.Fatalf("get key %q: %v", key, err)
		}
		if string(actual) != string(expected) {
			t.Fatalf("key %q = %q, want %q", key, actual, expected)
		}
	}
	actual, err := store.Get(ctx, []byte("/svc/restarted"))
	if err != nil {
		t.Fatalf("get restarted key: %v", err)
	}
	if string(actual) != "value-restarted" {
		t.Fatalf("restarted key = %q, want %q", actual, "value-restarted")
	}
}

func TestRaftNodeRestartContinuesSnapshotAndCompaction(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := t.TempDir()
	cfg := Config{
		NodeID:               1,
		ClusterID:            101,
		DataDir:              dataDir,
		TickInterval:         10 * time.Millisecond,
		ElectionTick:         3,
		HeartbeatTick:        1,
		SnapshotEntries:      3,
		CompactRetainEntries: 1,
		KeepSnapshots:        3,
	}

	node, err := New(cfg)
	if err != nil {
		t.Fatalf("new node: %v", err)
	}
	if err := node.Start(ctx); err != nil {
		t.Fatalf("start node: %v", err)
	}
	waitForLeader(t, node, 2*time.Second)

	for i := 1; i <= 6; i++ {
		proposePut(t, node, fmt.Sprintf("/svc/phase1/%d", i), fmt.Sprintf("value-phase1-%d", i))
	}
	waitForAppliedAtLeast(t, node, 6, 2*time.Second)
	firstSnapshot := waitForLatestSnapshotAtLeast(t, dataDir, 6, 2*time.Second)

	if err := node.Stop(ctx); err != nil {
		t.Fatalf("stop node: %v", err)
	}

	firstWALFirstIndex := firstRetainedWALIndex(t, dataDir)
	if firstWALFirstIndex == 0 {
		t.Fatalf("expected retained wal entries after first compaction")
	}

	restarted, err := New(cfg)
	if err != nil {
		t.Fatalf("new restarted node: %v", err)
	}
	if err := restarted.Start(ctx); err != nil {
		t.Fatalf("start restarted node: %v", err)
	}
	waitForLeader(t, restarted, 2*time.Second)

	before := restarted.Status().AppliedIndex
	for i := 7; i <= 10; i++ {
		proposePut(t, restarted, fmt.Sprintf("/svc/phase2/%d", i), fmt.Sprintf("value-phase2-%d", i))
	}
	waitForAppliedAtLeast(t, restarted, before+4, 2*time.Second)
	secondSnapshot := waitForLatestSnapshotAtLeast(t, dataDir, firstSnapshot.Index+3, 2*time.Second)

	if err := restarted.Stop(ctx); err != nil {
		t.Fatalf("stop restarted node: %v", err)
	}

	secondWALFirstIndex := firstRetainedWALIndex(t, dataDir)
	if secondSnapshot.Index <= firstSnapshot.Index {
		t.Fatalf("snapshot did not advance after restart: first=%d second=%d", firstSnapshot.Index, secondSnapshot.Index)
	}
	if secondWALFirstIndex <= firstWALFirstIndex {
		t.Fatalf("wal compaction did not advance after restart: first=%d second=%d", firstWALFirstIndex, secondWALFirstIndex)
	}

	store := storage.NewPebbleStore(filepath.Join(dataDir, "pebble"))
	if err := store.Open(ctx); err != nil {
		t.Fatalf("open pebble store: %v", err)
	}
	defer func() {
		if err := store.Close(ctx); err != nil {
			t.Fatalf("close pebble store: %v", err)
		}
	}()

	for i := 1; i <= 6; i++ {
		key := []byte(fmt.Sprintf("/svc/phase1/%d", i))
		expected := []byte(fmt.Sprintf("value-phase1-%d", i))
		assertPebbleValue(t, ctx, store, key, expected)
	}
	for i := 7; i <= 10; i++ {
		key := []byte(fmt.Sprintf("/svc/phase2/%d", i))
		expected := []byte(fmt.Sprintf("value-phase2-%d", i))
		assertPebbleValue(t, ctx, store, key, expected)
	}
}

func proposePut(t *testing.T, node *RaftNode, key, value string) {
	t.Helper()

	data, err := json.Marshal(storage.Command{
		Operation: storage.OperationPut,
		Key:       []byte(key),
		Value:     []byte(value),
	})
	if err != nil {
		t.Fatalf("marshal command: %v", err)
	}
	if err := node.Propose(context.Background(), data); err != nil {
		t.Fatalf("propose command: %v", err)
	}
}

func waitForLeader(t *testing.T, node *RaftNode, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status := node.Status()
		if status.Role == RoleLeader && status.LeaderID == status.NodeID {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("node did not become leader in %s, status=%+v", timeout, node.Status())
}

func waitForAppliedAtLeast(t *testing.T, node *RaftNode, expected uint64, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if node.Status().AppliedIndex >= expected {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("applied index did not reach %d in %s, status=%+v", expected, timeout, node.Status())
}

func waitForLatestSnapshotAtLeast(t *testing.T, dataDir string, expected uint64, timeout time.Duration) snapshot.Metadata {
	t.Helper()

	ctx := context.Background()
	store := snapshot.NewFileStore(filepath.Join(dataDir, "snapshot"))
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		meta, reader, err := store.OpenLatest(ctx)
		if err == nil {
			_ = reader.Close()
			if meta.Index >= expected {
				return meta
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("snapshot index did not reach %d in %s", expected, timeout)
	return snapshot.Metadata{}
}

func firstRetainedWALIndex(t *testing.T, dataDir string) uint64 {
	t.Helper()

	ctx := context.Background()
	walStore := wal.NewFileWAL(filepath.Join(dataDir, "wal"))
	if err := walStore.Open(ctx); err != nil {
		t.Fatalf("open wal: %v", err)
	}
	defer func() {
		if err := walStore.Close(ctx); err != nil {
			t.Fatalf("close wal: %v", err)
		}
	}()

	_, entries, err := walStore.Load(ctx)
	if err != nil {
		t.Fatalf("load wal: %v", err)
	}
	if len(entries) == 0 {
		return 0
	}

	return entries[0].Index
}

func assertPebbleValue(t *testing.T, ctx context.Context, store *storage.PebbleStore, key, expected []byte) {
	t.Helper()

	actual, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("get key %q: %v", key, err)
	}
	if string(actual) != string(expected) {
		t.Fatalf("key %q = %q, want %q", key, actual, expected)
	}
}
