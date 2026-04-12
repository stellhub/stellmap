package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type configFile struct {
	Node struct {
		ID        uint64 `toml:"id"`
		ClusterID uint64 `toml:"cluster_id"`
		Region    string `toml:"region"`
		DataDir   string `toml:"data_dir"`
	} `toml:"node"`
	Server struct {
		HTTPAddr  string `toml:"http_addr"`
		AdminAddr string `toml:"admin_addr"`
		GRPCAddr  string `toml:"grpc_addr"`
	} `toml:"server"`
	Auth struct {
		AdminToken        string `toml:"admin_token"`
		ReplicationToken  string `toml:"replication_token"`
		PrometheusSDToken string `toml:"prometheus_sd_token"`
	} `toml:"auth"`
	Cluster struct {
		PeerIDs        string `toml:"peer_ids"`
		PeerGRPCAddrs  string `toml:"peer_grpc_addrs"`
		PeerHTTPAddrs  string `toml:"peer_http_addrs"`
		PeerAdminAddrs string `toml:"peer_admin_addrs"`
	} `toml:"cluster"`
	Registry struct {
		CleanupInterval    string `toml:"cleanup_interval"`
		CleanupDeleteLimit int    `toml:"cleanup_delete_limit"`
	} `toml:"registry"`
	Runtime struct {
		RequestTimeout  string `toml:"request_timeout"`
		ShutdownTimeout string `toml:"shutdown_timeout"`
	} `toml:"runtime"`
	Replication struct {
		TargetsFile string `toml:"targets_file"`
	} `toml:"replication"`
}

// LoadConfigFile 从 TOML 文件加载 starmapd 配置。
func LoadConfigFile(path string) (Config, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Config{}, fmt.Errorf("config path is required")
	}

	var raw configFile
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		return Config{}, fmt.Errorf("decode config file %q: %w", path, err)
	}

	cfg := Config{
		NodeID:                     raw.Node.ID,
		ClusterID:                  raw.Node.ClusterID,
		Region:                     strings.TrimSpace(raw.Node.Region),
		DataDir:                    strings.TrimSpace(raw.Node.DataDir),
		HTTPAddr:                   strings.TrimSpace(raw.Server.HTTPAddr),
		AdminAddr:                  strings.TrimSpace(raw.Server.AdminAddr),
		GRPCAddr:                   strings.TrimSpace(raw.Server.GRPCAddr),
		AdminToken:                 strings.TrimSpace(raw.Auth.AdminToken),
		ReplicationToken:           strings.TrimSpace(raw.Auth.ReplicationToken),
		PrometheusSDToken:          strings.TrimSpace(raw.Auth.PrometheusSDToken),
		PeerIDs:                    strings.TrimSpace(raw.Cluster.PeerIDs),
		PeerGRPCAddrs:              strings.TrimSpace(raw.Cluster.PeerGRPCAddrs),
		PeerHTTPAddrs:              strings.TrimSpace(raw.Cluster.PeerHTTPAddrs),
		PeerAdminAddrs:             strings.TrimSpace(raw.Cluster.PeerAdminAddrs),
		ReplicationTargetsFile:     strings.TrimSpace(raw.Replication.TargetsFile),
		RegistryCleanupDeleteLimit: raw.Registry.CleanupDeleteLimit,
	}

	if strings.TrimSpace(raw.Registry.CleanupInterval) != "" {
		duration, err := time.ParseDuration(strings.TrimSpace(raw.Registry.CleanupInterval))
		if err != nil {
			return Config{}, fmt.Errorf("parse registry cleanup interval: %w", err)
		}
		cfg.RegistryCleanupInterval = duration
	}
	if strings.TrimSpace(raw.Runtime.RequestTimeout) != "" {
		duration, err := time.ParseDuration(strings.TrimSpace(raw.Runtime.RequestTimeout))
		if err != nil {
			return Config{}, fmt.Errorf("parse request timeout: %w", err)
		}
		cfg.RequestTimeout = duration
	}
	if strings.TrimSpace(raw.Runtime.ShutdownTimeout) != "" {
		duration, err := time.ParseDuration(strings.TrimSpace(raw.Runtime.ShutdownTimeout))
		if err != nil {
			return Config{}, fmt.Errorf("parse shutdown timeout: %w", err)
		}
		cfg.ShutdownTimeout = duration
	}

	return cfg, nil
}
