package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/stellhub/stellmap/internal/registry"
	"github.com/stellhub/stellmap/internal/storage"
	httptransport "github.com/stellhub/stellmap/internal/transport/http"
)

const replicationRetryDelay = 2 * time.Second

// ReplicationService 描述一个需要跨 region 同步的 namespace/service。
type ReplicationService struct {
	Namespace string
	Service   string
}

// ReplicationTarget 描述一个远端目录同步源。
type ReplicationTarget struct {
	SourceRegion    string
	SourceClusterID string
	BaseURL         string
	Services        []ReplicationService
}

type replicationSSEEvent struct {
	ID    string
	Event string
	Data  string
}

// LoadReplicationTargets 加载 replication targets。
func LoadReplicationTargets(file string) ([]ReplicationTarget, error) {
	file = strings.TrimSpace(file)
	if file == "" {
		return nil, nil
	}
	return ParseReplicationTargetsFile(file)
}

// ParseReplicationTargetsFile 从 JSON 文件加载 replication targets。
func ParseReplicationTargetsFile(path string) ([]ReplicationTarget, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read replication targets file %q: %w", path, err)
	}

	var targets []ReplicationTarget
	if err := json.Unmarshal(data, &targets); err != nil {
		return nil, fmt.Errorf("unmarshal replication targets file %q: %w", path, err)
	}
	for index := range targets {
		targets[index].SourceRegion = strings.TrimSpace(targets[index].SourceRegion)
		targets[index].SourceClusterID = strings.TrimSpace(targets[index].SourceClusterID)
		targets[index].BaseURL = strings.TrimRight(strings.TrimSpace(targets[index].BaseURL), "/")
		if targets[index].SourceRegion == "" || targets[index].SourceClusterID == "" || targets[index].BaseURL == "" {
			return nil, fmt.Errorf("replication target[%d] requires sourceRegion, sourceClusterId and baseURL", index)
		}
		if len(targets[index].Services) == 0 {
			return nil, fmt.Errorf("replication target[%d] requires at least one service", index)
		}
		for serviceIndex := range targets[index].Services {
			targets[index].Services[serviceIndex].Namespace = strings.TrimSpace(targets[index].Services[serviceIndex].Namespace)
			targets[index].Services[serviceIndex].Service = strings.TrimSpace(targets[index].Services[serviceIndex].Service)
			if targets[index].Services[serviceIndex].Namespace == "" || targets[index].Services[serviceIndex].Service == "" {
				return nil, fmt.Errorf("replication target[%d] service[%d] requires namespace and service", index, serviceIndex)
			}
		}
	}

	return targets, nil
}

func (a *App) replicationLoop(ctx context.Context) {
	if len(a.replicationTargets) == 0 {
		return
	}

	for _, target := range a.replicationTargets {
		for _, service := range target.Services {
			go a.replicateServiceLoop(ctx, target, service)
		}
	}
}

func (a *App) replicateServiceLoop(ctx context.Context, target ReplicationTarget, service ReplicationService) {
	for {
		if !a.isRegistryCleanupLeader() {
			if a.replicationTracker != nil {
				a.replicationTracker.SetConnected(target.SourceRegion, target.SourceClusterID, service.Namespace, service.Service, false)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(replicationRetryDelay):
				continue
			}
		}

		if a.replicationTracker != nil {
			a.replicationTracker.SetConnected(target.SourceRegion, target.SourceClusterID, service.Namespace, service.Service, false)
		}
		err := a.runReplicationStream(ctx, target, service)
		if err != nil && !errorsIsContext(err) {
			if a.replicationTracker != nil {
				a.replicationTracker.RecordError(target.SourceRegion, target.SourceClusterID, service.Namespace, service.Service, err.Error())
			}
			log.Printf("replicate service %s/%s from %s@%s failed: %v", service.Namespace, service.Service, target.SourceRegion, target.SourceClusterID, err)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(replicationRetryDelay):
		}
	}
}

