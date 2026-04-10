package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenwenlong-java/StarMap/internal/raftnode"
	"github.com/chenwenlong-java/StarMap/internal/registry"
	"github.com/chenwenlong-java/StarMap/internal/storage"
	httptransport "github.com/chenwenlong-java/StarMap/internal/transport/http"
)

type testSuccessEnvelope struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

const testAdminToken = "test-admin-token"

func TestStarmapdThreeNodeClusterWriteAndRecovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clusterID := uint64(300)
	peerIDs := "1,2,3"

	httpAddrs := map[uint64]string{
		1: reserveTCPAddress(t),
		2: reserveTCPAddress(t),
		3: reserveTCPAddress(t),
	}
	adminAddrs := map[uint64]string{
		1: reserveTCPAddress(t),
		2: reserveTCPAddress(t),
		3: reserveTCPAddress(t),
	}
	grpcAddrs := map[uint64]string{
		1: reserveTCPAddress(t),
		2: reserveTCPAddress(t),
		3: reserveTCPAddress(t),
	}

	apps := make(map[uint64]*serverApp, 3)
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		app := mustNewTestApp(t, daemonConfig{
			NodeID:         nodeID,
			ClusterID:      clusterID,
			DataDir:        filepath.Join(t.TempDir(), fmt.Sprintf("node-%d", nodeID)),
			HTTPAddr:       httpAddrs[nodeID],
			AdminAddr:      adminAddrs[nodeID],
			AdminToken:     testAdminToken,
			GRPCAddr:       grpcAddrs[nodeID],
			PeerIDs:        peerIDs,
			PeerHTTPAddrs:  joinAddressMap(httpAddrs),
			PeerAdminAddrs: joinAddressMap(adminAddrs),
			PeerGRPCAddrs:  joinAddressMap(grpcAddrs),
		})
		if err := app.Start(ctx); err != nil {
			t.Fatalf("start node %d: %v", nodeID, err)
		}
		apps[nodeID] = app
	}
	defer stopApps(t, apps)

	leaderID := waitForClusterLeader(t, apps, 10*time.Second)
	leaderBaseURL := "http://" + httpAddrs[leaderID]

	if err := putKV(leaderBaseURL, "cluster-key-1", []byte("value-1")); err != nil {
		t.Fatalf("put first key via leader %d: %v", leaderID, err)
	}
	for nodeID, addr := range httpAddrs {
		waitForKVValue(t, "http://"+addr, "cluster-key-1", "value-1", 10*time.Second, fmt.Sprintf("node-%d first key", nodeID))
	}

	restartNodeID := pickFollower(apps, leaderID)
	restartCfg := apps[restartNodeID].Config()
	if err := apps[restartNodeID].Stop(context.Background()); err != nil {
		t.Fatalf("stop follower %d: %v", restartNodeID, err)
	}
	delete(apps, restartNodeID)

	if err := putKV(leaderBaseURL, "cluster-key-2", []byte("value-2")); err != nil {
		t.Fatalf("put second key while follower down: %v", err)
	}
	for nodeID, addr := range httpAddrs {
		if nodeID == restartNodeID {
			continue
		}
		waitForKVValue(t, "http://"+addr, "cluster-key-2", "value-2", 10*time.Second, fmt.Sprintf("node-%d second key before restart", nodeID))
	}

	restarted := mustNewTestApp(t, restartCfg)
	if err := restarted.Start(ctx); err != nil {
		t.Fatalf("restart follower %d: %v", restartNodeID, err)
	}
	apps[restartNodeID] = restarted

	waitForClusterLeader(t, apps, 10*time.Second)
	waitForKVValue(t, "http://"+httpAddrs[restartNodeID], "cluster-key-1", "value-1", 10*time.Second, fmt.Sprintf("node-%d recover first key", restartNodeID))
	waitForKVValue(t, "http://"+httpAddrs[restartNodeID], "cluster-key-2", "value-2", 10*time.Second, fmt.Sprintf("node-%d recover second key", restartNodeID))
}

