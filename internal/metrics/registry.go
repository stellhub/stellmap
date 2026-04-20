package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stellhub/stellmap/internal/registry"
	"github.com/stellhub/stellmap/internal/storage"
)

const (
	unknownLabelValue = "unknown"
)

// RegistryScanner 描述 registry 指标 collector 所需的最小扫描能力。
type RegistryScanner interface {
	Scan(ctx context.Context, start, end []byte, limit int) ([]storage.KV, error)
}

// RegistryIdentity 描述一个注册中心客户端的结构化身份。
type RegistryIdentity struct {
	Namespace        string
	Service          string
	Organization     string
	BusinessDomain   string
	CapabilityDomain string
	Application      string
	Role             string
	Zone             string
}

// RegistryWatchTarget 描述一次 watch 订阅的目标。
type RegistryWatchTarget struct {
	Namespace string
	Service   string
	Scope     string
}

// RegistryMetrics 收敛注册中心客户端画像和 watch 治理指标。
type RegistryMetrics struct {
	activeCollector    *activeInstancesCollector
	registerRequests   *prometheus.CounterVec
	heartbeatRequests  *prometheus.CounterVec
	deregisterRequests *prometheus.CounterVec
	watchSessions      *prometheus.GaugeVec
}

// NewRegistryMetrics 创建注册中心画像和治理指标。
func NewRegistryMetrics(scanner RegistryScanner) *RegistryMetrics {
	identityLabels := []string{
		"namespace",
		"service",
		"organization",
		"business_domain",
		"capability_domain",
		"application",
		"role",
	}
	requestLabels := append(append([]string{}, identityLabels...), "zone", "code")
	watchLabels := []string{
		"watch_kind",
		"caller_namespace",
		"caller_service",
		"caller_organization",
		"caller_business_domain",
		"caller_capability_domain",
		"caller_application",
		"caller_role",
		"target_namespace",
		"target_service",
		"target_scope",
	}

	return &RegistryMetrics{
		activeCollector: newActiveInstancesCollector(scanner),
		registerRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "stellmap_registry_register_requests_total",
			Help: "Total registry register requests grouped by caller identity.",
		}, requestLabels),
		heartbeatRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "stellmap_registry_heartbeat_requests_total",
			Help: "Total registry heartbeat requests grouped by caller identity.",
		}, requestLabels),
		deregisterRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "stellmap_registry_deregister_requests_total",
			Help: "Total registry deregister requests grouped by caller identity.",
		}, requestLabels),
		watchSessions: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "stellmap_registry_watch_sessions",
			Help: "Current watch sessions grouped by caller identity and target.",
		}, watchLabels),
	}
}

