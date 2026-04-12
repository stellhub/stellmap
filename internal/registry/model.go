package registry

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	// RootPrefix 是注册中心实例在状态机中的根前缀。
	RootPrefix = "/registry/"
	// DefaultLeaseTTLSeconds 是实例租约 TTL 的默认值。
	DefaultLeaseTTLSeconds = 30
	// DefaultEndpointWeight 是端点权重的默认值。
	DefaultEndpointWeight = 100
)

// Endpoint 表示实例对外暴露的一个协议端点。
type Endpoint struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int32  `json:"port"`
	Path     string `json:"path,omitempty"`
	Weight   int32  `json:"weight,omitempty"`
}

// RegisterInput 表示注册中心领域层使用的注册输入。
type RegisterInput struct {
	Namespace       string
	Service         string
	InstanceID      string
	Zone            string
	Labels          map[string]string
	Metadata        map[string]string
	Endpoints       []Endpoint
	LeaseTTLSeconds int64
}

// RegisterRequest 是面向传输层暴露的注册请求别名。
type RegisterRequest = RegisterInput

// DeregisterRequest 表示实例注销请求。
type DeregisterRequest struct {
	Namespace  string `json:"namespace"`
	Service    string `json:"service"`
	InstanceID string `json:"instanceId"`
}

// HeartbeatRequest 表示实例续约请求。
type HeartbeatRequest struct {
	Namespace       string `json:"namespace"`
	Service         string `json:"service"`
	InstanceID      string `json:"instanceId"`
	LeaseTTLSeconds int64  `json:"leaseTtlSeconds"`
}

// Value 是注册中心写入状态机的实例值。
type Value struct {
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
}

// Query 描述一次实例查询的过滤条件。
type Query struct {
	Namespace string
	Service   string
	Zone      string
	Endpoint  string
	Selector  Selector
	Limit     int
}

// MatchQuery 判断实例是否满足查询过滤条件。
func MatchQuery(value Value, query Query) bool {
	if query.Namespace != "" && value.Namespace != query.Namespace {
		return false
	}
	if query.Service != "" && value.Service != query.Service {
		return false
	}
	if query.Zone != "" && value.Zone != query.Zone {
		return false
	}
	if !MatchesLabelSelector(value.Labels, query.Selector) {
		return false
	}
	if query.Endpoint != "" && len(FilterEndpoints(value.Endpoints, query.Endpoint)) == 0 {
		return false
	}

	return true
}

// NormalizeInstanceIdentity 统一清理注册中心实例身份字段两端的空白字符。
func NormalizeInstanceIdentity(namespace, service, instanceID *string) {
	*namespace = strings.TrimSpace(*namespace)
	*service = strings.TrimSpace(*service)
	*instanceID = strings.TrimSpace(*instanceID)
}

// NormalizeRegisterInput 规范化并校验注册输入。
func NormalizeRegisterInput(request *RegisterInput) error {
	request.Namespace = strings.TrimSpace(request.Namespace)
	request.Service = strings.TrimSpace(request.Service)
	request.InstanceID = strings.TrimSpace(request.InstanceID)
	request.Zone = strings.TrimSpace(request.Zone)
	request.Labels = CloneStringMap(request.Labels)
	request.Metadata = CloneStringMap(request.Metadata)
	if request.LeaseTTLSeconds < 0 {
		return fmt.Errorf("leaseTtlSeconds must be greater than or equal to 0")
	}
	request.LeaseTTLSeconds = EffectiveLeaseTTLSeconds(request.LeaseTTLSeconds)

	if len(request.Endpoints) == 0 {
		return fmt.Errorf("at least one endpoint is required")
	}

	seen := make(map[string]struct{}, len(request.Endpoints))
	normalized := make([]Endpoint, 0, len(request.Endpoints))
	for _, endpoint := range request.Endpoints {
		endpoint.Name = strings.TrimSpace(endpoint.Name)
		endpoint.Protocol = strings.TrimSpace(endpoint.Protocol)
		endpoint.Host = strings.TrimSpace(endpoint.Host)
		endpoint.Path = strings.TrimSpace(endpoint.Path)

		if endpoint.Protocol == "" {
			return fmt.Errorf("endpoint protocol is required")
		}
		if endpoint.Name == "" {
			endpoint.Name = endpoint.Protocol
		}
		if endpoint.Host == "" {
			return fmt.Errorf("endpoint host is required")
		}
		if endpoint.Port <= 0 || endpoint.Port > 65535 {
			return fmt.Errorf("endpoint port must be within 1..65535")
		}
		if endpoint.Weight < 0 {
			return fmt.Errorf("endpoint weight must be greater than or equal to 0")
		}
		if endpoint.Weight == 0 {
			endpoint.Weight = DefaultEndpointWeight
		}
		if _, ok := seen[endpoint.Name]; ok {
			return fmt.Errorf("duplicate endpoint name: %s", endpoint.Name)
		}
		seen[endpoint.Name] = struct{}{}
		normalized = append(normalized, endpoint)
	}

	request.Endpoints = normalized
	return nil
}

