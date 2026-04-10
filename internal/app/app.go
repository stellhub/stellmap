package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chenwenlong-java/StarMap/internal/raftnode"
	"github.com/chenwenlong-java/StarMap/internal/registry"
	"github.com/chenwenlong-java/StarMap/internal/runtime"
	"github.com/chenwenlong-java/StarMap/internal/storage"
	httptransport "github.com/chenwenlong-java/StarMap/internal/transport/http"
	raftpb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/grpc"
)

const (
	DefaultHTTPAddr                   = ":8080"
	DefaultAdminAddr                  = "127.0.0.1:18080"
	DefaultGRPCAddr                   = ":19090"
	DefaultShutdownTimeout            = 10 * time.Second
	DefaultRequestTimeout             = 5 * time.Second
	DefaultRegistryCleanupInterval    = 1 * time.Second
	DefaultRegistryCleanupDeleteLimit = 128
	AdminTokenEnvKey                  = "STARMAP_ADMIN_TOKEN"
)

// Config 描述 starmapd 的启动配置。
type Config struct {
	NodeID                     uint64
	ClusterID                  uint64
	DataDir                    string
	HTTPAddr                   string
	AdminAddr                  string
	AdminToken                 string
	GRPCAddr                   string
	PeerIDs                    string
	PeerGRPCAddrs              string
	PeerHTTPAddrs              string
	PeerAdminAddrs             string
	RegistryCleanupInterval    time.Duration
	RegistryCleanupDeleteLimit int
}

// Components 描述构造 App 时注入的运行时组件。
type Components struct {
	Node             *raftnode.RaftNode
	HTTPServer       *http.Server
	AdminServer      *http.Server
	GRPCServer       *grpc.Server
	AddressBook      *runtime.AddressBook
	PeerTransport    *runtime.PeerTransport
	TransportService *runtime.InternalTransportService
}

// App 是 starmapd 的运行时应用对象。
type App struct {
	cfg                   Config
	node                  *raftnode.RaftNode
	httpServer            *http.Server
	adminServer           *http.Server
	grpcServer            *grpc.Server
	grpcListener          net.Listener
	addressBook           *runtime.AddressBook
	peerTransport         *runtime.PeerTransport
	transportService      *runtime.InternalTransportService
	errCh                 chan error
	runCancel             context.CancelFunc
	shutdownOnce          sync.Once
	registryCleanupCursor registry.CleanupCursor
}

