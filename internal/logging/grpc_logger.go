package logging

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"go.uber.org/zap"
	"google.golang.org/grpc/grpclog"
)

type grpcLogger struct {
	logger    *zap.Logger
	verbosity int
}

func newGRPCLogger(base *zap.Logger) grpclog.LoggerV2 {
	return &grpcLogger{
		logger:    base.Named("grpc").With(zap.String("component", "grpc")),
		verbosity: grpcVerbosityFromEnv(),
	}
}

func (l *grpcLogger) Info(args ...any) {
	l.logger.Info(fmt.Sprint(args...))
}

func (l *grpcLogger) Infoln(args ...any) {
	l.logger.Info(trimTrailingNewline(fmt.Sprintln(args...)))
}

func (l *grpcLogger) Infof(format string, args ...any) {
	l.logger.Info(fmt.Sprintf(format, args...))
}

func (l *grpcLogger) Warning(args ...any) {
	l.logger.Warn(fmt.Sprint(args...))
}

func (l *grpcLogger) Warningln(args ...any) {
	l.logger.Warn(trimTrailingNewline(fmt.Sprintln(args...)))
}

func (l *grpcLogger) Warningf(format string, args ...any) {
	l.logger.Warn(fmt.Sprintf(format, args...))
}

func (l *grpcLogger) Error(args ...any) {
	l.logger.Error(fmt.Sprint(args...))
}

func (l *grpcLogger) Errorln(args ...any) {
	l.logger.Error(trimTrailingNewline(fmt.Sprintln(args...)))
}

func (l *grpcLogger) Errorf(format string, args ...any) {
	l.logger.Error(fmt.Sprintf(format, args...))
}

func (l *grpcLogger) Fatal(args ...any) {
	l.logger.Fatal(fmt.Sprint(args...))
}

func (l *grpcLogger) Fatalln(args ...any) {
	l.logger.Fatal(trimTrailingNewline(fmt.Sprintln(args...)))
}

func (l *grpcLogger) Fatalf(format string, args ...any) {
	l.logger.Fatal(fmt.Sprintf(format, args...))
}

func (l *grpcLogger) V(level int) bool {
	return level <= l.verbosity
}

func (l *grpcLogger) InfoDepth(depth int, args ...any) {
	l.withDepth(depth).Info(trimTrailingNewline(fmt.Sprintln(args...)))
}

func (l *grpcLogger) WarningDepth(depth int, args ...any) {
	l.withDepth(depth).Warn(trimTrailingNewline(fmt.Sprintln(args...)))
}

func (l *grpcLogger) ErrorDepth(depth int, args ...any) {
	l.withDepth(depth).Error(trimTrailingNewline(fmt.Sprintln(args...)))
}

func (l *grpcLogger) FatalDepth(depth int, args ...any) {
	l.withDepth(depth).Fatal(trimTrailingNewline(fmt.Sprintln(args...)))
}

func (l *grpcLogger) withDepth(depth int) *zap.Logger {
	if depth <= 0 {
		return l.logger
	}
	return l.logger.WithOptions(zap.AddCallerSkip(depth))
}

func grpcVerbosityFromEnv() int {
	raw := strings.TrimSpace(os.Getenv("GRPC_GO_LOG_VERBOSITY_LEVEL"))
	if raw == "" {
		return 0
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func trimTrailingNewline(message string) string {
	return strings.TrimRight(message, "\r\n")
}