func TestStarmapdPersistDynamicMemberAddressesAcrossRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	httpAddr := reserveTCPAddress(t)
	adminAddr := reserveTCPAddress(t)
	grpcAddr := reserveTCPAddress(t)
	dataDir := filepath.Join(t.TempDir(), "node-1")

	app := mustNewTestApp(t, daemonConfig{
		NodeID:     1,
		ClusterID:  301,
		DataDir:    dataDir,
		HTTPAddr:   httpAddr,
		AdminAddr:  adminAddr,
		AdminToken: testAdminToken,
		GRPCAddr:   grpcAddr,
	})
	if err := app.Start(ctx); err != nil {
		t.Fatalf("start single node app: %v", err)
	}

	waitForClusterLeader(t, map[uint64]*serverApp{1: app}, 5*time.Second)

	memberRequest := httptransport.MemberChangeRequestDTO{
		NodeID:    2,
		HTTPAddr:  "127.0.0.1:28080",
		GRPCAddr:  "127.0.0.1:29090",
		AdminAddr: "127.0.0.1:28081",
	}
	if err := postJSON("http://"+adminAddr+"/admin/v1/members/add-learner", memberRequest, testAdminToken); err != nil {
		t.Fatalf("add learner failed: %v", err)
	}
	waitForMemberAddress(t, "http://"+adminAddr, testAdminToken, 2, memberRequest.HTTPAddr, memberRequest.GRPCAddr, memberRequest.AdminAddr, 5*time.Second)

	if err := app.Stop(context.Background()); err != nil {
		t.Fatalf("stop first app: %v", err)
	}

	restarted := mustNewTestApp(t, daemonConfig{
		NodeID:     1,
		ClusterID:  301,
		DataDir:    dataDir,
		HTTPAddr:   httpAddr,
		AdminAddr:  adminAddr,
		AdminToken: testAdminToken,
		GRPCAddr:   grpcAddr,
	})
	if err := restarted.Start(ctx); err != nil {
		t.Fatalf("restart single node app: %v", err)
	}
	defer func() {
		if err := restarted.Stop(context.Background()); err != nil {
			t.Fatalf("stop restarted app: %v", err)
		}
	}()

	waitForClusterLeader(t, map[uint64]*serverApp{1: restarted}, 5*time.Second)
	waitForMemberAddress(t, "http://"+adminAddr, testAdminToken, 2, memberRequest.HTTPAddr, memberRequest.GRPCAddr, memberRequest.AdminAddr, 5*time.Second)
}

func TestStarmapdAdminRequiresToken(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	httpAddr := reserveTCPAddress(t)
	adminAddr := reserveTCPAddress(t)
	grpcAddr := reserveTCPAddress(t)

	app := mustNewTestApp(t, daemonConfig{
		NodeID:     1,
		ClusterID:  302,
		DataDir:    filepath.Join(t.TempDir(), "node-1"),
		HTTPAddr:   httpAddr,
		AdminAddr:  adminAddr,
		AdminToken: testAdminToken,
		GRPCAddr:   grpcAddr,
	})
	if err := app.Start(ctx); err != nil {
		t.Fatalf("start single node app: %v", err)
	}
	defer func() {
		if err := app.Stop(context.Background()); err != nil {
			t.Fatalf("stop app: %v", err)
		}
	}()

	waitForAdminStatusCode(t, "http://"+adminAddr+"/admin/v1/status", "", http.StatusUnauthorized, 5*time.Second)
}

func TestAdminAuthMiddlewareRejectsNonLocalhost(t *testing.T) {
	handler := adminAuthMiddleware(testAdminToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	request := httptest.NewRequest(http.MethodGet, "/admin/v1/status", nil)
	request.RemoteAddr = "192.168.1.10:34567"
	request.Header.Set("Authorization", "Bearer "+testAdminToken)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusForbidden, recorder.Code, recorder.Body.String())
	}
}

