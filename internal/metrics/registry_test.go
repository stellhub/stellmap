package metrics

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chenwenlong-java/StarMap/internal/registry"
	"github.com/chenwenlong-java/StarMap/internal/storage"
	"github.com/prometheus/client_golang/prometheus"
)

type fakeRegistryScanner struct {
	scanFunc func(ctx context.Context, start, end []byte, limit int) ([]storage.KV, error)
}

func (f fakeRegistryScanner) Scan(ctx context.Context, start, end []byte, limit int) ([]storage.KV, error) {
	if f.scanFunc != nil {
		return f.scanFunc(ctx, start, end, limit)
	}
	return nil, nil
}

func TestRegistryMetricsRegisterAndObserve(t *testing.T) {
	now := time.Now().Unix()
	items := []storage.KV{
		mustRegistryKV(t, registry.NewValue(registry.RegisterInput{
			Namespace:        "prod",
			Service:          "company.trade.order.order-center.api",
			Organization:     "company",
			BusinessDomain:   "trade",
			CapabilityDomain: "order",
			Application:      "order-center",
			Role:             "api",
			Zone:             "az-1",
			InstanceID:       "order-center-api-1",
			LeaseTTLSeconds:  30,
		}, now)),
		mustRegistryKV(t, registry.NewValue(registry.RegisterInput{
			Namespace:        "prod",
			Service:          "company.trade.order.order-center.api",
			Organization:     "company",
			BusinessDomain:   "trade",
			CapabilityDomain: "order",
			Application:      "order-center",
			Role:             "api",
			Zone:             "az-1",
			InstanceID:       "order-center-api-2",
			LeaseTTLSeconds:  30,
		}, now)),
		mustRegistryKV(t, registry.NewValue(registry.RegisterInput{
			Namespace:        "prod",
			Service:          "company.trade.payment.payment-center.api",
			Organization:     "company",
			BusinessDomain:   "trade",
			CapabilityDomain: "payment",
			Application:      "payment-center",
			Role:             "api",
			Zone:             "az-2",
			InstanceID:       "payment-center-api-1",
			LeaseTTLSeconds:  30,
		}, now-120)),
	}

	registryMetrics := NewRegistryMetrics(fakeRegistryScanner{
		scanFunc: func(ctx context.Context, start, end []byte, limit int) ([]storage.KV, error) {
			return items, nil
		},
	})
	registryProm := prometheus.NewRegistry()
	if err := registryMetrics.Register(registryProm); err != nil {
		t.Fatalf("register registry metrics failed: %v", err)
	}

	identity := RegistryIdentity{
		Namespace:        "prod",
		Service:          "company.trade.order.order-center.api",
		Organization:     "company",
		BusinessDomain:   "trade",
		CapabilityDomain: "order",
		Application:      "order-center",
		Role:             "api",
		Zone:             "az-1",
	}
	registryMetrics.ObserveRegister(identity, 200)
	registryMetrics.ObserveHeartbeat(identity, 200)
	registryMetrics.ObserveDeregister(identity, 503)

	done := registryMetrics.TrackWatchSession("instances", identity, RegistryWatchTarget{
		Namespace: "prod",
		Service:   "company.trade.payment.payment-center.api",
		Scope:     "service",
	})

	families, err := registryProm.Gather()
	if err != nil {
		t.Fatalf("gather registry metrics failed: %v", err)
	}

	assertGaugeValue(t, families, "starmap_registry_active_instances", map[string]string{
		"namespace":         "prod",
		"service":           "company.trade.order.order-center.api",
		"organization":      "company",
		"business_domain":   "trade",
		"capability_domain": "order",
		"application":       "order-center",
		"role":              "api",
		"zone":              "az-1",
	}, 2)
	assertCounterValue(t, families, "starmap_registry_register_requests_total", map[string]string{
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
	assertCounterValue(t, families, "starmap_registry_heartbeat_requests_total", map[string]string{
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
	assertCounterValue(t, families, "starmap_registry_deregister_requests_total", map[string]string{
		"namespace":         "prod",
		"service":           "company.trade.order.order-center.api",
		"organization":      "company",
		"business_domain":   "trade",
		"capability_domain": "order",
		"application":       "order-center",
		"role":              "api",
		"zone":              "az-1",
		"code":              "503",
	}, 1)
	assertGaugeValue(t, families, "starmap_registry_watch_sessions", map[string]string{
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
	}, 1)

	done()

	families, err = registryProm.Gather()
	if err != nil {
		t.Fatalf("gather registry metrics after watch close failed: %v", err)
	}
	assertGaugeValue(t, families, "starmap_registry_watch_sessions", map[string]string{
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
	}, 0)
}

func mustRegistryKV(t *testing.T, value registry.Value) storage.KV {
	t.Helper()

	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal registry value failed: %v", err)
	}

	return storage.KV{
		Key:   registry.Key(value.Namespace, value.Service, value.InstanceID),
		Value: payload,
	}
}
