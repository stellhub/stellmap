package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseFlagsLoadsTOMLAndAllowsCLIOverride(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "starmapd.toml")
	content := `
[node]
id = 1
cluster_id = 100
region = "cn-sh"
data_dir = "/data/from-file"

[server]
http_addr = "10.0.0.11:8080"
admin_addr = "127.0.0.1:18080"
grpc_addr = "10.0.0.11:19090"

[auth]
admin_token = "file-admin"
replication_token = "file-repl"
prometheus_sd_token = "file-prom"

[cluster]
peer_ids = "1,2,3"
peer_grpc_addrs = "1=10.0.0.11:19090,2=10.0.0.12:19090,3=10.0.0.13:19090"
peer_http_addrs = "1=10.0.0.11:8080,2=10.0.0.12:8080,3=10.0.0.13:8080"
peer_admin_addrs = "1=127.0.0.1:18080,2=127.0.0.1:18080,3=127.0.0.1:18080"

[runtime]
request_timeout = "4s"
shutdown_timeout = "9s"

[registry]
cleanup_interval = "3s"
cleanup_delete_limit = 64

[replication]
targets_file = "/etc/starmapd/targets.json"
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config file failed: %v", err)
	}

	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()
	os.Args = []string{
		"starmapd",
		"--config", configPath,
		"--http-addr", "0.0.0.0:28080",
		"--admin-token", "cli-admin",
		"--prometheus-sd-token", "cli-prom",
		"--request-timeout", "8s",
	}

	cfg, err := parseFlags()
	if err != nil {
		t.Fatalf("parse flags failed: %v", err)
	}
	if cfg.NodeID != 1 || cfg.ClusterID != 100 {
		t.Fatalf("expected file values to load, got %+v", cfg)
	}
	if cfg.HTTPAddr != "0.0.0.0:28080" {
		t.Fatalf("expected cli http addr override, got %s", cfg.HTTPAddr)
	}
	if cfg.AdminToken != "cli-admin" {
		t.Fatalf("expected cli admin token override, got %s", cfg.AdminToken)
	}
	if cfg.PrometheusSDToken != "cli-prom" {
		t.Fatalf("expected cli prometheus sd token override, got %s", cfg.PrometheusSDToken)
	}
	if cfg.ReplicationToken != "file-repl" {
		t.Fatalf("expected replication token from file, got %s", cfg.ReplicationToken)
	}
	if cfg.RequestTimeout.String() != "8s" {
		t.Fatalf("expected cli request timeout override, got %s", cfg.RequestTimeout)
	}
	if cfg.ShutdownTimeout.String() != "9s" {
		t.Fatalf("expected shutdown timeout from file, got %s", cfg.ShutdownTimeout)
	}
}