func TestRegisterAndHeartbeatPersistStructuredRegistryValue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	httpAddr := reserveTCPAddress(t)
	adminAddr := reserveTCPAddress(t)
	grpcAddr := reserveTCPAddress(t)

	app := mustNewTestApp(t, daemonConfig{
		NodeID:     1,
		ClusterID:  303,
		DataDir:    filepath.Join(t.TempDir(), "node-1"),
		HTTPAddr:   httpAddr,
		AdminAddr:  adminAddr,
		AdminToken: testAdminToken,
		GRPCAddr:   grpcAddr,
	})
	if err := app.Start(ctx); err != nil {
		t.Fatalf("start single node app: %v", err)
	}
	defer func() {
		if err := app.Stop(context.Background()); err != nil {
			t.Fatalf("stop app: %v", err)
		}
	}()

	waitForClusterLeader(t, map[uint64]*serverApp{1: app}, 5*time.Second)

	registerRequest := httptransport.RegisterRequestDTO{
		Namespace:  " prod ",
		Service:    " order-service ",
		InstanceID: " order-1 ",
		Zone:       " az1 ",
		Labels: map[string]string{
			"color":   " gray ",
			"version": " v2 ",
		},
		Metadata: map[string]string{
			"owner":     " trade-team ",
			"build_sha": " abc123 ",
		},
		Endpoints: []httptransport.EndpointDTO{
			{
				Protocol: "http",
				Host:     "127.0.0.1",
				Port:     8080,
			},
			{
				Name:     "grpc-main",
				Protocol: "grpc",
				Host:     "127.0.0.1",
				Port:     9090,
				Weight:   80,
			},
		},
	}
	if err := postJSON("http://"+httpAddr+"/api/v1/registry/register", registerRequest, ""); err != nil {
		t.Fatalf("register instance failed: %v", err)
	}

	key := registry.Key("prod", "order-service", "order-1")
	stored := waitForRegistryValue(t, app, key, func(value registry.Value) bool {
		return value.Zone == "az1" && value.LeaseTTLSeconds == registry.DefaultLeaseTTLSeconds && len(value.Endpoints) == 2
	}, 5*time.Second, "register instance")
	if stored.Zone != "az1" {
		t.Fatalf("unexpected zone: %s", stored.Zone)
	}
	if stored.Labels["color"] != "gray" || stored.Labels["version"] != "v2" {
		t.Fatalf("unexpected labels: %+v", stored.Labels)
	}
	if stored.Metadata["owner"] != "trade-team" || stored.Metadata["build_sha"] != "abc123" {
		t.Fatalf("unexpected metadata: %+v", stored.Metadata)
	}
	if len(stored.Endpoints) != 2 {
		t.Fatalf("unexpected endpoints count: %d", len(stored.Endpoints))
	}
	if stored.Endpoints[0].Name != "http" || stored.Endpoints[0].Weight != registry.DefaultEndpointWeight {
		t.Fatalf("unexpected normalized endpoint: %+v", stored.Endpoints[0])
	}
	if stored.LeaseTTLSeconds != registry.DefaultLeaseTTLSeconds {
		t.Fatalf("unexpected default lease ttl: %d", stored.LeaseTTLSeconds)
	}
	registeredAt := stored.RegisteredAtUnix
	lastHeartbeat := stored.LastHeartbeatUnix

	secondRequest := httptransport.RegisterRequestDTO{
		Namespace:  "prod",
		Service:    "order-service",
		InstanceID: "order-2",
		Zone:       "az2",
		Labels: map[string]string{
			"color": "blue",
		},
		Metadata: map[string]string{
			"owner": "checkout-team",
		},
		Endpoints: []httptransport.EndpointDTO{
			{
				Name:     "http",
				Protocol: "http",
				Host:     "127.0.0.1",
				Port:     8081,
			},
		},
		LeaseTTLSeconds: 60,
	}
	if err := postJSON("http://"+httpAddr+"/api/v1/registry/register", secondRequest, ""); err != nil {
		t.Fatalf("register second instance failed: %v", err)
	}

	waitForRegistryValue(t, app, registry.Key("prod", "order-service", "order-2"), func(value registry.Value) bool {
		return value.Zone == "az2" && value.LeaseTTLSeconds == 60
	}, 5*time.Second, "register second instance")

	candidates := waitForRegistryInstances(t, "http://"+httpAddr, url.Values{
		"namespace": []string{"prod"},
		"service":   []string{"order-service"},
		"zone":      []string{"az1"},
		"endpoint":  []string{"http"},
		"selector":  []string{"color=gray,version in (v2),!deprecated"},
		"label":     []string{"color=gray"},
	}, 5*time.Second, func(items []httptransport.RegistryInstanceDTO) bool {
		return len(items) == 1
	})
	if candidates[0].InstanceID != "order-1" {
		t.Fatalf("unexpected candidate instance: %+v", candidates[0])
	}
	if len(candidates[0].Endpoints) != 1 || candidates[0].Endpoints[0].Name != "http" {
		t.Fatalf("unexpected filtered endpoints: %+v", candidates[0].Endpoints)
	}
	if candidates[0].LeaseTTLSeconds != registry.DefaultLeaseTTLSeconds {
		t.Fatalf("unexpected candidate lease ttl: %d", candidates[0].LeaseTTLSeconds)
	}

	time.Sleep(1 * time.Second)
	if err := postJSON("http://"+httpAddr+"/api/v1/registry/heartbeat", httptransport.HeartbeatRequestDTO{
		Namespace:       "prod",
		Service:         "order-service",
		InstanceID:      "order-1",
		LeaseTTLSeconds: 45,
	}, ""); err != nil {
		t.Fatalf("heartbeat failed: %v", err)
	}

	updated := waitForRegistryValue(t, app, key, func(value registry.Value) bool {
		return value.LeaseTTLSeconds == 45 && value.LastHeartbeatUnix > lastHeartbeat
	}, 5*time.Second, "heartbeat update")
	if updated.LeaseTTLSeconds != 45 {
		t.Fatalf("unexpected lease ttl after heartbeat: %d", updated.LeaseTTLSeconds)
	}
	if updated.RegisteredAtUnix != registeredAt {
		t.Fatalf("registered time changed after heartbeat: before=%d after=%d", registeredAt, updated.RegisteredAtUnix)
	}
	if updated.LastHeartbeatUnix <= lastHeartbeat {
		t.Fatalf("last heartbeat did not advance: before=%d after=%d", lastHeartbeat, updated.LastHeartbeatUnix)
	}
	if len(updated.Endpoints) != 2 || updated.Endpoints[0].Name != "http" || updated.Endpoints[1].Name != "grpc-main" {
		t.Fatalf("endpoints changed after heartbeat: %+v", updated.Endpoints)
	}
	if updated.Labels["color"] != "gray" || updated.Metadata["owner"] != "trade-team" {
		t.Fatalf("structured fields lost after heartbeat: labels=%+v metadata=%+v", updated.Labels, updated.Metadata)
	}
}

