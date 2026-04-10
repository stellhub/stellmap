package runtime

import "testing"

func TestAddressBookSetDeleteAndSnapshotsAreCopies(t *testing.T) {
	book := NewAddressBook(
		map[uint64]string{1: "127.0.0.1:8080"},
		map[uint64]string{1: "127.0.0.1:19090"},
		map[uint64]string{1: "127.0.0.1:18080"},
	)

	book.Set(2, "127.0.0.1:8081", "127.0.0.1:19091", "127.0.0.1:18081")
	if book.HTTPAddr(2) != "127.0.0.1:8081" || book.GRPCAddr(2) != "127.0.0.1:19091" || book.AdminAddr(2) != "127.0.0.1:18081" {
		t.Fatalf("unexpected address book values")
	}

	httpSnapshot := book.SnapshotHTTP()
	httpSnapshot[1] = "mutated"
	if book.HTTPAddr(1) != "127.0.0.1:8080" {
		t.Fatalf("snapshot should be a copy")
	}

	book.Delete(1)
	if book.HTTPAddr(1) != "" || book.GRPCAddr(1) != "" || book.AdminAddr(1) != "" {
		t.Fatalf("delete should remove all address kinds")
	}
}