// ValidateConfig 校验并补齐配置默认值。
func ValidateConfig(cfg Config) (Config, error) {
	if cfg.NodeID == 0 {
		return Config{}, errors.New("node-id must be greater than 0")
	}
	if cfg.ClusterID == 0 {
		return Config{}, errors.New("cluster-id must be greater than 0")
	}
	if cfg.DataDir == "" {
		cfg.DataDir = filepath.Join("data", fmt.Sprintf("node-%d", cfg.NodeID))
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = DefaultHTTPAddr
	}
	if cfg.AdminAddr == "" {
		cfg.AdminAddr = DefaultAdminAddr
	}
	if cfg.GRPCAddr == "" {
		cfg.GRPCAddr = DefaultGRPCAddr
	}
	if strings.TrimSpace(cfg.AdminToken) == "" {
		cfg.AdminToken = strings.TrimSpace(os.Getenv(AdminTokenEnvKey))
	}
	if cfg.AdminToken == "" {
		return Config{}, fmt.Errorf("admin-token is required, set --admin-token or %s", AdminTokenEnvKey)
	}
	if cfg.RegistryCleanupInterval < 0 {
		return Config{}, errors.New("registry-cleanup-interval must be greater than 0")
	}
	if cfg.RegistryCleanupInterval == 0 {
		cfg.RegistryCleanupInterval = DefaultRegistryCleanupInterval
	}
	if cfg.RegistryCleanupDeleteLimit < 0 {
		return Config{}, errors.New("registry-cleanup-delete-limit must be greater than 0")
	}
	if cfg.RegistryCleanupDeleteLimit == 0 {
		cfg.RegistryCleanupDeleteLimit = DefaultRegistryCleanupDeleteLimit
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// New 根据配置和运行时组件构造应用对象。
func New(cfg Config, components Components) (*App, error) {
	validated, err := ValidateConfig(cfg)
	if err != nil {
		return nil, err
	}
	if components.Node == nil {
		return nil, errors.New("node is required")
	}
	if components.HTTPServer == nil {
		return nil, errors.New("http server is required")
	}
	if components.AdminServer == nil {
		return nil, errors.New("admin server is required")
	}
	if components.GRPCServer == nil {
		return nil, errors.New("grpc server is required")
	}
	if components.AddressBook == nil {
		return nil, errors.New("address book is required")
	}
	if components.PeerTransport == nil {
		return nil, errors.New("peer transport is required")
	}
	if components.TransportService == nil {
		return nil, errors.New("transport service is required")
	}

	return &App{
		cfg:              validated,
		node:             components.Node,
		httpServer:       components.HTTPServer,
		adminServer:      components.AdminServer,
		grpcServer:       components.GRPCServer,
		addressBook:      components.AddressBook,
		peerTransport:    components.PeerTransport,
		transportService: components.TransportService,
		errCh:            make(chan error, 4),
	}, nil
}

// Config 返回应用配置快照。
func (a *App) Config() Config {
	return a.cfg
}

// Node 返回底层 Raft 节点。
func (a *App) Node() *raftnode.RaftNode {
	return a.node
}

// AddressBook 返回当前地址簿。
func (a *App) AddressBook() *runtime.AddressBook {
	return a.addressBook
}

// PeerTransport 返回节点间消息转发器。
func (a *App) PeerTransport() *runtime.PeerTransport {
	return a.peerTransport
}

// Errors 返回后台服务错误通道。
func (a *App) Errors() <-chan error {
	return a.errCh
}

// Start 启动整个应用。
func (a *App) Start(ctx context.Context) error {
	if err := a.node.Start(ctx); err != nil {
		return err
	}
	if err := a.restorePersistedMemberAddresses(ctx); err != nil {
		_ = a.node.Stop(context.Background())
		return err
	}
	if err := a.persistCurrentAddressBook(ctx); err != nil {
		_ = a.node.Stop(context.Background())
		return err
	}

	listener, err := net.Listen("tcp", a.cfg.GRPCAddr)
	if err != nil {
		_ = a.node.Stop(context.Background())
		return err
	}
	a.grpcListener = listener
	runCtx, runCancel := context.WithCancel(ctx)
	a.runCancel = runCancel

	go a.serveGRPC()
	go a.serveHTTP()
	go a.serveAdmin()
	go a.forwardReadyLoop(runCtx)
	go a.cleanupExpiredRegistryLoop(runCtx)

	return nil
}

// Stop 优雅停止整个应用。
func (a *App) Stop(ctx context.Context) error {
	var stopErr error

	a.shutdownOnce.Do(func() {
		if a.runCancel != nil {
			a.runCancel()
		}
		if err := a.persistCurrentAddressBook(ctx); err != nil {
			stopErr = errors.Join(stopErr, err)
		}
		if a.httpServer != nil {
			if err := a.httpServer.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				stopErr = errors.Join(stopErr, err)
			}
		}
		if a.adminServer != nil {
			if err := a.adminServer.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				stopErr = errors.Join(stopErr, err)
			}
		}
		if a.grpcServer != nil {
			done := make(chan struct{})
			go func() {
				a.grpcServer.GracefulStop()
				close(done)
			}()
			select {
			case <-done:
			case <-ctx.Done():
				a.grpcServer.Stop()
				stopErr = errors.Join(stopErr, ctx.Err())
			}
		}
		if err := a.peerTransport.Close(); err != nil {
			stopErr = errors.Join(stopErr, err)
		}
		if err := a.node.Stop(ctx); err != nil && !errors.Is(err, context.Canceled) {
			stopErr = errors.Join(stopErr, err)
		}
	})

	return stopErr
}