func TestRegistrySelectorSyntaxAndExpiredCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	httpAddr := reserveTCPAddress(t)
	adminAddr := reserveTCPAddress(t)
	grpcAddr := reserveTCPAddress(t)

	app := mustNewTestApp(t, daemonConfig{
		NodeID:     1,
		ClusterID:  304,
		DataDir:    filepath.Join(t.TempDir(), "node-1"),
		HTTPAddr:   httpAddr,
		AdminAddr:  adminAddr,
		AdminToken: testAdminToken,
		GRPCAddr:   grpcAddr,
	})
	if err := app.Start(ctx); err != nil {
		t.Fatalf("start single node app: %v", err)
	}
	defer func() {
		if err := app.Stop(context.Background()); err != nil {
			t.Fatalf("stop app: %v", err)
		}
	}()

	waitForClusterLeader(t, map[uint64]*serverApp{1: app}, 5*time.Second)

	longLived := httptransport.RegisterRequestDTO{
		Namespace:  "prod",
		Service:    "payment-service",
		InstanceID: "payment-1",
		Zone:       "az1",
		Labels: map[string]string{
			"color":   "gray",
			"version": "v2",
		},
		Endpoints: []httptransport.EndpointDTO{
			{
				Name:     "http",
				Protocol: "http",
				Host:     "127.0.0.1",
				Port:     8080,
			},
			{
				Name:     "grpc",
				Protocol: "grpc",
				Host:     "127.0.0.1",
				Port:     9090,
			},
		},
	}
	if err := postJSON("http://"+httpAddr+"/api/v1/registry/register", longLived, ""); err != nil {
		t.Fatalf("register long lived instance failed: %v", err)
	}

	expiring := httptransport.RegisterRequestDTO{
		Namespace:       "prod",
		Service:         "payment-service",
		InstanceID:      "payment-expired",
		Zone:            "az2",
		LeaseTTLSeconds: 1,
		Labels: map[string]string{
			"color":   "blue",
			"version": "v1",
		},
		Endpoints: []httptransport.EndpointDTO{
			{
				Name:     "http",
				Protocol: "http",
				Host:     "127.0.0.1",
				Port:     8081,
			},
		},
	}
	if err := postJSON("http://"+httpAddr+"/api/v1/registry/register", expiring, ""); err != nil {
		t.Fatalf("register expiring instance failed: %v", err)
	}

	selected := waitForRegistryInstances(t, "http://"+httpAddr, url.Values{
		"namespace": []string{"prod"},
		"service":   []string{"payment-service"},
		"endpoint":  []string{"http"},
		"selector":  []string{"color=gray,version in (v2),!deprecated"},
	}, 5*time.Second, func(items []httptransport.RegistryInstanceDTO) bool {
		return len(items) == 1 && items[0].InstanceID == "payment-1"
	})
	if len(selected[0].Endpoints) != 1 || selected[0].Endpoints[0].Name != "http" {
		t.Fatalf("unexpected selected endpoints: %+v", selected[0].Endpoints)
	}

	waitForRegistryValueDeleted(t, app, registry.Key("prod", "payment-service", "payment-expired"), 8*time.Second, "expired registry cleanup")

	waitForRegistryInstances(t, "http://"+httpAddr, url.Values{
		"namespace": []string{"prod"},
		"service":   []string{"payment-service"},
	}, 5*time.Second, func(items []httptransport.RegistryInstanceDTO) bool {
		return len(items) == 1 && items[0].InstanceID == "payment-1"
	})
}

