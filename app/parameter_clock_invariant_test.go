package app

import (
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"
)

func TestCrossModuleImportRejectsAnIncompatibleVerifierClock(t *testing.T) {
	application := bootAppMinimal(t)
	ctx := application.NewContextLegacy(true, cmtproto.Header{Height: 10})
	hub, err := application.HubKeeper.Params.Get(ctx)
	require.NoError(t, err)
	hub.Epoch.DeltaWBlocks, hub.Builder.OpenVerifyBuilderProposalWindowBlocks = 1, 120
	require.NoError(t, application.HubKeeper.Params.Set(ctx, hub))
	task, err := application.TaskKeeper.Params.Get(ctx)
	require.NoError(t, err)
	task.Deadlines.VerifyOpenDeadlineBlocks = 120
	require.NoError(t, application.TaskKeeper.Params.Set(ctx, task))
	require.ErrorContains(t, application.EnsureCrossModuleReferences(ctx), "verify_open_deadline_blocks")
}