// CleanupExpiredRegistry 扫描并清理过期实例。
func (a *App) CleanupExpiredRegistry(ctx context.Context) error {
	start := []byte(registry.RootPrefix)
	end := append(append([]byte(nil), start...), 0xFF)
	scanStart := a.registryCleanupCursor.Next(start)
	scanLimit := a.cfg.RegistryCleanupDeleteLimit

	items, err := a.node.Scan(ctx, scanStart, end, scanLimit)
	if err != nil {
		return err
	}
	a.registryCleanupCursor.Advance(start, items, scanLimit)
	if len(items) == 0 {
		return nil
	}

	now := time.Now().Unix()
	deleted := 0
	for _, item := range items {
		if !a.isRegistryCleanupLeader() {
			return nil
		}

		var value registry.Value
		if err := json.Unmarshal(item.Value, &value); err != nil {
			continue
		}
		if registry.IsAlive(value, now) {
			continue
		}
		if err := a.node.ProposeCommand(ctx, storage.Command{
			Operation: storage.OperationDelete,
			Key:       append([]byte(nil), item.Key...),
		}); err != nil {
			return err
		}
		deleted++
		if deleted >= a.cfg.RegistryCleanupDeleteLimit {
			break
		}
	}

	return nil
}

func (a *App) serveGRPC() {
	if err := a.grpcServer.Serve(a.grpcListener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		a.pushErr(fmt.Errorf("grpc serve: %w", err))
	}
}

func (a *App) serveHTTP() {
	if err := a.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		a.pushErr(fmt.Errorf("http serve: %w", err))
	}
}

func (a *App) serveAdmin() {
	if err := a.adminServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		a.pushErr(fmt.Errorf("admin serve: %w", err))
	}
}

func (a *App) cleanupExpiredRegistryLoop(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.RegistryCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !a.isRegistryCleanupLeader() {
				continue
			}
			cleanupCtx, cancel := context.WithTimeout(ctx, DefaultRequestTimeout)
			err := a.CleanupExpiredRegistry(cleanupCtx)
			cancel()
			if err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("cleanup expired registry instances failed: %v", err)
			}
		}
	}
}

func (a *App) isRegistryCleanupLeader() bool {
	status := a.node.Status()
	return status.Started && !status.Stopped &&
		status.Role == raftnode.RoleLeader &&
		status.LeaderID == status.NodeID
}

func (a *App) forwardReadyLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ready, ok := <-a.node.Ready():
			if !ok {
				return
			}
			a.applyCommittedControlEntries(ready.CommittedEntries)
			if err := a.peerTransport.Forward(ctx, ready); err != nil {
				log.Printf("forward ready messages failed: %v", err)
			}
		}
	}
}

func (a *App) applyCommittedControlEntries(entries []raftnode.LogEntry) {
	for _, entry := range entries {
		switch entry.Raw.Type {
		case raftpb.EntryConfChangeV2:
			var change raftpb.ConfChangeV2
			if err := change.Unmarshal(entry.Raw.Data); err != nil {
				continue
			}
			a.applyConfChangeContext(change.Changes, change.Context)
		case raftpb.EntryConfChange:
			var change raftpb.ConfChange
			if err := change.Unmarshal(entry.Raw.Data); err != nil {
				continue
			}
			a.applyConfChangeContext([]raftpb.ConfChangeSingle{{
				Type:   change.Type,
				NodeID: change.NodeID,
			}}, change.Context)
		}
	}
}