func TestCleanupExpiredRegistryRespectsDeleteLimitAndAdvancesScanCursor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	httpAddr := reserveTCPAddress(t)
	adminAddr := reserveTCPAddress(t)
	grpcAddr := reserveTCPAddress(t)

	app := mustNewTestApp(t, daemonConfig{
		NodeID:                     1,
		ClusterID:                  305,
		DataDir:                    filepath.Join(t.TempDir(), "node-1"),
		HTTPAddr:                   httpAddr,
		AdminAddr:                  adminAddr,
		AdminToken:                 testAdminToken,
		GRPCAddr:                   grpcAddr,
		RegistryCleanupInterval:    time.Hour,
		RegistryCleanupDeleteLimit: 1,
	})
	if err := app.Start(ctx); err != nil {
		t.Fatalf("start single node app: %v", err)
	}
	defer func() {
		if err := app.Stop(context.Background()); err != nil {
			t.Fatalf("stop app: %v", err)
		}
	}()

	waitForClusterLeader(t, map[uint64]*serverApp{1: app}, 5*time.Second)

	now := time.Now().Unix()
	alive := registry.Value{
		Namespace:         "prod",
		Service:           "payment-service",
		InstanceID:        "a-live",
		Endpoints:         nil,
		LeaseTTLSeconds:   30,
		RegisteredAtUnix:  now,
		LastHeartbeatUnix: now,
	}
	expiredFirst := registry.Value{
		Namespace:         "prod",
		Service:           "payment-service",
		InstanceID:        "b-expired",
		Endpoints:         nil,
		LeaseTTLSeconds:   1,
		RegisteredAtUnix:  now - 10,
		LastHeartbeatUnix: now - 10,
	}
	expiredSecond := registry.Value{
		Namespace:         "prod",
		Service:           "payment-service",
		InstanceID:        "c-expired",
		Endpoints:         nil,
		LeaseTTLSeconds:   1,
		RegisteredAtUnix:  now - 10,
		LastHeartbeatUnix: now - 10,
	}

	mustPutTestRegistryValue(t, app, alive)
	mustPutTestRegistryValue(t, app, expiredFirst)
	mustPutTestRegistryValue(t, app, expiredSecond)

	runCleanupRound(t, app)
	mustReadRegistryValue(t, app, registry.Key(alive.Namespace, alive.Service, alive.InstanceID))
	mustReadRegistryValue(t, app, registry.Key(expiredFirst.Namespace, expiredFirst.Service, expiredFirst.InstanceID))
	mustReadRegistryValue(t, app, registry.Key(expiredSecond.Namespace, expiredSecond.Service, expiredSecond.InstanceID))

	runCleanupRound(t, app)
	waitForRegistryValueDeleted(t, app, registry.Key(expiredFirst.Namespace, expiredFirst.Service, expiredFirst.InstanceID), 5*time.Second, "first expired registry cleanup")
	mustReadRegistryValue(t, app, registry.Key(expiredSecond.Namespace, expiredSecond.Service, expiredSecond.InstanceID))

	runCleanupRound(t, app)
	waitForRegistryValueDeleted(t, app, registry.Key(expiredSecond.Namespace, expiredSecond.Service, expiredSecond.InstanceID), 5*time.Second, "second expired registry cleanup")
	mustReadRegistryValue(t, app, registry.Key(alive.Namespace, alive.Service, alive.InstanceID))
}

