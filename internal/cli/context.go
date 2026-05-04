package cli

import (
	"context"

	"github.com/rfizzle/shhh/internal/config"
)

type contextKey struct{}

func withConfig(ctx context.Context, cfg config.Config) context.Context {
	return context.WithValue(ctx, contextKey{}, cfg)
}

func ConfigFrom(ctx context.Context) config.Config {
	cfg, _ := ctx.Value(contextKey{}).(config.Config)
	return cfg
}