// Register 将 registry 指标注册到指定 registry。
func (m *RegistryMetrics) Register(registerer prometheus.Registerer) error {
	if m == nil || registerer == nil {
		return nil
	}

	var errs []error
	for _, collector := range []prometheus.Collector{
		m.activeCollector,
		m.registerRequests,
		m.heartbeatRequests,
		m.deregisterRequests,
		m.watchSessions,
	} {
		if collector == nil {
			continue
		}
		if err := registerer.Register(collector); err != nil {
			var registeredErr prometheus.AlreadyRegisteredError
			if errors.As(err, &registeredErr) {
				continue
			}
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// ObserveRegister 记录一次注册请求。
func (m *RegistryMetrics) ObserveRegister(identity RegistryIdentity, statusCode int) {
	if m == nil {
		return
	}
	m.registerRequests.WithLabelValues(append(requestLabels(identity), strconv.Itoa(statusCode))...).Inc()
}

// ObserveHeartbeat 记录一次续约请求。
func (m *RegistryMetrics) ObserveHeartbeat(identity RegistryIdentity, statusCode int) {
	if m == nil {
		return
	}
	m.heartbeatRequests.WithLabelValues(append(requestLabels(identity), strconv.Itoa(statusCode))...).Inc()
}

// ObserveDeregister 记录一次注销请求。
func (m *RegistryMetrics) ObserveDeregister(identity RegistryIdentity, statusCode int) {
	if m == nil {
		return
	}
	m.deregisterRequests.WithLabelValues(append(requestLabels(identity), strconv.Itoa(statusCode))...).Inc()
}

// TrackWatchSession 记录一个 watch 会话，并返回结束函数。
func (m *RegistryMetrics) TrackWatchSession(watchKind string, caller RegistryIdentity, target RegistryWatchTarget) func() {
	if m == nil {
		return func() {}
	}

	labels := watchLabels(watchKind, caller, target)
	m.watchSessions.WithLabelValues(labels...).Inc()
	return func() {
		m.watchSessions.WithLabelValues(labels...).Dec()
	}
}

func requestLabels(identity RegistryIdentity) []string {
	return []string{
		labelValue(identity.Namespace),
		labelValue(identity.Service),
		labelValue(identity.Organization),
		labelValue(identity.BusinessDomain),
		labelValue(identity.CapabilityDomain),
		labelValue(identity.Application),
		labelValue(identity.Role),
		labelValue(identity.Zone),
	}
}

func watchLabels(watchKind string, caller RegistryIdentity, target RegistryWatchTarget) []string {
	return []string{
		labelValue(watchKind),
		labelValue(caller.Namespace),
		labelValue(caller.Service),
		labelValue(caller.Organization),
		labelValue(caller.BusinessDomain),
		labelValue(caller.CapabilityDomain),
		labelValue(caller.Application),
		labelValue(caller.Role),
		labelValue(target.Namespace),
		labelValue(target.Service),
		labelValue(target.Scope),
	}
}

func labelValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return unknownLabelValue
	}
	return value
}

type activeInstancesCollector struct {
	scanner RegistryScanner
	desc    *prometheus.Desc
}

func newActiveInstancesCollector(scanner RegistryScanner) *activeInstancesCollector {
	return &activeInstancesCollector{
		scanner: scanner,
		desc: prometheus.NewDesc(
			"stellmap_registry_active_instances",
			"Current active registry instances grouped by client identity.",
			[]string{
				"namespace",
				"service",
				"organization",
				"business_domain",
				"capability_domain",
				"application",
				"role",
				"zone",
			},
			nil,
		),
	}
}

func (c *activeInstancesCollector) Describe(ch chan<- *prometheus.Desc) {
	if c == nil || c.desc == nil {
		return
	}
	ch <- c.desc
}

func (c *activeInstancesCollector) Collect(ch chan<- prometheus.Metric) {
	if c == nil || c.scanner == nil || c.desc == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	items, err := c.scanner.Scan(ctx, []byte(registry.RootPrefix), prefixUpperBound(registry.RootPrefix), 0)
	if err != nil {
		ch <- prometheus.NewInvalidMetric(c.desc, fmt.Errorf("scan registry active instances: %w", err))
		return
	}

	now := time.Now().Unix()
	counts := make(map[string]float64)
	labelsByKey := make(map[string][]string)
	for _, item := range items {
		var value registry.Value
		if err := decodeRegistryValue(item.Value, &value); err != nil {
			continue
		}
		if !registry.IsAlive(value, now) {
			continue
		}

		labels := []string{
			labelValue(value.Namespace),
			labelValue(value.Service),
			labelValue(value.Organization),
			labelValue(value.BusinessDomain),
			labelValue(value.CapabilityDomain),
			labelValue(value.Application),
			labelValue(value.Role),
			labelValue(value.Zone),
		}
		key := strings.Join(labels, "\x00")
		labelsByKey[key] = labels
		counts[key]++
	}

	for key, count := range counts {
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, count, labelsByKey[key]...)
	}
}

func prefixUpperBound(prefix string) []byte {
	if prefix == "" {
		return nil
	}
	return append(append([]byte(nil), []byte(prefix)...), 0xFF)
}

func decodeRegistryValue(raw []byte, target *registry.Value) error {
	if len(raw) == 0 || target == nil {
		return fmt.Errorf("empty registry value")
	}
	return json.Unmarshal(raw, target)
}