func mustNewTestApp(t *testing.T, cfg daemonConfig) *serverApp {
	t.Helper()

	app, err := newServerApp(cfg)
	if err != nil {
		t.Fatalf("new server app failed: %v", err)
	}

	return app
}

func mustReadRegistryValue(t *testing.T, app *serverApp, key []byte) registry.Value {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	data, err := app.Node().Get(ctx, key)
	if err != nil {
		t.Fatalf("read registry value failed: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("registry value for key=%s is empty", string(key))
	}

	var value registry.Value
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("unmarshal registry value failed: %v", err)
	}

	return value
}

func mustPutTestRegistryValue(t *testing.T, app *serverApp, value registry.Value) {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal registry value failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := registry.Key(value.Namespace, value.Service, value.InstanceID)
	if err := app.Node().ProposeCommand(ctx, storage.Command{
		Operation: storage.OperationPut,
		Key:       key,
		Value:     data,
	}); err != nil {
		t.Fatalf("write registry value failed: %v", err)
	}

	waitForRegistryValue(t, app, key, func(current registry.Value) bool {
		return current.InstanceID == value.InstanceID
	}, 5*time.Second, "write registry value")
}

func runCleanupRound(t *testing.T, app *serverApp) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := app.CleanupExpiredRegistry(ctx); err != nil {
		t.Fatalf("cleanup expired registry failed: %v", err)
	}
}