func (a *App) runReplicationStream(ctx context.Context, target ReplicationTarget, service ReplicationService) error {
	checkpointCtx, checkpointCancel := context.WithTimeout(ctx, a.cfg.RequestTimeout)
	checkpoint, err := a.readReplicationCheckpoint(checkpointCtx, target.SourceRegion, target.SourceClusterID, service.Namespace, service.Service)
	checkpointCancel()
	if err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, buildReplicationWatchURL(target.BaseURL, service, checkpoint.LastAppliedRevision), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+a.cfg.ReplicationToken)

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		return fmt.Errorf("replication watch status=%d body=%s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	if a.replicationTracker != nil {
		a.replicationTracker.SetConnected(target.SourceRegion, target.SourceClusterID, service.Namespace, service.Service, true)
	}

	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	var current replicationSSEEvent
	currentRevision := uint64(0)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if current.Event != "" && current.Data != "" {
				revision, err := parseRevision(current.ID)
				if err != nil {
					return err
				}
				if revision > currentRevision {
					if err := a.applyReplicationEvent(ctx, target, service, current.Event, revision, []byte(current.Data)); err != nil {
						return err
					}
					currentRevision = revision
				}
			}
			current = replicationSSEEvent{}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "id:"):
			current.ID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "event:"):
			current.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			current.Data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	return ctx.Err()
}

func buildReplicationWatchURL(baseURL string, service ReplicationService, sinceRevision uint64) string {
	values := url.Values{}
	values.Set("namespace", service.Namespace)
	values.Set("service", service.Service)
	if sinceRevision > 0 {
		values.Set("sinceRevision", fmt.Sprintf("%d", sinceRevision))
	}

	return strings.TrimRight(baseURL, "/") + "/internal/v1/replication/watch?" + values.Encode()
}

func parseRevision(raw string) (uint64, error) {
	var revision uint64
	if _, err := fmt.Sscanf(strings.TrimSpace(raw), "%d", &revision); err != nil {
		return 0, fmt.Errorf("parse replication revision %q: %w", raw, err)
	}
	return revision, nil
}

func (a *App) applyReplicationEvent(ctx context.Context, target ReplicationTarget, service ReplicationService, eventType string, revision uint64, data []byte) error {
	switch eventType {
	case "snapshot":
		var payload httptransport.ReplicationWatchEventDTO
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		return a.applyReplicationSnapshot(ctx, target, service, payload, revision)
	case string(registry.WatchEventUpsert):
		var payload httptransport.ReplicationWatchEventDTO
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		return a.applyReplicationUpsert(ctx, target, service, payload, revision)
	case string(registry.WatchEventDelete):
		var payload httptransport.ReplicationWatchEventDTO
		if err := json.Unmarshal(data, &payload); err != nil {
			return err
		}
		return a.applyReplicationDelete(ctx, target, service, payload, revision)
	default:
		return nil
	}
}

func (a *App) applyReplicationSnapshot(ctx context.Context, target ReplicationTarget, service ReplicationService, payload httptransport.ReplicationWatchEventDTO, revision uint64) error {
	servicePrefix := registry.ReplicationServicePrefix(target.SourceRegion, target.SourceClusterID, service.Namespace, service.Service)
	if err := a.node.ProposeCommand(ctx, storage.Command{
		Operation: storage.OperationDeletePrefix,
		Prefix:    []byte(servicePrefix),
	}); err != nil {
		return err
	}

	for _, instance := range payload.Instances {
		replicatedValue, err := replicatedValueFromDTO(target, instance, revision)
		if err != nil {
			return err
		}
		if err := a.putReplicatedValue(ctx, replicatedValue); err != nil {
			return err
		}
	}

	return a.putReplicationCheckpoint(ctx, registry.ReplicationCheckpoint{
		SourceRegion:         target.SourceRegion,
		SourceClusterID:      target.SourceClusterID,
		Namespace:            service.Namespace,
		Service:              service.Service,
		LastAppliedRevision:  revision,
		LastSnapshotRevision: revision,
		UpdatedAtUnix:        time.Now().Unix(),
	})
}

func (a *App) applyReplicationUpsert(ctx context.Context, target ReplicationTarget, service ReplicationService, payload httptransport.ReplicationWatchEventDTO, revision uint64) error {
	if payload.Instance == nil {
		return a.putReplicationCheckpoint(ctx, registry.ReplicationCheckpoint{
			SourceRegion:         target.SourceRegion,
			SourceClusterID:      target.SourceClusterID,
			Namespace:            service.Namespace,
			Service:              service.Service,
			LastAppliedRevision:  revision,
			LastSnapshotRevision: 0,
			UpdatedAtUnix:        time.Now().Unix(),
		})
	}

	replicatedValue, err := replicatedValueFromDTO(target, *payload.Instance, revision)
	if err != nil {
		return err
	}
	if err := a.putReplicatedValue(ctx, replicatedValue); err != nil {
		return err
	}

	return a.putReplicationCheckpoint(ctx, registry.ReplicationCheckpoint{
		SourceRegion:         target.SourceRegion,
		SourceClusterID:      target.SourceClusterID,
		Namespace:            service.Namespace,
		Service:              service.Service,
		LastAppliedRevision:  revision,
		LastSnapshotRevision: 0,
		UpdatedAtUnix:        time.Now().Unix(),
	})
}

