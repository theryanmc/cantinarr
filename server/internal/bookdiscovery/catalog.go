package bookdiscovery

import (
	"context"
	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/instance"
)

// Catalog is shared by HTTP discovery and durable request delivery so both
// use the same provider pacing, metadata cache and identity rules.
type Catalog interface {
	Feed(context.Context, string, string, int) ([]byte, error)
	Search(context.Context, string, int) ([]byte, error)
	Book(context.Context, string) ([]byte, error)
	RequestTargets(context.Context, *instance.Instance, string) ([]byte, error)
	ResolveClient(context.Context, *chaptarr.Client, string) (Targets, error)
}
