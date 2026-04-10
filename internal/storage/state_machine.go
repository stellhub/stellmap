package storage

import (
	"context"
	"encoding/json"
	"io"
	"sort"
	"sync"
)

// Operation 描述状态机操作类型。
type Operation string

const (
	// OperationPut 表示写入/覆盖。
	OperationPut Operation = "put"
	// OperationDelete 表示删除单 key。
	OperationDelete Operation = "delete"
	// OperationDeletePrefix 表示按前缀删除。
	OperationDeletePrefix Operation = "delete_prefix"
)

// Command 描述一条状态机命令。
type Command struct {
	Operation Operation `json:"operation"`
	Key       []byte    `json:"key,omitempty"`
	Value     []byte    `json:"value,omitempty"`
	Prefix    []byte    `json:"prefix,omitempty"`
}

// Result 描述一次状态机应用结果。
type Result struct {
	Modified int
}

// KV 表示一条 KV 记录。
type KV struct {
	Key   []byte `json:"key"`
	Value []byte `json:"value"`
}

// MemberAddress 表示一个集群成员的公共 HTTP、内部 gRPC 与管理面地址。
type MemberAddress struct {
	NodeID    uint64 `json:"nodeId"`
	HTTPAddr  string `json:"httpAddr,omitempty"`
	GRPCAddr  string `json:"grpcAddr,omitempty"`
	AdminAddr string `json:"adminAddr,omitempty"`
}

// StateMachine 定义最小状态机接口。
type StateMachine interface {
	Apply(ctx context.Context, cmd Command) (Result, error)
	Get(ctx context.Context, key []byte) ([]byte, error)
	Scan(ctx context.Context, start, end []byte, limit int) ([]KV, error)
	Snapshot(ctx context.Context, w io.Writer) error
	Restore(ctx context.Context, r io.Reader) error
	AppliedIndex(ctx context.Context) (uint64, error)
}

// MemoryStateMachine 是最小内存版状态机实现。
type MemoryStateMachine struct {
	mu           sync.RWMutex
	data         map[string][]byte
	appliedIndex uint64
}

// NewMemoryStateMachine 创建一个内存版状态机。
func NewMemoryStateMachine() *MemoryStateMachine {
	return &MemoryStateMachine{
		data: make(map[string][]byte),
	}
}

// Apply 应用一条状态机命令。
func (m *MemoryStateMachine) Apply(ctx context.Context, cmd Command) (Result, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()

	result := Result{}

	switch cmd.Operation {
	case OperationPut:
		m.data[string(cmd.Key)] = append([]byte(nil), cmd.Value...)
		result.Modified = 1
	case OperationDelete:
		if _, ok := m.data[string(cmd.Key)]; ok {
			delete(m.data, string(cmd.Key))
			result.Modified = 1
		}
	case OperationDeletePrefix:
		for key := range m.data {
			if len(cmd.Prefix) == 0 || hasPrefix([]byte(key), cmd.Prefix) {
				delete(m.data, key)
				result.Modified++
			}
		}
	}

	m.appliedIndex++

	return result, nil
}

// Get 查询单 key。
func (m *MemoryStateMachine) Get(ctx context.Context, key []byte) ([]byte, error) {
	_ = ctx
	m.mu.RLock()
	defer m.mu.RUnlock()

	value := append([]byte(nil), m.data[string(key)]...)
	return value, nil
}

// Scan 按键空间扫描。
func (m *MemoryStateMachine) Scan(ctx context.Context, start, end []byte, limit int) ([]KV, error) {
	_ = ctx
	m.mu.RLock()
	defer m.mu.RUnlock()

	keys := make([]string, 0, len(m.data))
	for key := range m.data {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	items := make([]KV, 0, len(keys))
	for _, key := range keys {
		raw := []byte(key)
		if len(start) > 0 && string(raw) < string(start) {
			continue
		}
		if len(end) > 0 && string(raw) >= string(end) {
			continue
		}
		items = append(items, KV{
			Key:   append([]byte(nil), raw...),
			Value: append([]byte(nil), m.data[key]...),
		})
		if limit > 0 && len(items) >= limit {
			break
		}
	}

	return items, nil
}

// Snapshot 导出状态机快照。
func (m *MemoryStateMachine) Snapshot(ctx context.Context, w io.Writer) error {
	_ = ctx
	m.mu.RLock()
	defer m.mu.RUnlock()

	encoder := json.NewEncoder(w)
	return encoder.Encode(m.data)
}

// Restore 从快照恢复状态机。
func (m *MemoryStateMachine) Restore(ctx context.Context, r io.Reader) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()

	decoder := json.NewDecoder(r)
	return decoder.Decode(&m.data)
}

// AppliedIndex 返回已应用索引。
func (m *MemoryStateMachine) AppliedIndex(ctx context.Context) (uint64, error) {
	_ = ctx
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.appliedIndex, nil
}

func hasPrefix(key, prefix []byte) bool {
	if len(prefix) > len(key) {
		return false
	}

	for i := range prefix {
		if key[i] != prefix[i] {
			return false
		}
	}

	return true
}
