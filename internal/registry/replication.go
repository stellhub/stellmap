package registry

import (
	"fmt"
	"strings"
	"time"
)

const (
	// ReplicationRootPrefix 是跨 region 复制目录在状态机中的根前缀。
	ReplicationRootPrefix = "/replication/"
	// ReplicationRegistryPrefix 标识远端复制实例目录的路径段。
	ReplicationRegistryPrefix = "/replication/regions/"
	// ReplicationCheckpointPrefix 是每个远端服务复制水位的根前缀。
	ReplicationCheckpointPrefix = "/replication/checkpoints/"
	replicationRegistryMarker   = "/registry/"
)

// ReplicatedValue 表示从远端 region 异步复制到本地的实例目录视图。
type ReplicatedValue struct {
	Namespace         string            `json:"namespace"`
	Service           string            `json:"service"`
	InstanceID        string            `json:"instanceId"`
	Zone              string            `json:"zone,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
	Endpoints         []Endpoint        `json:"endpoints"`
	LeaseTTLSeconds   int64             `json:"leaseTtlSeconds"`
	RegisteredAtUnix  int64             `json:"registeredAtUnix"`
	LastHeartbeatUnix int64             `json:"lastHeartbeatUnix"`
	SourceRegion      string            `json:"sourceRegion"`
	SourceClusterID   string            `json:"sourceClusterId"`
	SourceRevision    uint64            `json:"sourceRevision"`
	ReplicatedAtUnix  int64             `json:"replicatedAtUnix"`
	Origin            string            `json:"origin"`
}

// ReplicationCheckpoint 表示某个远端服务目录同步到本地的最新水位。
//
// 第一阶段同步是按 namespace/service 维度独立 watch，因此 checkpoint 也按服务粒度保存。
type ReplicationCheckpoint struct {
	SourceRegion         string `json:"sourceRegion"`
	SourceClusterID      string `json:"sourceClusterId"`
	Namespace            string `json:"namespace"`
	Service              string `json:"service"`
	LastAppliedRevision  uint64 `json:"lastAppliedRevision"`
	LastSnapshotRevision uint64 `json:"lastSnapshotRevision"`
	UpdatedAtUnix        int64  `json:"updatedAtUnix"`
}

// ReplicationServicePrefix 返回某个远端服务复制目录的公共 key 前缀。
func ReplicationServicePrefix(sourceRegion, sourceClusterID, namespace, service string) string {
	return fmt.Sprintf("%s%s/clusters/%s/registry/%s/%s/", ReplicationRegistryPrefix, strings.TrimSpace(sourceRegion), strings.TrimSpace(sourceClusterID), strings.TrimSpace(namespace), strings.TrimSpace(service))
}

// ReplicatedKey 生成某个远端实例复制到本地后的状态机 key。
func ReplicatedKey(sourceRegion, sourceClusterID, namespace, service, instanceID string) []byte {
	return []byte(fmt.Sprintf("%s%s/clusters/%s/registry/%s/%s/%s", ReplicationRegistryPrefix, strings.TrimSpace(sourceRegion), strings.TrimSpace(sourceClusterID), strings.TrimSpace(namespace), strings.TrimSpace(service), strings.TrimSpace(instanceID)))
}

// ReplicationCheckpointKey 生成某个远端服务复制水位的状态机 key。
func ReplicationCheckpointKey(sourceRegion, sourceClusterID, namespace, service string) []byte {
	return []byte(fmt.Sprintf("%s%s/%s/%s/%s", ReplicationCheckpointPrefix, strings.TrimSpace(sourceRegion), strings.TrimSpace(sourceClusterID), strings.TrimSpace(namespace), strings.TrimSpace(service)))
}

// ParseReplicatedKey 反解远端复制目录 key。
func ParseReplicatedKey(key []byte) (sourceRegion, sourceClusterID, namespace, service, instanceID string, ok bool) {
	raw := string(key)
	if !strings.HasPrefix(raw, ReplicationRegistryPrefix) {
		return "", "", "", "", "", false
	}

	withoutPrefix := strings.TrimPrefix(raw, ReplicationRegistryPrefix)
	parts := strings.SplitN(withoutPrefix, replicationRegistryMarker, 2)
	if len(parts) != 2 {
		return "", "", "", "", "", false
	}

	sourceParts := strings.Split(parts[0], "/")
	if len(sourceParts) != 3 || sourceParts[1] != "clusters" {
		return "", "", "", "", "", false
	}

	targetParts := strings.Split(parts[1], "/")
	if len(targetParts) != 3 {
		return "", "", "", "", "", false
	}

	sourceRegion = strings.TrimSpace(sourceParts[0])
	sourceClusterID = strings.TrimSpace(sourceParts[2])
	namespace = strings.TrimSpace(targetParts[0])
	service = strings.TrimSpace(targetParts[1])
	instanceID = strings.TrimSpace(targetParts[2])
	if sourceRegion == "" || sourceClusterID == "" || namespace == "" || service == "" || instanceID == "" {
		return "", "", "", "", "", false
	}

	return sourceRegion, sourceClusterID, namespace, service, instanceID, true
}

// ParseReplicationCheckpointKey 反解复制 checkpoint key。
func ParseReplicationCheckpointKey(key []byte) (sourceRegion, sourceClusterID, namespace, service string, ok bool) {
	raw := string(key)
	if !strings.HasPrefix(raw, ReplicationCheckpointPrefix) {
		return "", "", "", "", false
	}

	parts := strings.Split(strings.TrimPrefix(raw, ReplicationCheckpointPrefix), "/")
	if len(parts) != 4 {
		return "", "", "", "", false
	}

	sourceRegion = strings.TrimSpace(parts[0])
	sourceClusterID = strings.TrimSpace(parts[1])
	namespace = strings.TrimSpace(parts[2])
	service = strings.TrimSpace(parts[3])
	if sourceRegion == "" || sourceClusterID == "" || namespace == "" || service == "" {
		return "", "", "", "", false
	}

	return sourceRegion, sourceClusterID, namespace, service, true
}

// NewReplicatedValue 根据远端源信息和原生实例值构造复制目录对象。
func NewReplicatedValue(sourceRegion, sourceClusterID string, sourceRevision uint64, value Value, now int64) ReplicatedValue {
	if now <= 0 {
		now = time.Now().Unix()
	}

	return ReplicatedValue{
		Namespace:         value.Namespace,
		Service:           value.Service,
		InstanceID:        value.InstanceID,
		Zone:              value.Zone,
		Labels:            CloneStringMap(value.Labels),
		Metadata:          CloneStringMap(value.Metadata),
		Endpoints:         CloneEndpoints(value.Endpoints),
		LeaseTTLSeconds:   EffectiveLeaseTTLSeconds(value.LeaseTTLSeconds),
		RegisteredAtUnix:  value.RegisteredAtUnix,
		LastHeartbeatUnix: value.LastHeartbeatUnix,
		SourceRegion:      strings.TrimSpace(sourceRegion),
		SourceClusterID:   strings.TrimSpace(sourceClusterID),
		SourceRevision:    sourceRevision,
		ReplicatedAtUnix:  now,
		Origin:            "replicated",
	}
}

// ToValue 将复制目录对象转换为普通实例值，便于复用现有查询与过滤逻辑。
func (v ReplicatedValue) ToValue() Value {
	return Value{
		Namespace:         v.Namespace,
		Service:           v.Service,
		InstanceID:        v.InstanceID,
		Zone:              v.Zone,
		Labels:            CloneStringMap(v.Labels),
		Metadata:          CloneStringMap(v.Metadata),
		Endpoints:         CloneEndpoints(v.Endpoints),
		LeaseTTLSeconds:   EffectiveLeaseTTLSeconds(v.LeaseTTLSeconds),
		RegisteredAtUnix:  v.RegisteredAtUnix,
		LastHeartbeatUnix: v.LastHeartbeatUnix,
	}
}