// NormalizeRegisterRequest 兼容传输层调用，实际复用领域层注册输入规范化逻辑。
func NormalizeRegisterRequest(request *RegisterRequest) error {
	return NormalizeRegisterInput((*RegisterInput)(request))
}

// NewValue 根据注册输入构造状态机落盘对象。
func NewValue(request RegisterInput, now int64) Value {
	return Value{
		Namespace:         request.Namespace,
		Service:           request.Service,
		InstanceID:        request.InstanceID,
		Zone:              request.Zone,
		Labels:            CloneStringMap(request.Labels),
		Metadata:          CloneStringMap(request.Metadata),
		Endpoints:         CloneEndpoints(request.Endpoints),
		LeaseTTLSeconds:   EffectiveLeaseTTLSeconds(request.LeaseTTLSeconds),
		RegisteredAtUnix:  now,
		LastHeartbeatUnix: now,
	}
}

// CloneStringMap 复制一个字符串 map，避免直接复用调用方的底层对象。
func CloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(source))
	for key, value := range source {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		cloned[key] = strings.TrimSpace(value)
	}
	if len(cloned) == 0 {
		return nil
	}

	return cloned
}

// CloneEndpoints 深拷贝端点列表，避免和请求对象共享底层切片。
func CloneEndpoints(source []Endpoint) []Endpoint {
	if len(source) == 0 {
		return nil
	}

	cloned := make([]Endpoint, 0, len(source))
	for _, endpoint := range source {
		cloned = append(cloned, Endpoint{
			Name:     endpoint.Name,
			Protocol: endpoint.Protocol,
			Host:     endpoint.Host,
			Port:     endpoint.Port,
			Path:     endpoint.Path,
			Weight:   endpoint.Weight,
		})
	}

	return cloned
}

// ParseQuery 解析实例查询的过滤条件。
func ParseQuery(values url.Values) (Query, error) {
	query := Query{
		Namespace: strings.TrimSpace(values.Get("namespace")),
		Service:   strings.TrimSpace(values.Get("service")),
		Zone:      strings.TrimSpace(values.Get("zone")),
		Endpoint:  strings.TrimSpace(values.Get("endpoint")),
	}
	if query.Namespace == "" || query.Service == "" {
		return Query{}, fmt.Errorf("namespace and service are required")
	}

	limit, err := parseLimit(values.Get("limit"))
	if err != nil {
		return Query{}, err
	}
	query.Limit = limit

	selector, err := ParseLabelSelectorFilters(values["selector"], values["label"])
	if err != nil {
		return Query{}, err
	}
	query.Selector = selector

	return query, nil
}

// FilterEndpoints 过滤出符合 endpoint 条件的端点列表。
func FilterEndpoints(endpoints []Endpoint, expected string) []Endpoint {
	if expected == "" {
		return CloneEndpoints(endpoints)
	}

	filtered := make([]Endpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if endpoint.Name == expected || endpoint.Protocol == expected {
			filtered = append(filtered, Endpoint{
				Name:     endpoint.Name,
				Protocol: endpoint.Protocol,
				Host:     endpoint.Host,
				Port:     endpoint.Port,
				Path:     endpoint.Path,
				Weight:   endpoint.Weight,
			})
		}
	}

	return filtered
}

// ServicePrefix 返回某个 namespace/service 下所有实例的公共 key 前缀。
func ServicePrefix(namespace, service string) string {
	return fmt.Sprintf("%s%s/%s/", RootPrefix, namespace, service)
}

// Key 生成注册中心实例在状态机中的 KV key。
func Key(namespace, service, instanceID string) []byte {
	return []byte(fmt.Sprintf("%s%s/%s/%s", RootPrefix, namespace, service, instanceID))
}

// ParseKey 反解注册中心实例在状态机中的 KV key。
func ParseKey(key []byte) (namespace, service, instanceID string, ok bool) {
	raw := string(key)
	if !strings.HasPrefix(raw, RootPrefix) {
		return "", "", "", false
	}

	parts := strings.Split(strings.TrimPrefix(raw, RootPrefix), "/")
	if len(parts) != 3 {
		return "", "", "", false
	}
	if strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[2]) == "" {
		return "", "", "", false
	}

	return parts[0], parts[1], parts[2], true
}

// EffectiveLeaseTTLSeconds 返回实例的有效 TTL。
func EffectiveLeaseTTLSeconds(ttl int64) int64 {
	if ttl > 0 {
		return ttl
	}

	return DefaultLeaseTTLSeconds
}

// IsAlive 判断实例当前是否仍在租约有效期内。
func IsAlive(value Value, now int64) bool {
	if now <= 0 {
		return true
	}

	lastHeartbeat := value.LastHeartbeatUnix
	if lastHeartbeat <= 0 {
		lastHeartbeat = value.RegisteredAtUnix
	}
	if lastHeartbeat <= 0 {
		return true
	}

	return now <= lastHeartbeat+EffectiveLeaseTTLSeconds(value.LeaseTTLSeconds)
}

func parseLimit(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}

	limit, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	if limit < 0 {
		return 0, fmt.Errorf("limit must be greater than or equal to 0")
	}

	return limit, nil
}
