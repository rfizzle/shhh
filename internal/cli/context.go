package cli

import (
	"context"

	"github.com/rfizzle/shhh/internal/config"
)

type contextKey struct{}

type projectConfigKey struct{}

func withConfig(ctx context.Context, cfg config.Config) context.Context {
	return context.WithValue(ctx, contextKey{}, cfg)
}

func ConfigFrom(ctx context.Context) config.Config {
	cfg, _ := ctx.Value(contextKey{}).(config.Config)
	return cfg
}

// withProjectConfig carries what the checkout's own settings file
// contributed, beside the settings it contributed to. Every surface that
// says where a value came from needs both, and reading the file again per
// surface is how two of them come to disagree about it.
func withProjectConfig(ctx context.Context, proj config.Project) context.Context {
	return context.WithValue(ctx, projectConfigKey{}, proj)
}

// ProjectConfigFrom is the checkout's contribution, and the zero value —
// the user's file alone — where there was none.
func ProjectConfigFrom(ctx context.Context) config.Project {
	proj, _ := ctx.Value(projectConfigKey{}).(config.Project)
	return proj
}
