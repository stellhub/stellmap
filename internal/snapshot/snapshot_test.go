package snapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"
)

func TestFileStoreCreateOpenLatestAndCleanup(t *testing.T) {
	ctx := context.Background()
	store := NewFileStore(t.TempDir())

	firstData := []byte("first-snapshot")
	firstMeta, err := store.Create(ctx, Metadata{Term: 1, Index: 10, ConfState: []byte("c1")}, func(w io.Writer) error {
		_, writeErr := w.Write(firstData)
		return writeErr
	})
	if err != nil {
		t.Fatalf("create first snapshot failed: %v", err)
	}
	if firstMeta.FileSize != uint64(len(firstData)) {
		t.Fatalf("unexpected first file size: %d", firstMeta.FileSize)
	}
	expectedChecksum := sha256.Sum256(firstData)
	if firstMeta.Checksum != hex.EncodeToString(expectedChecksum[:]) {
		t.Fatalf("unexpected first checksum: %s", firstMeta.Checksum)
	}

	if _, err := store.Create(ctx, Metadata{Term: 1, Index: 11}, func(w io.Writer) error {
		_, writeErr := w.Write([]byte("second"))
		return writeErr
	}); err != nil {
		t.Fatalf("create second snapshot failed: %v", err)
	}
	latestMeta, err := store.Create(ctx, Metadata{Term: 2, Index: 12}, func(w io.Writer) error {
		_, writeErr := w.Write([]byte("third"))
		return writeErr
	})
	if err != nil {
		t.Fatalf("create third snapshot failed: %v", err)
	}

	metas, err := store.listMetadata()
	if err != nil {
		t.Fatalf("list metadata failed: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("expected cleanup to keep 2 snapshots, got %d", len(metas))
	}

	meta, reader, err := store.OpenLatest(ctx)
	if err != nil {
		t.Fatalf("open latest snapshot failed: %v", err)
	}
	defer reader.Close()

	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read latest snapshot failed: %v", err)
	}
	if meta.Term != latestMeta.Term || meta.Index != latestMeta.Index || string(body) != "third" {
		t.Fatalf("unexpected latest snapshot meta=%+v body=%q", meta, string(body))
	}
}

func TestFileStoreInstallAndOpenLatest(t *testing.T) {
	ctx := context.Background()
	store := NewFileStore(t.TempDir())

	if err := store.Install(ctx, Metadata{Term: 3, Index: 20, ConfState: []byte("state")}, bytes.NewReader([]byte("installed"))); err != nil {
		t.Fatalf("install snapshot failed: %v", err)
	}

	meta, reader, err := store.OpenLatest(ctx)
	if err != nil {
		t.Fatalf("open installed snapshot failed: %v", err)
	}
	defer reader.Close()

	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read installed snapshot failed: %v", err)
	}
	if meta.Term != 3 || meta.Index != 20 || meta.FileSize != uint64(len("installed")) || string(body) != "installed" {
		t.Fatalf("unexpected installed snapshot meta=%+v body=%q", meta, string(body))
	}
}

func TestFileStoreOpenLatestReturnsNotFoundOnEmptyStore(t *testing.T) {
	_, _, err := NewFileStore(t.TempDir()).OpenLatest(context.Background())
	if !errors.Is(err, errSnapshotNotFound) {
		t.Fatalf("expected snapshot not found, got %v", err)
	}
}

func TestMemoryStoreCreateInstallAndOpenLatest(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	meta, err := store.Create(ctx, Metadata{Term: 1, Index: 2}, func(w io.Writer) error {
		_, writeErr := w.Write([]byte("memory"))
		return writeErr
	})
	if err != nil {
		t.Fatalf("create memory snapshot failed: %v", err)
	}
	if meta.FileSize != uint64(len("memory")) {
		t.Fatalf("unexpected memory snapshot size: %d", meta.FileSize)
	}

	if err := store.Install(ctx, Metadata{Term: 2, Index: 3}, bytes.NewReader([]byte("replaced"))); err != nil {
		t.Fatalf("install memory snapshot failed: %v", err)
	}

	latest, reader, err := store.OpenLatest(ctx)
	if err != nil {
		t.Fatalf("open latest memory snapshot failed: %v", err)
	}
	defer reader.Close()

	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read memory snapshot failed: %v", err)
	}
	if latest.Term != 2 || latest.Index != 3 || string(body) != "replaced" {
		t.Fatalf("unexpected memory snapshot latest=%+v body=%q", latest, string(body))
	}
}

func TestNopReaderAndWriter(t *testing.T) {
	var buf bytes.Buffer
	writer := NewWriter(&buf)
	if _, err := writer.WriteChunk([]byte("abc")); err != nil {
		t.Fatalf("write chunk failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	reader := NewReader(bytes.NewReader(buf.Bytes()))
	data := make([]byte, 3)
	n, err := reader.ReadChunk(data)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("read chunk failed: %v", err)
	}
	if n != 3 || string(data) != "abc" {
		t.Fatalf("unexpected reader output: n=%d data=%q", n, string(data))
	}
}
