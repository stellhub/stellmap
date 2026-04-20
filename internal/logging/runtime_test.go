package logging

import (
	"testing"
	"time"

	daemonapp "github.com/stellhub/stellmap/internal/app"
	stellspecconfig "github.com/stellhub/stellspec-go-sdk/config"
)

func TestBuildConfigUsesStdoutInLocalEnvironment(t *testing.T) {
	t.Setenv("STELLAR_ENV", "")
	t.Setenv("STELLSPEC_ENVIRONMENT", "")
	t.Setenv("STELLSPEC_ENDPOINT", "")

	cfg, err := buildConfig(daemonapp.Config{
		NodeID:    1,
		ClusterID: 100,
		Region:    "cn-sh",
		HTTPAddr:  "127.0.0.1:8080",
		AdminAddr: "127.0.0.1:18080",
		GRPCAddr:  "127.0.0.1:19090",
	})
	if err != nil {
		t.Fatalf("build config failed: %v", err)
	}

	if cfg.Environment != localEnvironmentName {
		t.Fatalf("expected environment %q, got %q", localEnvironmentName, cfg.Environment)
	}
	if !cfg.Development {
		t.Fatalf("expected development=true")
	}
	if cfg.Output != stellspecconfig.OutputStdout {
		t.Fatalf("expected output=%q, got %q", stellspecconfig.OutputStdout, cfg.Output)
	}
	if cfg.Format != stellspecconfig.FormatConsole {
		t.Fatalf("expected format=%q, got %q", stellspecconfig.FormatConsole, cfg.Format)
	}
	if cfg.Endpoint != "" {
		t.Fatalf("expected empty endpoint in local environment, got %q", cfg.Endpoint)
	}
	if cfg.ServiceName != daemonServiceName {
		t.Fatalf("expected service name %q, got %q", daemonServiceName, cfg.ServiceName)
	}
	if cfg.ServiceInstanceID != "stellmapd-1" {
		t.Fatalf("expected service instance id %q, got %q", "stellmapd-1", cfg.ServiceInstanceID)
	}
	if cfg.Region != "cn-sh" {
		t.Fatalf("expected region %q, got %q", "cn-sh", cfg.Region)
	}
}

func TestBuildConfigUsesOTLPOutsideLocalEnvironment(t *testing.T) {
	t.Setenv("STELLAR_ENV", "prod")
	t.Setenv("STELLSPEC_ENDPOINT", "127.0.0.1:5317")
	t.Setenv("STELLSPEC_LEVEL", "error")

	cfg, err := buildConfig(daemonapp.Config{
		NodeID:    2,
		ClusterID: 200,
		Region:    "cn-bj",
	})
	if err != nil {
		t.Fatalf("build config failed: %v", err)
	}

	if cfg.Development {
		t.Fatalf("expected development=false")
	}
	if cfg.Output != stellspecconfig.OutputOTLP {
		t.Fatalf("expected output=%q, got %q", stellspecconfig.OutputOTLP, cfg.Output)
	}
	if cfg.Format != stellspecconfig.FormatJSON {
		t.Fatalf("expected format=%q, got %q", stellspecconfig.FormatJSON, cfg.Format)
	}
	if cfg.Endpoint != "127.0.0.1:5317" {
		t.Fatalf("expected endpoint %q, got %q", "127.0.0.1:5317", cfg.Endpoint)
	}
	if cfg.Level != "error" {
		t.Fatalf("expected level %q, got %q", "error", cfg.Level)
	}
	if cfg.ServiceInstanceID != "stellmapd-2" {
		t.Fatalf("expected service instance id %q, got %q", "stellmapd-2", cfg.ServiceInstanceID)
	}
}

func TestEffectiveShutdownTimeoutUsesConfiguredValue(t *testing.T) {
	cfg := daemonapp.Config{ShutdownTimeout: 12 * time.Second}
	if EffectiveShutdownTimeout(cfg) != 12*time.Second {
		t.Fatalf("expected shutdown timeout=%v, got %v", cfg.ShutdownTimeout, EffectiveShutdownTimeout(cfg))
	}
}

func TestGRPCVerbosityFromEnv(t *testing.T) {
	t.Setenv("GRPC_GO_LOG_VERBOSITY_LEVEL", "3")
	if got := grpcVerbosityFromEnv(); got != 3 {
		t.Fatalf("expected grpc verbosity=3, got %d", got)
	}
}

func TestGRPCVerbosityFromEnvFallsBackToZero(t *testing.T) {
	t.Setenv("GRPC_GO_LOG_VERBOSITY_LEVEL", "invalid")
	if got := grpcVerbosityFromEnv(); got != 0 {
		t.Fatalf("expected grpc verbosity=0, got %d", got)
	}
}