func (a *App) applyReplicationDelete(ctx context.Context, target ReplicationTarget, service ReplicationService, payload httptransport.ReplicationWatchEventDTO, revision uint64) error {
	if payload.InstanceID != "" {
		if err := a.node.ProposeCommand(ctx, storage.Command{
			Operation: storage.OperationDelete,
			Key:       registry.ReplicatedKey(target.SourceRegion, target.SourceClusterID, service.Namespace, service.Service, payload.InstanceID),
		}); err != nil {
			return err
		}
	}

	return a.putReplicationCheckpoint(ctx, registry.ReplicationCheckpoint{
		SourceRegion:         target.SourceRegion,
		SourceClusterID:      target.SourceClusterID,
		Namespace:            service.Namespace,
		Service:              service.Service,
		LastAppliedRevision:  revision,
		LastSnapshotRevision: 0,
		UpdatedAtUnix:        time.Now().Unix(),
	})
}

func (a *App) putReplicatedValue(ctx context.Context, value registry.ReplicatedValue) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	return a.node.ProposeCommand(ctx, storage.Command{
		Operation: storage.OperationPut,
		Key:       registry.ReplicatedKey(value.SourceRegion, value.SourceClusterID, value.Namespace, value.Service, value.InstanceID),
		Value:     data,
	})
}

func (a *App) putReplicationCheckpoint(ctx context.Context, checkpoint registry.ReplicationCheckpoint) error {
	data, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}

	if err := a.node.ProposeCommand(ctx, storage.Command{
		Operation: storage.OperationPut,
		Key:       registry.ReplicationCheckpointKey(checkpoint.SourceRegion, checkpoint.SourceClusterID, checkpoint.Namespace, checkpoint.Service),
		Value:     data,
	}); err != nil {
		return err
	}
	if a.replicationTracker != nil {
		a.replicationTracker.UpdateRevision(
			checkpoint.SourceRegion,
			checkpoint.SourceClusterID,
			checkpoint.Namespace,
			checkpoint.Service,
			checkpoint.LastAppliedRevision,
			checkpoint.LastSnapshotRevision,
		)
	}
	return nil
}

func replicatedValueFromDTO(target ReplicationTarget, instance httptransport.RegistryInstanceDTO, revision uint64) (registry.ReplicatedValue, error) {
	value := registry.Value{
		Namespace:         instance.Namespace,
		Service:           instance.Service,
		InstanceID:        instance.InstanceID,
		Zone:              instance.Zone,
		Labels:            registry.CloneStringMap(instance.Labels),
		Metadata:          registry.CloneStringMap(instance.Metadata),
		Endpoints:         append([]registry.Endpoint(nil), instance.Endpoints...),
		LeaseTTLSeconds:   instance.LeaseTTLSeconds,
		RegisteredAtUnix:  instance.RegisteredAtUnix,
		LastHeartbeatUnix: instance.LastHeartbeatUnix,
	}
	return registry.NewReplicatedValue(target.SourceRegion, target.SourceClusterID, revision, value, time.Now().Unix()), nil
}

func errorsIsContext(err error) bool {
	return err == context.Canceled || err == context.DeadlineExceeded
}

// readReplicationCheckpoint 预留给后续支持 sinceRevision 的恢复逻辑。
func (a *App) readReplicationCheckpoint(ctx context.Context, sourceRegion, sourceClusterID, namespace, service string) (registry.ReplicationCheckpoint, error) {
	data, err := a.node.Get(ctx, registry.ReplicationCheckpointKey(sourceRegion, sourceClusterID, namespace, service))
	if err != nil {
		return registry.ReplicationCheckpoint{}, err
	}
	if len(data) == 0 {
		return registry.ReplicationCheckpoint{}, nil
	}

	var checkpoint registry.ReplicationCheckpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return registry.ReplicationCheckpoint{}, err
	}
	return checkpoint, nil
}
