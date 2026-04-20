package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	internalmetrics "github.com/stellhub/stellmap/internal/metrics"
	"github.com/stellhub/stellmap/internal/raftnode"
	"github.com/stellhub/stellmap/internal/registry"
	"github.com/stellhub/stellmap/internal/storage"
)

func TestRegisterRecordsRegistryClientMetrics(t *testing.T) {
	t.Parallel()

	metricsRegistry := prometheus.NewRegistry()
	registryMetrics := internalmetrics.NewRegistryMetrics(nil)
	if err := registryMetrics.Register(metricsRegistry); err != nil {
		t.Fatalf("register registry metrics failed: %v", err)
	}

	node := &fakeRegistryNode{
		status: raftnode.Status{
			NodeID:   1,
			LeaderID: 1,
			Role:     raftnode.RoleLeader,
			Started:  true,
		},
	}
	handler := (&RegistryAPI{
		node:          node,
		requestTimout: time.Second,
	}).WithRegistryMetrics(registryMetrics)

	response := performJSONHandlerRequest(t, http.MethodPost, routeRegistryRegister, `{
  "namespace":"prod",
  "organization":"company",
  "businessDomain":"trade",
  "capabilityDomain":"order",
  "application":"order-center",
  "role":"api",
  "instanceId":"order-center-api-1",
  "zone":"az-1",
  "endpoints":[{"protocol":"http","host":"127.0.0.1","port":8080}]
}`, handler.Register)
	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", response.Code)
	}

	families, err := metricsRegistry.Gather()
	if err != nil {
		t.Fatalf("gather registry metrics failed: %v", err)
	}
	assertHTTPMetricCounter(t, families, "stellmap_registry_register_requests_total", map[string]string{
		"namespace":         "prod",
		"service":           "company.trade.order.order-center.api",
		"organization":      "company",
		"business_domain":   "trade",
		"capability_domain": "order",
		"application":       "order-center",
		"role":              "api",
		"zone":              "az-1",
		"code":              "200",
	}, 1)
}

func TestWatchInstancesTracksCallerAwareSessions(t *testing.T) {
	t.Parallel()

	metricsRegistry := prometheus.NewRegistry()
	registryMetrics := internalmetrics.NewRegistryMetrics(nil)
	if err := registryMetrics.Register(metricsRegistry); err != nil {
		t.Fatalf("register registry metrics failed: %v", err)
	}

	now := time.Now().Unix()
	node := &fakeRegistryNode{
		status: raftnode.Status{
			NodeID:       1,
			LeaderID:     1,
			Role:         raftnode.RoleLeader,
			Started:      true,
			AppliedIndex: 7,
			CommitIndex:  7,
		},
		scanFunc: func(ctx context.Context, start, end []byte, limit int) ([]storage.KV, error) {
			return []storage.KV{
				mustRegistryKV(t, registry.NewValue(registry.RegisterInput{
					Namespace:        "prod",
					Service:          "company.trade.payment.payment-center.api",
					Organization:     "company",
					BusinessDomain:   "trade",
					CapabilityDomain: "payment",
					Application:      "payment-center",
					Role:             "api",
					InstanceID:       "payment-center-api-1",
					LeaseTTLSeconds:  30,
				}, now)),
			}, nil
		},
	}
	handler := (&RegistryAPI{
		node:          node,
		httpAddr:      "127.0.0.1:8080",
		watchHub:      registry.NewWatchHub(),
		requestTimout: time.Second,
	}).WithRegistryMetrics(registryMetrics)

	server := httptest.NewServer(http.HandlerFunc(handler.WatchInstances))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		server.URL+"?namespace=prod&service=company.trade.payment.payment-center.api&callerNamespace=prod&callerOrganization=company&callerBusinessDomain=trade&callerCapabilityDomain=order&callerApplication=order-center&callerRole=api",
		nil,
	)
	if err != nil {
		t.Fatalf("create watch request failed: %v", err)
	}

	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("execute watch request failed: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected watch status 200, got %d", response.StatusCode)
	}

	eventID, eventName, _ := readSSEEvent(t, response.Body)
	if eventID != "7" || eventName != "snapshot" {
		t.Fatalf("expected snapshot event id=7 event=snapshot, got id=%s event=%s", eventID, eventName)
	}

	assertWatchGaugeValue(t, metricsRegistry, 1)

	cancel()
	_ = response.Body.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if gauge := currentWatchGaugeValue(t, metricsRegistry); gauge == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("expected watch session gauge to return to zero")
}

func assertWatchGaugeValue(t *testing.T, metricsRegistry *prometheus.Registry, want float64) {
	t.Helper()
	if got := currentWatchGaugeValue(t, metricsRegistry); got != want {
		t.Fatalf("expected watch session gauge %v, got %v", want, got)
	}
}

func currentWatchGaugeValue(t *testing.T, metricsRegistry *prometheus.Registry) float64 {
	t.Helper()

	families, err := metricsRegistry.Gather()
	if err != nil {
		t.Fatalf("gather registry metrics failed: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "stellmap_registry_watch_sessions" {
			continue
		}
		for _, metric := range family.GetMetric() {
			if httpMetricMatches(metric, map[string]string{
				"watch_kind":               "instances",
				"caller_namespace":         "prod",
				"caller_service":           "company.trade.order.order-center.api",
				"caller_organization":      "company",
				"caller_business_domain":   "trade",
				"caller_capability_domain": "order",
				"caller_application":       "order-center",
				"caller_role":              "api",
				"target_namespace":         "prod",
				"target_service":           "company.trade.payment.payment-center.api",
				"target_scope":             "service",
			}) {
				return metric.GetGauge().GetValue()
			}
		}
	}

	t.Fatalf("watch session metric not found")
	return 0
}
