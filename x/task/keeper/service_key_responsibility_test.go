package keeper

import (
	"bytes"
	"context"
	"encoding/hex"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

const testBuildersPerStageResponsibility = 3

func TestServiceKeyResponsibilityIDUsesRegisteredCanonicalPreimage(t *testing.T) {
	base, err := serviceKeyResponsibilityID(
		shared.ParticipantType_PARTICIPANT_TYPE_BUILDER,
		hubtypes.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_SETTLE_BUILDER,
		"session-a", "task-a", "scope-a", "operator-a",
	)
	require.NoError(t, err)
	require.Equal(t, "37bc2e1c7a9f7779ab579799af5bc5bfea034395674b4fc241cfd8017fe43f1b", hex.EncodeToString(base))
	otherScope, err := serviceKeyResponsibilityID(
		shared.ParticipantType_PARTICIPANT_TYPE_BUILDER,
		hubtypes.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_SETTLE_BUILDER,
		"session-a", "task-a", "scope-b", "operator-a",
	)
	require.NoError(t, err)
	require.NotEqual(t, base, otherScope)
	spec, ok := shared.DomainSpecFor(shared.DomainServiceKeyResponsibilityIDV1)
	require.True(t, ok)
	require.Equal(t, shared.FramingHFieldsV1, spec.Framing)
}

type builderStageResponsibilityRecorder struct {
	hubtypes.HubKeeper
	reserved []hubtypes.ServiceKeyResponsibilityState
	released []string
}

func (r *builderStageResponsibilityRecorder) GetHubParams(sdk.Context) hubtypes.HubParamsSnapshot {
	return hubtypes.HubParamsSnapshot{BuildersPerTask: testBuildersPerStageResponsibility}
}

func (r *builderStageResponsibilityRecorder) ReserveServiceKeyResponsibility(_ context.Context, responsibility hubtypes.ServiceKeyResponsibilityState) error {
	r.reserved = append(r.reserved, responsibility)
	return nil
}

func (r *builderStageResponsibilityRecorder) ReleaseServiceKeyResponsibility(_ context.Context, participantType, operatorAddress, responsibilityID string) error {
	r.released = append(r.released, participantType+"/"+operatorAddress+"/"+responsibilityID)
	return nil
}

func builderStageResponsibilityFixture(t *testing.T) (context.Context, tasktypes.TaskCoreState, tasktypes.TaskBuilderSelectionState, []string) {
	t.Helper()
	base := initInternalFixture(t)
	ctx := sdk.WrapSDKContext(sdk.UnwrapSDKContext(base.ctx).WithChainID("trueopen-responsibility-test"))
	core := tasktypes.TaskCoreState{
		SessionId: bytes.Repeat([]byte{0x31}, tasktypes.Hash32Len),
		TaskId:    bytes.Repeat([]byte{0x32}, tasktypes.Hash32Len),
	}
	builders := []string{
		sdk.AccAddress(bytes.Repeat([]byte{0x41}, 20)).String(),
		sdk.AccAddress(bytes.Repeat([]byte{0x42}, 20)).String(),
		sdk.AccAddress(bytes.Repeat([]byte{0x43}, 20)).String(),
	}
	builderSetHash := bytes.Repeat([]byte{0x33}, tasktypes.Hash32Len)
	selectedHash, err := tasktypes.SelectedTaskBuildersHash(
		sdk.UnwrapSDKContext(ctx).ChainID(), core.TaskId, "builder-set-v1", builderSetHash, builders,
	)
	require.NoError(t, err)
	selection := tasktypes.TaskBuilderSelectionState{
		TaskId: core.TaskId, BuilderSetId: "builder-set-v1", BuilderSetHash: builderSetHash,
		SelectedTaskBuilders: builders, SelectedTaskBuilderCount: uint32(len(builders)),
		SelectedTaskBuildersHash: selectedHash, BodyStatus: shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE,
	}
	return ctx, core, selection, builders
}

func TestBuilderStageResponsibilitiesCoverAndReleaseEverySelectedBuilder(t *testing.T) {
	recorder := &builderStageResponsibilityRecorder{}
	k := Keeper{hubKeeper: recorder}
	ctx, core, selection, builders := builderStageResponsibilityFixture(t)
	sessionID := hex.EncodeToString(core.SessionId)
	taskID := hex.EncodeToString(core.TaskId)

	require.NoError(t, k.reserveBuilderStageResponsibilities(ctx, core, selection, serviceKeyResponsibilitySettleBuilder, 42))
	require.Len(t, recorder.reserved, testBuildersPerStageResponsibility)
	for i, builder := range builders {
		responsibility := recorder.reserved[i]
		require.Equal(t, shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, responsibility.ParticipantType)
		require.Equal(t, builder, responsibility.OperatorAddress)
		require.Equal(t, serviceKeyResponsibilitySettleBuilder, responsibility.ResponsibilityKind)
		expectedResponsibilityID, err := BuilderStageResponsibilityID(sessionID, taskID, serviceKeyResponsibilitySettleBuilder, builder)
		require.NoError(t, err)
		require.Equal(t, expectedResponsibilityID, responsibility.ResponsibilityId)
		require.Equal(t, uint64(42), responsibility.CreatedHeight)
	}

	require.NoError(t, k.releaseBuilderStageResponsibilities(ctx, core, selection, serviceKeyResponsibilitySettleBuilder))
	require.Len(t, recorder.released, testBuildersPerStageResponsibility)
}

func TestBuilderStageResponsibilitiesRejectIncompleteSelection(t *testing.T) {
	k := Keeper{hubKeeper: &builderStageResponsibilityRecorder{}}
	ctx, core, selection, builders := builderStageResponsibilityFixture(t)
	selection.SelectedTaskBuilders = builders[:2]
	require.ErrorContains(t, k.reserveBuilderStageResponsibilities(ctx, core, selection, serviceKeyResponsibilityOpenVerifyBuilder, 42), "exactly 3 builders")
	require.ErrorContains(t, k.releaseBuilderStageResponsibilities(ctx, core, selection, serviceKeyResponsibilityOpenVerifyBuilder), "exactly 3 builders")

	selection.SelectedTaskBuilders = []string{builders[0], builders[0], builders[1]}
	require.ErrorContains(t, k.reserveBuilderStageResponsibilities(ctx, core, selection, serviceKeyResponsibilityOpenVerifyBuilder, 42), "3 unique builders")
	require.ErrorContains(t, k.releaseBuilderStageResponsibilities(ctx, core, selection, serviceKeyResponsibilityOpenVerifyBuilder), "3 unique builders")
}