func waitForRegistryValue(t *testing.T, app *serverApp, key []byte, predicate func(registry.Value) bool, timeout time.Duration, scene string) registry.Value {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var last registry.Value
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		data, err := app.Node().Get(ctx, key)
		cancel()
		if err == nil && len(data) > 0 {
			if err := json.Unmarshal(data, &last); err != nil {
				t.Fatalf("unmarshal registry value failed: %v", err)
			}
		}
		if len(data) > 0 && predicate(last) {
			return last
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("%s did not observe expected registry value within %s, last=%+v", scene, timeout, last)
	return registry.Value{}
}

func waitForRegistryValueDeleted(t *testing.T, app *serverApp, key []byte, timeout time.Duration, scene string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		data, err := app.Node().Get(ctx, key)
		cancel()
		if err == nil && len(data) == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("%s did not delete registry key=%s within %s", scene, string(key), timeout)
}

func stopApps(t *testing.T, apps map[uint64]*serverApp) {
	t.Helper()

	for nodeID, app := range apps {
		if app == nil {
			continue
		}
		stopCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
		if err := app.Stop(stopCtx); err != nil {
			cancel()
			t.Fatalf("stop node %d failed: %v", nodeID, err)
		}
		cancel()
	}
}

func waitForClusterLeader(t *testing.T, apps map[uint64]*serverApp, timeout time.Duration) uint64 {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var leaderID uint64
		allReady := true

		for _, app := range apps {
			status := app.Node().Status()
			if status.LeaderID == 0 {
				allReady = false
				break
			}
			if leaderID == 0 {
				leaderID = status.LeaderID
			}
			if leaderID != status.LeaderID {
				allReady = false
				break
			}
		}

		if allReady && leaderID != 0 {
			leaderApp, ok := apps[leaderID]
			if ok {
				if leaderApp.Node().Status().Role == raftnode.RoleLeader {
					return leaderID
				}
			}
		}

		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("cluster did not elect a stable leader within %s", timeout)
	return 0
}

func putKV(baseURL, key string, value []byte) error {
	request, err := http.NewRequest(http.MethodPut, strings.TrimRight(baseURL, "/")+"/api/v1/kv/"+key, bytes.NewReader(value))
	if err != nil {
		return err
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		return fmt.Errorf("status=%d body=%s", response.StatusCode, string(body))
	}

	return nil
}

func waitForKVValue(t *testing.T, baseURL, key, expected string, timeout time.Duration, scene string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		value, statusCode, err := getKV(baseURL, key)
		if err == nil && statusCode == http.StatusOK && value == expected {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("%s did not observe key=%s value=%s within %s", scene, key, expected, timeout)
}

func waitForMemberAddress(t *testing.T, baseURL, token string, nodeID uint64, httpAddr, grpcAddr, adminAddr string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, err := getClusterStatus(baseURL, token)
		if err == nil {
			if status.HTTPAddrs[nodeID] == httpAddr && status.GRPCAddrs[nodeID] == grpcAddr && status.AdminAddrs[nodeID] == adminAddr {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("member address nodeId=%d http=%s grpc=%s admin=%s not observed within %s", nodeID, httpAddr, grpcAddr, adminAddr, timeout)
}

func getKV(baseURL, key string) (string, int, error) {
	response, err := http.Get(strings.TrimRight(baseURL, "/") + "/api/v1/kv/" + key)
	if err != nil {
		return "", 0, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", response.StatusCode, err
	}
	if response.StatusCode != http.StatusOK {
		return "", response.StatusCode, fmt.Errorf("status=%d body=%s", response.StatusCode, string(body))
	}

	var envelope testSuccessEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", response.StatusCode, err
	}
	var kv httptransport.KVResponseDTO
	if err := json.Unmarshal(envelope.Data, &kv); err != nil {
		return "", response.StatusCode, err
	}

	return string(kv.Value), response.StatusCode, nil
}

func getClusterStatus(baseURL, token string) (httptransport.ClusterStatusDTO, error) {
	request, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/admin/v1/status", nil)
	if err != nil {
		return httptransport.ClusterStatusDTO{}, err
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return httptransport.ClusterStatusDTO{}, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return httptransport.ClusterStatusDTO{}, err
	}
	if response.StatusCode != http.StatusOK {
		return httptransport.ClusterStatusDTO{}, fmt.Errorf("status=%d body=%s", response.StatusCode, string(body))
	}

	var envelope testSuccessEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return httptransport.ClusterStatusDTO{}, err
	}
	var status httptransport.ClusterStatusDTO
	if err := json.Unmarshal(envelope.Data, &status); err != nil {
		return httptransport.ClusterStatusDTO{}, err
	}

	return status, nil
}

func getRegistryInstances(baseURL string, values url.Values) ([]httptransport.RegistryInstanceDTO, error) {
	target := strings.TrimRight(baseURL, "/") + "/api/v1/registry/instances"
	if encoded := values.Encode(); encoded != "" {
		target += "?" + encoded
	}

	response, err := http.Get(target)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status=%d body=%s", response.StatusCode, string(body))
	}

	var envelope testSuccessEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	var instances []httptransport.RegistryInstanceDTO
	if err := json.Unmarshal(envelope.Data, &instances); err != nil {
		return nil, err
	}

	return instances, nil
}

func waitForRegistryInstances(t *testing.T, baseURL string, params url.Values, timeout time.Duration, predicate func([]httptransport.RegistryInstanceDTO) bool) []httptransport.RegistryInstanceDTO {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var last []httptransport.RegistryInstanceDTO
	var lastErr error
	for time.Now().Before(deadline) {
		last, lastErr = getRegistryInstances(baseURL, params)
		if lastErr == nil && predicate(last) {
			return last
		}
		time.Sleep(50 * time.Millisecond)
	}

	if lastErr != nil {
		t.Fatalf("query registry instances failed within %s: %v", timeout, lastErr)
	}
	t.Fatalf("query registry instances did not match predicate within %s, last=%+v", timeout, last)
	return nil
}

func postJSON(target string, body interface{}, token string) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	request, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(response.Body)
		return fmt.Errorf("status=%d body=%s", response.StatusCode, string(raw))
	}

	return nil
}

