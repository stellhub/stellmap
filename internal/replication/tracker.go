package replication

import (
	"sort"
	"sync"
	"time"
)

// Status 表示一个远端服务目录同步任务的当前状态快照。
type Status struct {
	SourceRegion         string `json:"sourceRegion"`
	SourceClusterID      string `json:"sourceClusterId"`
	Namespace            string `json:"namespace"`
	Service              string `json:"service"`
	Connected            bool   `json:"connected"`
	LastAppliedRevision  uint64 `json:"lastAppliedRevision"`
	LastSnapshotRevision uint64 `json:"lastSnapshotRevision"`
	LastSyncUnix         int64  `json:"lastSyncUnix"`
	ErrorCount           uint64 `json:"errorCount"`
	LastError            string `json:"lastError,omitempty"`
}

// Tracker 维护复制任务状态与指标。
type Tracker struct {
	mu     sync.RWMutex
	status map[string]Status
}

// NewTracker 创建一个新的复制状态跟踪器。
func NewTracker() *Tracker {
	return &Tracker{
		status: make(map[string]Status),
	}
}

func key(sourceRegion, sourceClusterID, namespace, service string) string {
	return sourceRegion + "\x00" + sourceClusterID + "\x00" + namespace + "\x00" + service
}

// SetConnected 更新某个复制任务的连接状态。
func (t *Tracker) SetConnected(sourceRegion, sourceClusterID, namespace, service string, connected bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	item := t.status[key(sourceRegion, sourceClusterID, namespace, service)]
	item.SourceRegion = sourceRegion
	item.SourceClusterID = sourceClusterID
	item.Namespace = namespace
	item.Service = service
	item.Connected = connected
	if connected {
		item.LastError = ""
		item.LastSyncUnix = time.Now().Unix()
	}
	t.status[key(sourceRegion, sourceClusterID, namespace, service)] = item
}

// UpdateRevision 更新某个复制任务的同步水位。
func (t *Tracker) UpdateRevision(sourceRegion, sourceClusterID, namespace, service string, appliedRevision, snapshotRevision uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	item := t.status[key(sourceRegion, sourceClusterID, namespace, service)]
	item.SourceRegion = sourceRegion
	item.SourceClusterID = sourceClusterID
	item.Namespace = namespace
	item.Service = service
	item.Connected = true
	if appliedRevision > item.LastAppliedRevision {
		item.LastAppliedRevision = appliedRevision
	}
	if snapshotRevision > 0 {
		item.LastSnapshotRevision = snapshotRevision
	}
	item.LastSyncUnix = time.Now().Unix()
	item.LastError = ""
	t.status[key(sourceRegion, sourceClusterID, namespace, service)] = item
}

// RecordError 记录一次复制错误。
func (t *Tracker) RecordError(sourceRegion, sourceClusterID, namespace, service, message string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	item := t.status[key(sourceRegion, sourceClusterID, namespace, service)]
	item.SourceRegion = sourceRegion
	item.SourceClusterID = sourceClusterID
	item.Namespace = namespace
	item.Service = service
	item.Connected = false
	item.ErrorCount++
	item.LastError = message
	item.LastSyncUnix = time.Now().Unix()
	t.status[key(sourceRegion, sourceClusterID, namespace, service)] = item
}

// List 返回复制状态快照列表。
func (t *Tracker) List() []Status {
	t.mu.RLock()
	defer t.mu.RUnlock()

	items := make([]Status, 0, len(t.status))
	for _, item := range t.status {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].SourceRegion != items[j].SourceRegion {
			return items[i].SourceRegion < items[j].SourceRegion
		}
		if items[i].SourceClusterID != items[j].SourceClusterID {
			return items[i].SourceClusterID < items[j].SourceClusterID
		}
		if items[i].Namespace != items[j].Namespace {
			return items[i].Namespace < items[j].Namespace
		}
		return items[i].Service < items[j].Service
	})
	return items
}
