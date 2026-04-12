package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateConfigRequiresExplicitFields(t *testing.T) {
	_, err := ValidateConfig(Config{
		NodeID:    1,
		ClusterID: 100,
	})
	if err == nil {
		t.Fatalf("expected validate config to fail when required fields are missing")
	}
}

func TestParseReplicationTargetsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replication-targets.json")
	content := `[
  {
    "sourceRegion": "cn-bj",
    "sourceClusterId": "bj-prod-01",
    "baseURL": "http://127.0.0.1:8080",
    "services": [
      {"namespace": "prod", "service": "order-service"},
      {"namespace": "prod", "service": "payment-service"}
    ]
  }
]`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write targets file failed: %v", err)
	}

	targets, err := ParseReplicationTargetsFile(path)
	if err != nil {
		t.Fatalf("parse targets file failed: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0].SourceRegion != "cn-bj" || targets[0].SourceClusterID != "bj-prod-01" {
		t.Fatalf("unexpected target source: %+v", targets[0])
	}
	if len(targets[0].Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(targets[0].Services))
	}
}

func TestLoadConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "starmapd.toml")
	content := `
[node]
id = 1
cluster_id = 100
region = "cn-sh"
data_dir = "/data/starmap/node-1"

[server]
http_addr = "0.0.0.0:8080"
admin_addr = "127.0.0.1:18080"
grpc_addr = "0.0.0.0:19090"

[auth]
admin_token = "admin-token"
replication_token = "replication-token"
prometheus_sd_token = "prom-sd-token"

[cluster]
peer_ids = "1,2,3"
peer_grpc_addrs = "1=10.0.0.11:19090,2=10.0.0.12:19090,3=10.0.0.13:19090"
peer_http_addrs = "1=10.0.0.11:8080,2=10.0.0.12:8080,3=10.0.0.13:8080"
peer_admin_addrs = "1=127.0.0.1:18080,2=127.0.0.1:18080,3=127.0.0.1:18080"

[runtime]
request_timeout = "6s"
shutdown_timeout = "12s"

[registry]
cleanup_interval = "5s"
cleanup_delete_limit = 256

[replication]
targets_file = "/etc/starmapd/replication-targets.json"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config file failed: %v", err)
	}

	cfg, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("load config file failed: %v", err)
	}
	if cfg.NodeID != 1 || cfg.ClusterID != 100 || cfg.Region != "cn-sh" {
		t.Fatalf("unexpected node config: %+v", cfg)
	}
	if cfg.AdminToken != "admin-token" || cfg.ReplicationToken != "replication-token" || cfg.PrometheusSDToken != "prom-sd-token" {
		t.Fatalf("unexpected auth config: %+v", cfg)
	}
	if cfg.RegistryCleanupDeleteLimit != 256 || cfg.RegistryCleanupInterval.String() != "5s" {
		t.Fatalf("unexpected registry config: %+v", cfg)
	}
	if cfg.RequestTimeout.String() != "6s" || cfg.ShutdownTimeout.String() != "12s" {
		t.Fatalf("unexpected runtime config: %+v", cfg)
	}
	if cfg.ReplicationTargetsFile != "/etc/starmapd/replication-targets.json" {
		t.Fatalf("unexpected replication targets file: %s", cfg.ReplicationTargetsFile)
	}
}