func rawGet(target, token string) (int, string, error) {
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return 0, "", err
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return response.StatusCode, "", err
	}

	return response.StatusCode, string(body), nil
}

func waitForAdminStatusCode(t *testing.T, target, token string, expectedStatus int, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var lastBody string
	var lastErr error
	var lastStatus int

	for time.Now().Before(deadline) {
		statusCode, body, err := rawGet(target, token)
		lastBody = body
		lastErr = err
		lastStatus = statusCode
		if err == nil && statusCode == expectedStatus {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	if lastErr != nil {
		t.Fatalf("admin endpoint %s did not return status=%d within %s, last error=%v", target, expectedStatus, timeout, lastErr)
	}
	t.Fatalf("admin endpoint %s did not return status=%d within %s, last status=%d body=%s", target, expectedStatus, timeout, lastStatus, lastBody)
}

func pickFollower(apps map[uint64]*serverApp, leaderID uint64) uint64 {
	for nodeID := range apps {
		if nodeID != leaderID {
			return nodeID
		}
	}

	return 0
}

func joinAddressMap(items map[uint64]string) string {
	parts := make([]string, 0, len(items))
	for nodeID := uint64(1); nodeID <= uint64(len(items)); nodeID++ {
		if addr, ok := items[nodeID]; ok {
			parts = append(parts, fmt.Sprintf("%d=%s", nodeID, addr))
		}
	}

	return strings.Join(parts, ",")
}

func reserveTCPAddress(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve tcp address failed: %v", err)
	}
	defer listener.Close()

	return listener.Addr().String()
}
