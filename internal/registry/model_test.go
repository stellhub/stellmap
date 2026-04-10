package registry

import (
	"net/url"
	"testing"
)

func TestNormalizeRegisterInputNormalizesAndDefaults(t *testing.T) {
	request := RegisterInput{
		Namespace:  " prod ",
		Service:    " order-service ",
		InstanceID: " order-1 ",
		Zone:       " az1 ",
		Labels: map[string]string{
			" color ": " gray ",
			"":        "ignored",
		},
		Metadata: map[string]string{
			" owner ": " trade-team ",
		},
		Endpoints: []Endpoint{
			{
				Protocol: " http ",
				Host:     " 127.0.0.1 ",
				Port:     8080,
			},
			{
				Name:     " grpc-main ",
				Protocol: " grpc ",
				Host:     " 127.0.0.1 ",
				Port:     9090,
				Path:     " /rpc ",
				Weight:   80,
			},
		},
	}

	if err := NormalizeRegisterInput(&request); err != nil {
		t.Fatalf("normalize register input failed: %v", err)
	}

	if request.Namespace != "prod" || request.Service != "order-service" || request.InstanceID != "order-1" || request.Zone != "az1" {
		t.Fatalf("unexpected normalized identity: %+v", request)
	}
	if request.LeaseTTLSeconds != DefaultLeaseTTLSeconds {
		t.Fatalf("expected default lease ttl %d, got %d", DefaultLeaseTTLSeconds, request.LeaseTTLSeconds)
	}
	if len(request.Endpoints) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(request.Endpoints))
	}
	if request.Endpoints[0].Name != "http" || request.Endpoints[0].Weight != DefaultEndpointWeight {
		t.Fatalf("unexpected defaulted endpoint: %+v", request.Endpoints[0])
	}
	if request.Endpoints[1].Name != "grpc-main" || request.Endpoints[1].Protocol != "grpc" || request.Endpoints[1].Host != "127.0.0.1" || request.Endpoints[1].Path != "/rpc" || request.Endpoints[1].Weight != 80 {
		t.Fatalf("unexpected normalized second endpoint: %+v", request.Endpoints[1])
	}
	if len(request.Labels) != 1 || request.Labels["color"] != "gray" {
		t.Fatalf("unexpected normalized labels: %+v", request.Labels)
	}
	if request.Metadata["owner"] != "trade-team" {
		t.Fatalf("unexpected normalized metadata: %+v", request.Metadata)
	}
}

func TestNormalizeRegisterInputRejectsInvalidEndpoint(t *testing.T) {
	tests := []struct {
		name    string
		request RegisterInput
	}{
		{
			name: "missing endpoints",
			request: RegisterInput{
				Namespace:  "prod",
				Service:    "svc",
				InstanceID: "i-1",
			},
		},
		{
			name: "invalid ttl",
			request: RegisterInput{
				Namespace:       "prod",
				Service:         "svc",
				InstanceID:      "i-1",
				LeaseTTLSeconds: -1,
				Endpoints: []Endpoint{{
					Protocol: "http",
					Host:     "127.0.0.1",
					Port:     8080,
				}},
			},
		},
		{
			name: "duplicate endpoint name",
			request: RegisterInput{
				Namespace:  "prod",
				Service:    "svc",
				InstanceID: "i-1",
				Endpoints: []Endpoint{
					{Name: "http", Protocol: "http", Host: "127.0.0.1", Port: 8080},
					{Name: "http", Protocol: "grpc", Host: "127.0.0.1", Port: 9090},
				},
			},
		},
		{
			name: "invalid port",
			request: RegisterInput{
				Namespace:  "prod",
				Service:    "svc",
				InstanceID: "i-1",
				Endpoints: []Endpoint{{
					Protocol: "http",
					Host:     "127.0.0.1",
					Port:     0,
				}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := NormalizeRegisterInput(&tt.request); err == nil {
				t.Fatalf("expected normalize register input error")
			}
		})
	}
}

func TestParseQueryAndFilterEndpoints(t *testing.T) {
	values := url.Values{
		"namespace": []string{"prod"},
		"service":   []string{"order-service"},
		"zone":      []string{"az1"},
		"endpoint":  []string{"http"},
		"limit":     []string{"5"},
		"selector":  []string{"color=gray,version in (v2)"},
		"label":     []string{"owner=trade-team"},
	}

	query, err := ParseQuery(values)
	if err != nil {
		t.Fatalf("parse query failed: %v", err)
	}

	if query.Namespace != "prod" || query.Service != "order-service" || query.Zone != "az1" || query.Endpoint != "http" || query.Limit != 5 {
		t.Fatalf("unexpected parsed query: %+v", query)
	}
	if len(query.Selector) != 3 {
		t.Fatalf("expected 3 selector requirements, got %d", len(query.Selector))
	}

	endpoints := []Endpoint{
		{Name: "http", Protocol: "http", Host: "127.0.0.1", Port: 8080},
		{Name: "grpc", Protocol: "grpc", Host: "127.0.0.1", Port: 9090},
	}
	filtered := FilterEndpoints(endpoints, "http")
	if len(filtered) != 1 || filtered[0].Name != "http" {
		t.Fatalf("unexpected filtered endpoints: %+v", filtered)
	}

	filtered[0].Host = "mutated"
	if endpoints[0].Host != "127.0.0.1" {
		t.Fatalf("filter endpoints should return cloned slice")
	}
}

func TestIsAliveUsesHeartbeatAndRegisteredAt(t *testing.T) {
	tests := []struct {
		name  string
		value Value
		now   int64
		alive bool
	}{
		{
			name: "alive by heartbeat",
			value: Value{
				LeaseTTLSeconds:   10,
				RegisteredAtUnix:  100,
				LastHeartbeatUnix: 105,
			},
			now:   110,
			alive: true,
		},
		{
			name: "expired by heartbeat",
			value: Value{
				LeaseTTLSeconds:   10,
				RegisteredAtUnix:  100,
				LastHeartbeatUnix: 105,
			},
			now:   116,
			alive: false,
		},
		{
			name: "fallback to registered at",
			value: Value{
				LeaseTTLSeconds:  10,
				RegisteredAtUnix: 100,
			},
			now:   109,
			alive: true,
		},
		{
			name: "zero now treated as alive",
			value: Value{
				LeaseTTLSeconds:  1,
				RegisteredAtUnix: 1,
			},
			now:   0,
			alive: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAlive(tt.value, tt.now); got != tt.alive {
				t.Fatalf("expected alive=%v, got %v", tt.alive, got)
			}
		})
	}
}
