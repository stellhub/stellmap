package logging

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	daemonapp "github.com/stellhub/stellmap/internal/app"
	"github.com/stellhub/stellspec-go-sdk/bridge/zapbridge"
	stellspecconfig "github.com/stellhub/stellspec-go-sdk/config"
	"github.com/stellhub/stellspec-go-sdk/sdk"
	"go.etcd.io/raft/v3"
	"go.uber.org/zap"
	"google.golang.org/grpc/grpclog"
)

const (
	daemonServiceName      = "stellmapd"
	daemonServiceNamespace = "stellhub.stellmap"
	localEnvironmentName   = "local"
)

// Version 描述当前二进制版本，默认值可在构建时通过 ldflags 覆盖。
var Version = "dev"

// Runtime 管理 stellmapd 进程内的日志生命周期。
type Runtime struct {
	runtime        *sdk.Runtime
	logger         *zap.Logger
	restoreStdLog  func()
	restoreGlobals func()
}

// New 为 stellmapd 初始化 stellspec 日志运行时，并接管标准库日志与 raft 全局日志。
func New(ctx context.Context, cfg daemonapp.Config) (*Runtime, error) {
	logCfg, err := buildConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build daemon log config: %w", err)
	}

	runtime, err := sdk.New(ctx, logCfg)
	if err != nil {
		return nil, fmt.Errorf("init stellspec runtime: %w", err)
	}

	logger, err := zapbridge.NewLogger(runtime, zapbridge.Options{
		Name:          daemonServiceName,
		AddCaller:     true,
		AddStacktrace: true,
		Fields: []zap.Field{
			zap.String("service", daemonServiceName),
			zap.Uint64("node_id", cfg.NodeID),
			zap.Uint64("cluster_id", cfg.ClusterID),
			zap.String("region", cfg.Region),
			zap.String("http_addr", cfg.HTTPAddr),
			zap.String("admin_addr", cfg.AdminAddr),
			zap.String("grpc_addr", cfg.GRPCAddr),
			zap.String("data_dir", cfg.DataDir),
		},
	})
	if err != nil {
		shutdownCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_ = runtime.Shutdown(shutdownCtx)
		return nil, fmt.Errorf("create zap bridge logger: %w", err)
	}

	raft.SetLogger(newRaftLogger(logger))
	grpclog.SetLoggerV2(newGRPCLogger(logger))

	return &Runtime{
		runtime:        runtime,
		logger:         logger,
		restoreStdLog:  zap.RedirectStdLog(logger),
		restoreGlobals: zap.ReplaceGlobals(logger),
	}, nil
}

// Close 刷新并关闭日志运行时。
func (r *Runtime) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if r.restoreStdLog != nil {
		r.restoreStdLog()
	}
	if r.restoreGlobals != nil {
		r.restoreGlobals()
	}
	raft.ResetDefaultLogger()

	var result error
	if r.logger != nil {
		if err := r.logger.Sync(); err != nil && !isIgnorableLoggerSyncError(err) {
			result = errors.Join(result, err)
		}
	}
	if r.runtime != nil {
		if err := r.runtime.Shutdown(ctx); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

// EffectiveShutdownTimeout 返回日志关闭阶段应使用的等待时长。
func EffectiveShutdownTimeout(cfg daemonapp.Config) time.Duration {
	if cfg.ShutdownTimeout > 0 {
		return cfg.ShutdownTimeout
	}
	return 5 * time.Second
}

func buildConfig(cfg daemonapp.Config) (stellspecconfig.Config, error) {
	logCfg := stellspecconfig.Default()
	if err := logCfg.ApplyEnv(); err != nil {
		return stellspecconfig.Config{}, err
	}

	if strings.TrimSpace(logCfg.ServiceName) == "" {
		logCfg.ServiceName = daemonServiceName
	}
	if strings.TrimSpace(logCfg.ServiceNamespace) == "" {
		logCfg.ServiceNamespace = daemonServiceNamespace
	}
	if strings.TrimSpace(logCfg.ServiceVersion) == "" {
		logCfg.ServiceVersion = Version
	}
	if strings.TrimSpace(logCfg.ServiceInstanceID) == "" {
		logCfg.ServiceInstanceID = buildServiceInstanceID(cfg.NodeID)
	}
	if strings.TrimSpace(logCfg.Region) == "" {
		logCfg.Region = strings.TrimSpace(cfg.Region)
	}
	if logCfg.ResourceAttributes == nil {
		logCfg.ResourceAttributes = make(map[string]string)
	}
	if cfg.NodeID > 0 {
		logCfg.ResourceAttributes["stellmap.node_id"] = strconv.FormatUint(cfg.NodeID, 10)
	}
	if cfg.ClusterID > 0 {
		logCfg.ResourceAttributes["stellmap.cluster_id"] = strconv.FormatUint(cfg.ClusterID, 10)
	}
	if strings.TrimSpace(cfg.HTTPAddr) != "" {
		logCfg.ResourceAttributes["stellmap.http_addr"] = strings.TrimSpace(cfg.HTTPAddr)
	}
	if strings.TrimSpace(cfg.AdminAddr) != "" {
		logCfg.ResourceAttributes["stellmap.admin_addr"] = strings.TrimSpace(cfg.AdminAddr)
	}
	if strings.TrimSpace(cfg.GRPCAddr) != "" {
		logCfg.ResourceAttributes["stellmap.grpc_addr"] = strings.TrimSpace(cfg.GRPCAddr)
	}

	switch normalizeEnvironment(logCfg.Environment) {
	case "", "dev", "local", "development":
		logCfg.Environment = localEnvironmentName
		logCfg.Development = true
		logCfg.Output = stellspecconfig.OutputStdout
		logCfg.Format = stellspecconfig.FormatConsole
		logCfg.Endpoint = ""
	default:
		logCfg.Development = false
		logCfg.Output = stellspecconfig.OutputOTLP
		logCfg.Format = stellspecconfig.FormatJSON
	}

	return logCfg.Normalize()
}

func buildServiceInstanceID(nodeID uint64) string {
	if nodeID > 0 {
		return fmt.Sprintf("%s-%d", daemonServiceName, nodeID)
	}
	return fmt.Sprintf("%s-pid-%d", daemonServiceName, os.Getpid())
}

func normalizeEnvironment(environment string) string {
	return strings.ToLower(strings.TrimSpace(environment))
}

func isIgnorableLoggerSyncError(err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	return strings.Contains(message, "invalid argument") ||
		strings.Contains(message, "inappropriate ioctl for device") ||
		errors.Is(err, os.ErrInvalid)
}
