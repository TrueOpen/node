package app

import (
	"context"
	"testing"

	hubkeeper "github.com/TrueOpen/node/x/hub/keeper"
)

// The Task keeper finds a Worker's published data-availability endpoint
// through an optional method of its Hub view. This keeps the concrete Hub
// keeper from losing that method unnoticed.
func TestHubKeeperPublishesTheAnchorDAEndpoint(t *testing.T) {
	var _ interface {
		GetAnchorDAEndpoint(context.Context, string) string
	} = hubkeeper.Keeper{}
}