func (a *App) applyConfChangeContext(changes []raftpb.ConfChangeSingle, contextData []byte) {
	if len(changes) == 0 {
		return
	}

	var request httptransport.MemberChangeRequestDTO
	if len(contextData) > 0 {
		if err := json.Unmarshal(contextData, &request); err != nil {
			log.Printf("decode conf change context failed: %v", err)
		}
	}
	if request.NodeID == 0 {
		request.NodeID = changes[0].NodeID
	}

	for _, change := range changes {
		switch change.Type {
		case raftpb.ConfChangeAddLearnerNode, raftpb.ConfChangeAddNode:
			if err := a.peerTransport.UpsertPeer(request.NodeID, request.HTTPAddr, request.GRPCAddr, request.AdminAddr); err != nil {
				log.Printf("upsert peer %d failed: %v", request.NodeID, err)
			}
			if err := a.node.SetMemberAddress(context.Background(), request.NodeID, request.HTTPAddr, request.GRPCAddr, request.AdminAddr); err != nil {
				log.Printf("persist peer %d address failed: %v", request.NodeID, err)
			}
		case raftpb.ConfChangeRemoveNode:
			if err := a.peerTransport.RemovePeer(request.NodeID); err != nil {
				log.Printf("remove peer %d failed: %v", request.NodeID, err)
			}
			if err := a.node.DeleteMemberAddress(context.Background(), request.NodeID); err != nil {
				log.Printf("delete peer %d address failed: %v", request.NodeID, err)
			}
		}
	}
}

func (a *App) restorePersistedMemberAddresses(ctx context.Context) error {
	members, err := a.node.ListMemberAddresses(ctx)
	if err != nil {
		return err
	}

	for _, member := range members {
		if member.NodeID == 0 {
			continue
		}
		currentHTTP := a.addressBook.HTTPAddr(member.NodeID)
		currentGRPC := a.addressBook.GRPCAddr(member.NodeID)
		currentAdmin := a.addressBook.AdminAddr(member.NodeID)
		httpAddr := currentHTTP
		grpcAddr := currentGRPC
		adminAddr := currentAdmin
		if httpAddr == "" {
			httpAddr = member.HTTPAddr
		}
		if grpcAddr == "" {
			grpcAddr = member.GRPCAddr
		}
		if adminAddr == "" {
			adminAddr = member.AdminAddr
		}
		a.addressBook.Set(member.NodeID, httpAddr, grpcAddr, adminAddr)
	}

	return nil
}

func (a *App) persistCurrentAddressBook(ctx context.Context) error {
	httpAddrs := a.addressBook.SnapshotHTTP()
	grpcAddrs := a.addressBook.SnapshotGRPC()
	adminAddrs := a.addressBook.SnapshotAdmin()
	persisted, err := a.node.ListMemberAddresses(ctx)
	if err != nil {
		return err
	}

	seen := make(map[uint64]struct{}, len(httpAddrs)+len(grpcAddrs)+len(adminAddrs))
	for nodeID, httpAddr := range httpAddrs {
		seen[nodeID] = struct{}{}
		if err := a.node.SetMemberAddress(ctx, nodeID, httpAddr, grpcAddrs[nodeID], adminAddrs[nodeID]); err != nil {
			return err
		}
	}
	for nodeID, grpcAddr := range grpcAddrs {
		if _, ok := seen[nodeID]; ok {
			continue
		}
		seen[nodeID] = struct{}{}
		if err := a.node.SetMemberAddress(ctx, nodeID, httpAddrs[nodeID], grpcAddr, adminAddrs[nodeID]); err != nil {
			return err
		}
	}
	for nodeID, adminAddr := range adminAddrs {
		if _, ok := seen[nodeID]; ok {
			continue
		}
		seen[nodeID] = struct{}{}
		if err := a.node.SetMemberAddress(ctx, nodeID, httpAddrs[nodeID], grpcAddrs[nodeID], adminAddr); err != nil {
			return err
		}
	}
	for _, member := range persisted {
		if _, ok := seen[member.NodeID]; ok {
			continue
		}
		if err := a.node.DeleteMemberAddress(ctx, member.NodeID); err != nil {
			return err
		}
	}

	return nil
}

func (a *App) pushErr(err error) {
	select {
	case a.errCh <- err:
	default:
	}
}
