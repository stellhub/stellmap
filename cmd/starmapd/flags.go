package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	daemonapp "github.com/stellaraxis/starmap/internal/app"
	"github.com/stellaraxis/starmap/internal/raftnode"
)

// parseFlags 解析命令行参数，并在需要时合并 TOML 配置文件。
//
// 优先级规则：
// 1. 命令行参数
// 2. TOML 配置文件
// 3. 调用方必须显式提供的剩余字段
func parseFlags() (daemonConfig, error) {
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	var (
		configPath string
		cli        daemonConfig
	)

	fs.StringVar(&configPath, "config", "", "starmapd TOML 配置文件路径")
	fs.Uint64Var(&cli.NodeID, "node-id", 0, "当前节点 ID")
	fs.Uint64Var(&cli.ClusterID, "cluster-id", 0, "集群 ID")
	fs.StringVar(&cli.Region, "region", "", "当前节点所属 region，例如 cn-sh、cn-bj")
	fs.StringVar(&cli.DataDir, "data-dir", "", "节点数据目录")
	fs.StringVar(&cli.HTTPAddr, "http-addr", "", "对外 HTTP 监听地址")
	fs.StringVar(&cli.AdminAddr, "admin-addr", "", "独立 admin HTTP 监听地址")
	fs.StringVar(&cli.AdminToken, "admin-token", "", "admin HTTP 固定鉴权 token；命令行优先级高于配置文件")
	fs.StringVar(&cli.ReplicationToken, "replication-token", "", "内部 replication watch 固定鉴权 token；命令行优先级高于配置文件")
	fs.StringVar(&cli.PrometheusSDToken, "prometheus-sd-token", "", "Prometheus HTTP SD 固定鉴权 token；命令行优先级高于配置文件")
	fs.StringVar(&cli.ReplicationTargetsFile, "replication-targets-file", "", "跨 region 目录同步目标配置文件路径，建议使用 JSON 数组结构")
	fs.StringVar(&cli.GRPCAddr, "grpc-addr", "", "对内 gRPC 监听地址")
	fs.StringVar(&cli.PeerIDs, "peer-ids", "", "集群节点 ID 列表，逗号分隔，例如 1,2,3")
	fs.StringVar(&cli.PeerGRPCAddrs, "peer-grpc-addrs", "", "节点 gRPC 地址映射，格式 1=127.0.0.1:19090,2=127.0.0.1:19091")
	fs.StringVar(&cli.PeerHTTPAddrs, "peer-http-addrs", "", "节点 HTTP 地址映射，格式 1=127.0.0.1:8080,2=127.0.0.1:8081")
	fs.StringVar(&cli.PeerAdminAddrs, "peer-admin-addrs", "", "节点 admin 地址映射，格式 1=127.0.0.1:18080,2=127.0.0.1:18081")
	fs.DurationVar(&cli.RequestTimeout, "request-timeout", 0, "单次请求处理超时，例如 5s")
	fs.DurationVar(&cli.ShutdownTimeout, "shutdown-timeout", 0, "服务优雅退出等待超时，例如 10s")
	fs.DurationVar(&cli.RegistryCleanupInterval, "registry-cleanup-interval", 0, "Leader 后台过期实例清理扫描间隔，例如 1s、5s、1m")
	fs.IntVar(&cli.RegistryCleanupDeleteLimit, "registry-cleanup-delete-limit", 0, "Leader 后台过期实例清理单轮最多处理多少个实例键")

	if err := fs.Parse(os.Args[1:]); err != nil {
		return daemonConfig{}, err
	}

	cfg := daemonConfig{}
	if strings.TrimSpace(configPath) != "" {
		loaded, err := daemonapp.LoadConfigFile(configPath)
		if err != nil {
			return daemonConfig{}, err
		}
		cfg = loaded
	}

	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "node-id":
			cfg.NodeID = cli.NodeID
		case "cluster-id":
			cfg.ClusterID = cli.ClusterID
		case "region":
			cfg.Region = cli.Region
		case "data-dir":
			cfg.DataDir = cli.DataDir
		case "http-addr":
			cfg.HTTPAddr = cli.HTTPAddr
		case "admin-addr":
			cfg.AdminAddr = cli.AdminAddr
		case "admin-token":
			cfg.AdminToken = cli.AdminToken
		case "replication-token":
			cfg.ReplicationToken = cli.ReplicationToken
		case "prometheus-sd-token":
			cfg.PrometheusSDToken = cli.PrometheusSDToken
		case "replication-targets-file":
			cfg.ReplicationTargetsFile = cli.ReplicationTargetsFile
		case "grpc-addr":
			cfg.GRPCAddr = cli.GRPCAddr
		case "peer-ids":
			cfg.PeerIDs = cli.PeerIDs
		case "peer-grpc-addrs":
			cfg.PeerGRPCAddrs = cli.PeerGRPCAddrs
		case "peer-http-addrs":
			cfg.PeerHTTPAddrs = cli.PeerHTTPAddrs
		case "peer-admin-addrs":
			cfg.PeerAdminAddrs = cli.PeerAdminAddrs
		case "request-timeout":
			cfg.RequestTimeout = cli.RequestTimeout
		case "shutdown-timeout":
			cfg.ShutdownTimeout = cli.ShutdownTimeout
		case "registry-cleanup-interval":
			cfg.RegistryCleanupInterval = cli.RegistryCleanupInterval
		case "registry-cleanup-delete-limit":
			cfg.RegistryCleanupDeleteLimit = cli.RegistryCleanupDeleteLimit
		}
	})

	return cfg, nil
}

// parsePeerIDs 解析 `--peer-ids`，并确保当前节点一定出现在 peers 列表中。
//
// 即使用户漏掉了自己的 node-id，这里也会自动补上，避免单节点或多节点启动时配置不完整。
func parsePeerIDs(selfID uint64, raw string) ([]raftnode.Peer, error) {
	items := strings.Split(raw, ",")
	peers := make([]raftnode.Peer, 0, len(items)+1)
	seen := make(map[uint64]struct{})
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		id, err := strconv.ParseUint(item, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse peer id %q: %w", item, err)
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		peers = append(peers, raftnode.Peer{ID: id})
	}
	if _, ok := seen[selfID]; !ok {
		peers = append(peers, raftnode.Peer{ID: selfID})
	}

	return peers, nil
}

// parseAddressMap 解析 `1=host1,2=host2` 这样的地址映射字符串。
func parseAddressMap(raw string) (map[uint64]string, error) {
	result := make(map[uint64]string)
	if strings.TrimSpace(raw) == "" {
		return result, nil
	}

	items := strings.Split(raw, ",")
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid address mapping %q", item)
		}
		id, err := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse node id %q: %w", parts[0], err)
		}
		result[id] = strings.TrimSpace(parts[1])
	}

	return result, nil
}
