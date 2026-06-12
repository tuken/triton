package context

import (
	"context"

	"go.uber.org/zap"
)

type contextKey struct{ string }

var (
	LoggerKey = contextKey{"logger"}
)

func MustLogger(ctx context.Context) *zap.SugaredLogger {

	logger, ok := ctx.Value(LoggerKey).(*zap.SugaredLogger)

	if !ok {
		panic("Logger is not found")
	}

	if logger == nil {
		panic("Logger is nil")
	}

	return logger
}
