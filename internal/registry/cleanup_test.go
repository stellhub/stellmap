package registry

import (
	"bytes"
	"testing"

	"github.com/chenwenlong-java/StarMap/internal/storage"
)

func TestCleanupCursorNextAndAdvance(t *testing.T) {
	defaultStart := []byte(RootPrefix)

	var cursor CleanupCursor
	if !bytes.Equal(cursor.Next(defaultStart), defaultStart) {
		t.Fatalf("expected default scan start")
	}

	items := []storage.KV{
		{Key: []byte("/registry/prod/payment/a")},
		{Key: []byte("/registry/prod/payment/b")},
	}
	cursor.Advance(defaultStart, items, 2)

	expected := append([]byte("/registry/prod/payment/b"), 0)
	if !bytes.Equal(cursor.Next(defaultStart), expected) {
		t.Fatalf("expected next scan start %q, got %q", string(expected), string(cursor.Next(defaultStart)))
	}

	cursor.Advance(defaultStart, items[:1], 2)
	if !bytes.Equal(cursor.Next(defaultStart), defaultStart) {
		t.Fatalf("expected cursor reset to default start")
	}
}
