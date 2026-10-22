package singmux

import (
	"context"

	"github.com/xtls/xray-core/common/errors"
)

// logger routes sing-mux service messages to the Xray log. Per-stream notices
// are debug; service errors are info, like other connection errors.
type logger struct{}

func (logger) Trace(args ...any) {}
func (logger) Debug(args ...any) { errors.LogDebug(context.Background(), args...) }
func (logger) Info(args ...any)  { errors.LogDebug(context.Background(), args...) }
func (logger) Warn(args ...any)  { errors.LogInfo(context.Background(), args...) }
func (logger) Error(args ...any) { errors.LogInfo(context.Background(), args...) }
func (logger) Fatal(args ...any) { errors.LogError(context.Background(), args...) }
func (logger) Panic(args ...any) { errors.LogError(context.Background(), args...) }

func (logger) TraceContext(ctx context.Context, args ...any) {}
func (logger) DebugContext(ctx context.Context, args ...any) { errors.LogDebug(ctx, args...) }
func (logger) InfoContext(ctx context.Context, args ...any)  { errors.LogDebug(ctx, args...) }
func (logger) WarnContext(ctx context.Context, args ...any)  { errors.LogInfo(ctx, args...) }
func (logger) ErrorContext(ctx context.Context, args ...any) { errors.LogInfo(ctx, args...) }
func (logger) FatalContext(ctx context.Context, args ...any) { errors.LogError(ctx, args...) }
func (logger) PanicContext(ctx context.Context, args ...any) { errors.LogError(ctx, args...) }
