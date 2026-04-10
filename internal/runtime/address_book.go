package runtime

import "sync"

// AddressBook 维护节点 ID 与 HTTP/gRPC/admin 地址映射。
type AddressBook struct {
	mu        sync.RWMutex
	httpByID  map[uint64]string
	grpcByID  map[uint64]string
	adminByID map[uint64]string
}

// NewAddressBook 根据已知地址映射构造一个新的地址簿。
func NewAddressBook(httpAddrs, grpcAddrs, adminAddrs map[uint64]string) *AddressBook {
	book := &AddressBook{
		httpByID:  make(map[uint64]string, len(httpAddrs)),
		grpcByID:  make(map[uint64]string, len(grpcAddrs)),
		adminByID: make(map[uint64]string, len(adminAddrs)),
	}
	for id, addr := range httpAddrs {
		book.httpByID[id] = addr
	}
	for id, addr := range grpcAddrs {
		book.grpcByID[id] = addr
	}
	for id, addr := range adminAddrs {
		book.adminByID[id] = addr
	}

	return book
}

// HTTPAddr 返回指定节点的 HTTP 地址。
func (b *AddressBook) HTTPAddr(id uint64) string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.httpByID[id]
}

// GRPCAddr 返回指定节点的 gRPC 地址。
func (b *AddressBook) GRPCAddr(id uint64) string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.grpcByID[id]
}

// AdminAddr 返回指定节点的 admin HTTP 地址。
func (b *AddressBook) AdminAddr(id uint64) string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.adminByID[id]
}

// Set 更新或写入一个节点的地址。
func (b *AddressBook) Set(id uint64, httpAddr, grpcAddr, adminAddr string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if httpAddr != "" {
		b.httpByID[id] = httpAddr
	}
	if grpcAddr != "" {
		b.grpcByID[id] = grpcAddr
	}
	if adminAddr != "" {
		b.adminByID[id] = adminAddr
	}
}

// Delete 删除一个节点的地址记录。
func (b *AddressBook) Delete(id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.httpByID, id)
	delete(b.grpcByID, id)
	delete(b.adminByID, id)
}

// SnapshotHTTP 返回当前 HTTP 地址映射的拷贝。
func (b *AddressBook) SnapshotHTTP() map[uint64]string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	copied := make(map[uint64]string, len(b.httpByID))
	for id, addr := range b.httpByID {
		copied[id] = addr
	}
	return copied
}

// SnapshotGRPC 返回当前 gRPC 地址映射的拷贝。
func (b *AddressBook) SnapshotGRPC() map[uint64]string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	copied := make(map[uint64]string, len(b.grpcByID))
	for id, addr := range b.grpcByID {
		copied[id] = addr
	}
	return copied
}

// SnapshotAdmin 返回当前 admin 地址映射的拷贝。
func (b *AddressBook) SnapshotAdmin() map[uint64]string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	copied := make(map[uint64]string, len(b.adminByID))
	for id, addr := range b.adminByID {
		copied[id] = addr
	}
	return copied
}
