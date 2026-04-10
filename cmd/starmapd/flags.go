package main

import (
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/chenwenlong-java/StarMap/internal/raftnode"
)

// parseFlags 解析命令行参数，构造 daemonConfig。
//
// 这里不做复杂校验，真正的字段合法性校验放到 newServerApp 中统一处理。
func parseFlags() daemonConfig {
	var cfg daemonConfig

	flag.Uint64Var(&cfg.NodeID, "node-id", 1, "当前节点 ID")
	flag.Uint64Var(&cfg.ClusterID, "cluster-id", 1, "集群 ID")
	flag.StringVar(&cfg.DataDir, "data-dir", "", "节点数据目录，留空时默认使用 ./data/node-{node-id}")
	flag.StringVar(&cfg.HTTPAddr, "http-addr", defaultHTTPAddr, "对外 HTTP 监听地址")
	flag.StringVar(&cfg.AdminAddr, "admin-addr", defaultAdminAddr, "独立 admin HTTP 监听地址，默认仅绑定本地回环地址")
	flag.StringVar(&cfg.AdminToken, "admin-token", "", "admin HTTP 固定鉴权 token；留空时尝试读取环境变量 STARMAP_ADMIN_TOKEN")
	flag.StringVar(&cfg.GRPCAddr, "grpc-addr", defaultGRPCAddr, "对内 gRPC 监听地址")
	flag.StringVar(&cfg.PeerIDs, "peer-ids", "", "集群节点 ID 列表，逗号分隔，例如 1,2,3")
	flag.StringVar(&cfg.PeerGRPCAddrs, "peer-grpc-addrs", "", "节点 gRPC 地址映射，格式 1=127.0.0.1:19090,2=127.0.0.1:19091")
	flag.StringVar(&cfg.PeerHTTPAddrs, "peer-http-addrs", "", "节点 HTTP 地址映射，格式 1=127.0.0.1:8080,2=127.0.0.1:8081")
	flag.StringVar(&cfg.PeerAdminAddrs, "peer-admin-addrs", "", "节点 admin 地址映射，格式 1=127.0.0.1:18080,2=127.0.0.1:18081")
	flag.DurationVar(&cfg.RegistryCleanupInterval, "registry-cleanup-interval", defaultRegistryCleanupInterval, "Leader 后台过期实例清理扫描间隔，例如 1s、5s、1m")
	flag.IntVar(&cfg.RegistryCleanupDeleteLimit, "registry-cleanup-delete-limit", defaultRegistryCleanupDeleteLimit, "Leader 后台过期实例清理单轮最多处理多少个实例键")
	flag.Parse()

	return cfg
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
