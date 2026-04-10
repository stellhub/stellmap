package main

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	daemonapp "github.com/chenwenlong-java/StarMap/internal/app"
	"github.com/chenwenlong-java/StarMap/internal/raftnode"
	"github.com/chenwenlong-java/StarMap/internal/runtime"
	"github.com/chenwenlong-java/StarMap/internal/snapshot"
	grpctransport "github.com/chenwenlong-java/StarMap/internal/transport/grpc"
	httptransport "github.com/chenwenlong-java/StarMap/internal/transport/http"
	"google.golang.org/grpc"
)

type daemonConfig = daemonapp.Config
type serverApp = daemonapp.App

const (
	defaultHTTPAddr                   = daemonapp.DefaultHTTPAddr
	defaultAdminAddr                  = daemonapp.DefaultAdminAddr
	defaultGRPCAddr                   = daemonapp.DefaultGRPCAddr
	defaultShutdownTimeout            = daemonapp.DefaultShutdownTimeout
	defaultRequestTimeout             = daemonapp.DefaultRequestTimeout
	defaultRegistryCleanupInterval    = daemonapp.DefaultRegistryCleanupInterval
	defaultRegistryCleanupDeleteLimit = daemonapp.DefaultRegistryCleanupDeleteLimit
	adminTokenEnvKey                  = daemonapp.AdminTokenEnvKey
)

// main 是 starmapd 的服务进程入口。
//
// 启动前提：
// 1. 必须通过 `--admin-token` 或环境变量 `STARMAP_ADMIN_TOKEN` 提供控制面固定鉴权 token。
// 2. admin listener 当前默认只允许 `127.0.0.1` 访问，因此控制面运维通常需要在节点本机执行。
//
// 单节点启动示例：
//
//	go run ./cmd/starmapd --node-id=1 --cluster-id=100 --data-dir=./data/node-1 --http-addr=:8080 --admin-addr=127.0.0.1:18080 --admin-token=your-admin-token --grpc-addr=:19090
//
// 三节点启动示例中的单个节点：
//
//	go run ./cmd/starmapd --node-id=1 --cluster-id=100 --data-dir=./data/node-1 --http-addr=:8080 --admin-addr=127.0.0.1:18080 --admin-token=your-admin-token --grpc-addr=:19090 --peer-ids=1,2,3 --peer-grpc-addrs=1=127.0.0.1:19090,2=127.0.0.1:19091,3=127.0.0.1:19092 --peer-http-addrs=1=127.0.0.1:8080,2=127.0.0.1:8081,3=127.0.0.1:8082 --peer-admin-addrs=1=127.0.0.1:18080,2=127.0.0.1:18081,3=127.0.0.1:18082
//
// 当前职责：
// 1. 解析启动配置。
// 2. 装配 raftnode、runtime、grpc/http server。
// 3. 启动应用并处理优雅退出。
func main() {
	cfg := parseFlags()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	app, err := newServerApp(cfg)
	if err != nil {
		log.Fatalf("create starmapd failed: %v", err)
	}

	if err := app.Start(ctx); err != nil {
		log.Fatalf("start starmapd failed: %v", err)
	}

	log.Printf("starmapd started, node_id=%d cluster_id=%d http=%s admin=%s grpc=%s data_dir=%s", cfg.NodeID, cfg.ClusterID, cfg.HTTPAddr, cfg.AdminAddr, cfg.GRPCAddr, cfg.DataDir)

	select {
	case <-ctx.Done():
		log.Printf("shutdown signal received")
	case err := <-app.Errors():
		if err != nil {
			log.Printf("server error: %v", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
	defer cancel()
	if err := app.Stop(shutdownCtx); err != nil {
		log.Fatalf("stop starmapd failed: %v", err)
	}
}

func newServerApp(cfg daemonConfig) (*serverApp, error) {
	validated, err := daemonapp.ValidateConfig(cfg)
	if err != nil {
		return nil, err
	}

	peers, err := parsePeerIDs(validated.NodeID, validated.PeerIDs)
	if err != nil {
		return nil, err
	}
	httpAddrByID, err := parseAddressMap(validated.PeerHTTPAddrs)
	if err != nil {
		return nil, err
	}
	grpcAddrByID, err := parseAddressMap(validated.PeerGRPCAddrs)
	if err != nil {
		return nil, err
	}
	adminAddrByID, err := parseAddressMap(validated.PeerAdminAddrs)
	if err != nil {
		return nil, err
	}

	book := runtime.NewAddressBook(httpAddrByID, grpcAddrByID, adminAddrByID)
	book.Set(validated.NodeID, validated.HTTPAddr, validated.GRPCAddr, validated.AdminAddr)

	node, err := raftnode.New(raftnode.Config{
		NodeID:    validated.NodeID,
		ClusterID: validated.ClusterID,
		DataDir:   validated.DataDir,
		Peers:     peers,
	})
	if err != nil {
		return nil, err
	}

	internalService := runtime.NewInternalTransportService(node, snapshot.NewFileStore(filepath.Join(validated.DataDir, "snapshot")))
	grpcServer := grpc.NewServer()
	grpctransport.NewServer(internalService).RegisterHandlers(grpcServer)

	peerTransport := runtime.NewPeerTransport(validated.NodeID, book, runtime.DefaultSnapshotChunk)
	registry := httptransport.NewRegistryHandler(node, validated.HTTPAddr, book, defaultRequestTimeout)
	health := httptransport.NewHealthHandler(node, validated.HTTPAddr, book)
	control := httptransport.NewControlHandler(node, validated.ClusterID, validated.AdminAddr, book, peerTransport, defaultRequestTimeout)
	httpServer := &http.Server{
		Addr:    validated.HTTPAddr,
		Handler: httptransport.NewPublicServer(registry, health).Handler(),
	}
	adminServer := &http.Server{
		Addr:    validated.AdminAddr,
		Handler: adminAuthMiddleware(validated.AdminToken, httptransport.NewAdminServer(control).Handler()),
	}

	return daemonapp.New(validated, daemonapp.Components{
		Node:             node,
		HTTPServer:       httpServer,
		AdminServer:      adminServer,
		GRPCServer:       grpcServer,
		AddressBook:      book,
		PeerTransport:    peerTransport,
		TransportService: internalService,
	})
}

// adminAuthMiddleware 为独立 admin listener 提供最小控制面防护。
func adminAuthMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLocalAdminRequest(r.RemoteAddr) {
			writeError(w, http.StatusForbidden, "forbidden", "admin listener only allows 127.0.0.1")
			return
		}
		if !isAuthorizedAdminRequest(r, token) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid admin token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLocalAdminRequest(remoteAddr string) bool {
	remoteAddr = strings.TrimSpace(remoteAddr)
	if remoteAddr == "" {
		return false
	}

	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}

	return host == "127.0.0.1"
}

func isAuthorizedAdminRequest(r *http.Request, token string) bool {
	if strings.TrimSpace(token) == "" {
		return false
	}

	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	expected := "Bearer " + token
	return authorization == expected
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("write json response failed: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, httptransport.ErrorResponse{
		Code:    code,
		Message: message,
	})
}
