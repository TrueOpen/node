package keeper_test

import (
	"bytes"
	"encoding/hex"
	"math"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func busObjectiveEvidenceLocator(identity hubTestIdentity, session, task string) shared.BusObjectiveEvidenceResponsibilityV1 {
	return shared.BusObjectiveEvidenceResponsibilityV1{
		SchemaVersion:             1,
		BuilderOperator:           identity.Address,
		SessionId:                 hubHashBytes(session),
		TaskId:                    hubHashBytes(task),
		ServiceAuthorizationNonce: 1,
	}
}

func expectedBusObjectiveEvidenceResponsibilityID(
	t *testing.T,
	locator shared.BusObjectiveEvidenceResponsibilityV1,
) []byte {
	t.Helper()
	operatorBytes := sdk.MustAccAddressFromBech32(locator.BuilderOperator).Bytes()
	id, err := shared.NewCanonicalHashBuilderV1(
		shared.MustDomain(shared.DomainServiceKeyResponsibilityIDV1),
	).Raw(
		shared.EnumBE(uint32(shared.ParticipantType_PARTICIPANT_TYPE_BUILDER)),
		shared.EnumBE(uint32(types.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_BUS_OBJECTIVE_EVIDENCE)),
		locator.SessionId,
		locator.TaskId,
		locator.TaskId,
		operatorBytes,
	).Sum()
	require.NoError(t, err)
	return id
}

func TestBusObjectiveEvidenceResponsibilityLifecycle(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(200))
	identity := hubIdentity(t, 201)
	registerBuilderIdentityForTest(t, f, identity, 10)
	locator := busObjectiveEvidenceLocator(identity, "bus-responsibility-session", "bus-responsibility-task")

	acquired, err := f.keeper.AcquireBusObjectiveEvidenceResponsibility(f.ctx, locator)
	require.NoError(t, err)
	require.True(t, acquired.Active)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, acquired.Status)
	require.Equal(t, expectedBusObjectiveEvidenceResponsibilityID(t, locator), acquired.ResponsibilityId)
	require.Equal(t, locator.BuilderOperator, acquired.BuilderOperator)
	require.Equal(t, locator.TaskId, acquired.TaskId)
	require.Equal(t, locator.ServiceAuthorizationNonce, acquired.ServiceAuthorizationNonce)

	primaryKey := types.NewServiceKeyResponsibilityKey(
		shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, locator.BuilderOperator, acquired.ResponsibilityId,
	)
	stored, err := f.keeper.ReadServiceKeyResponsibilityValue(f.ctx, primaryKey)
	require.NoError(t, err)
	require.Equal(t, hex.EncodeToString(locator.SessionId), stored.SessionId)
	require.Equal(t, hex.EncodeToString(locator.TaskId), stored.TaskId)
	require.Equal(t, uint64(200), stored.CreatedHeight)
	require.Equal(t, locator.ServiceAuthorizationNonce, stored.ServiceAuthorizationNonce)
	indexKey := types.NewServiceKeyResponsibilityByTaskKey(
		stored.SessionId, stored.TaskId, stored.ParticipantType, stored.OperatorAddress, stored.ResponsibilityId,
	)
	has, err := f.keeper.ServiceKeyResponsibilityByTaskIndex.Has(f.ctx, indexKey)
	require.NoError(t, err)
	require.True(t, has)
	builder, err := f.keeper.GetBuilderState(f.ctx, locator.BuilderOperator)
	require.NoError(t, err)
	require.Equal(t, uint32(1), builder.PendingEvidenceSubmissionCount)

	replayed, err := f.keeper.AcquireBusObjectiveEvidenceResponsibility(f.ctx, locator)
	require.NoError(t, err)
	require.True(t, replayed.Active)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, replayed.Status)
	builder, err = f.keeper.GetBuilderState(f.ctx, locator.BuilderOperator)
	require.NoError(t, err)
	require.Equal(t, uint32(1), builder.PendingEvidenceSubmissionCount)
	wrongNonce := locator
	wrongNonce.ServiceAuthorizationNonce++
	_, err = f.keeper.AcquireBusObjectiveEvidenceResponsibility(f.ctx, wrongNonce)
	require.ErrorContains(t, err, "id already exists with different state")
	builder, err = f.keeper.GetBuilderState(f.ctx, locator.BuilderOperator)
	require.NoError(t, err)
	require.Equal(t, uint32(1), builder.PendingEvidenceSubmissionCount)

	// The generic lifecycle must not bypass the kind-5 counter path, and the
	// task-wide terminal release must preserve this row until EvidenceCleanup.
	require.ErrorContains(t, f.keeper.ReserveServiceKeyResponsibility(f.ctx, stored), "typed acquire")
	require.ErrorContains(t, f.keeper.ReleaseServiceKeyResponsibility(
		f.ctx, shared.ParticipantTypeBuilder, locator.BuilderOperator, hex.EncodeToString(acquired.ResponsibilityId),
	), "typed release")
	require.NoError(t, f.keeper.ReleaseServiceKeyResponsibilities(f.ctx, stored.SessionId, stored.TaskId))
	has, err = f.keeper.ServiceKeyResponsibility.Has(f.ctx, primaryKey)
	require.NoError(t, err)
	require.True(t, has)

	_, err = f.keeper.ReleaseBusObjectiveEvidenceResponsibility(f.ctx, wrongNonce)
	require.ErrorContains(t, err, "does not match the stored locator")
	builder, err = f.keeper.GetBuilderState(f.ctx, locator.BuilderOperator)
	require.NoError(t, err)
	require.Equal(t, uint32(1), builder.PendingEvidenceSubmissionCount)

	revoked, err := keeper.NewMsgServerImpl(f.keeper).RevokeServiceKey(f.ctx, &types.MsgRevokeServiceKey{
		OperatorAddress:                          locator.BuilderOperator,
		ParticipantType:                          shared.ParticipantType_PARTICIPANT_TYPE_BUILDER,
		ExpectedCurrentServiceAuthorizationNonce: locator.ServiceAuthorizationNonce,
		ReasonCode:                               types.ServiceKeyRevocationReason_SERVICE_KEY_REVOCATION_REASON_COMPROMISED,
	})
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, revoked.Status)
	binding, err := f.keeper.GetBuilderObjectiveEvidenceCurrentBinding(f.ctx, locator.BuilderOperator)
	require.NoError(t, err)
	require.Equal(t, types.ServiceKeyStatusRevoked, binding.Status)
	require.Equal(t, locator.ServiceAuthorizationNonce, binding.AuthorizationNonce)

	replayed, err = f.keeper.AcquireBusObjectiveEvidenceResponsibility(f.ctx, locator)
	require.NoError(t, err, "an acquired row remains an exact replay after revoke")
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, replayed.Status)
	newAfterRevoke := busObjectiveEvidenceLocator(identity, "bus-responsibility-session", "new-task-after-revoke")
	_, err = f.keeper.AcquireBusObjectiveEvidenceResponsibility(f.ctx, newAfterRevoke)
	require.ErrorContains(t, err, "not ACTIVE")

	released, err := f.keeper.ReleaseBusObjectiveEvidenceResponsibility(f.ctx, locator)
	require.NoError(t, err)
	require.False(t, released.Active)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, released.Status)
	has, err = f.keeper.ServiceKeyResponsibility.Has(f.ctx, primaryKey)
	require.NoError(t, err)
	require.False(t, has)
	has, err = f.keeper.ServiceKeyResponsibilityByTaskIndex.Has(f.ctx, indexKey)
	require.NoError(t, err)
	require.False(t, has)
	builder, err = f.keeper.GetBuilderState(f.ctx, locator.BuilderOperator)
	require.NoError(t, err)
	require.Zero(t, builder.PendingEvidenceSubmissionCount)

	// There is intentionally no released-row tombstone in Wire v0.2.1. Once the
	// primary is absent, bounded cleanup replay uses the recomputed ID and cannot
	// validate a historical nonce that is no longer stored.
	releasedReplay, err := f.keeper.ReleaseBusObjectiveEvidenceResponsibility(f.ctx, locator)
	require.NoError(t, err)
	require.False(t, releasedReplay.Active)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, releasedReplay.Status)
}

func TestBusObjectiveEvidenceResponsibilityAtomicFailures(t *testing.T) {
	t.Run("counter overflow leaves no row or index", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
		f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(200))
		identity := hubIdentity(t, 202)
		builder := registerBuilderIdentityForTest(t, f, identity, 10)
		builder.PendingEvidenceSubmissionCount = math.MaxUint32
		require.NoError(t, f.keeper.StoreBuilder(f.ctx, identity.Address, builder))
		locator := busObjectiveEvidenceLocator(identity, "overflow-session", "overflow-task")
		expectedID := expectedBusObjectiveEvidenceResponsibilityID(t, locator)

		_, err := f.keeper.AcquireBusObjectiveEvidenceResponsibility(f.ctx, locator)
		require.ErrorContains(t, err, "overflow")
		has, getErr := f.keeper.ServiceKeyResponsibility.Has(f.ctx, types.NewServiceKeyResponsibilityKey(
			shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, identity.Address, expectedID,
		))
		require.NoError(t, getErr)
		require.False(t, has)
		_, err = keeper.NewMsgServerImpl(f.keeper).RotateServiceKey(f.ctx, &types.MsgRotateServiceKey{
			OperatorAddress:                          identity.Address,
			ParticipantType:                          shared.ParticipantType_PARTICIPANT_TYPE_BUILDER,
			ExpectedCurrentServiceAuthorizationNonce: 1,
		})
		require.ErrorContains(t, err, "evidence submissions are pending")
	})

	t.Run("missing index and counter underflow retain primary", func(t *testing.T) {
		for _, mutate := range []string{"index", "counter"} {
			t.Run(mutate, func(t *testing.T) {
				f := initFixture(t)
				require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
				f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(200))
				identity := hubIdentity(t, byte(203+len(mutate)))
				registerBuilderIdentityForTest(t, f, identity, 10)
				locator := busObjectiveEvidenceLocator(identity, "atomic-session-"+mutate, "atomic-task-"+mutate)
				receipt, err := f.keeper.AcquireBusObjectiveEvidenceResponsibility(f.ctx, locator)
				require.NoError(t, err)
				primaryKey := types.NewServiceKeyResponsibilityKey(
					shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, identity.Address, receipt.ResponsibilityId,
				)
				state, err := f.keeper.ReadServiceKeyResponsibilityValue(f.ctx, primaryKey)
				require.NoError(t, err)
				indexKey := types.NewServiceKeyResponsibilityByTaskKey(
					state.SessionId, state.TaskId, state.ParticipantType, state.OperatorAddress, state.ResponsibilityId,
				)
				if mutate == "index" {
					require.NoError(t, f.keeper.ServiceKeyResponsibilityByTaskIndex.Remove(f.ctx, indexKey))
				} else {
					builder, err := f.keeper.GetBuilderState(f.ctx, identity.Address)
					require.NoError(t, err)
					builder.PendingEvidenceSubmissionCount = 0
					require.NoError(t, f.keeper.StoreBuilder(f.ctx, identity.Address, builder))
				}

				_, err = f.keeper.ReleaseBusObjectiveEvidenceResponsibility(f.ctx, locator)
				require.Error(t, err)
				has, getErr := f.keeper.ServiceKeyResponsibility.Has(f.ctx, primaryKey)
				require.NoError(t, getErr)
				require.True(t, has)
			})
		}
	})
}

func TestBusObjectiveEvidenceResponsibilityGenesisConsistency(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(200))
	identity := hubIdentity(t, 211)
	registerBuilderIdentityForTest(t, f, identity, 10)
	locator := busObjectiveEvidenceLocator(identity, "genesis-session", "genesis-task")
	receipt, err := f.keeper.AcquireBusObjectiveEvidenceResponsibility(f.ctx, locator)
	require.NoError(t, err)
	fault := types.BuilderFaultState{
		BuilderAddress:          identity.Address,
		FaultId:                 hubHashBytes("genesis-builder-fault"),
		FaultKind:               types.BuilderFaultKind_BUILDER_FAULT_KIND_OBJECTIVE_DATA_UNAVAILABLE,
		FaultStatus:             types.BuilderFaultStatus_BUILDER_FAULT_STATUS_RECORDED,
		FaultHeight:             200,
		EvidenceId:              hubHashBytes("genesis-builder-evidence"),
		CanonicalEvidenceDigest: hubHashBytes("genesis-builder-digest"),
		ScopeId:                 hubHashBytes("genesis-builder-scope"),
		PruneHeight:             250,
	}
	require.NoError(t, f.keeper.WriteBuilderFaultValue(f.ctx, types.NewBuilderFaultKey(fault.BuilderAddress, fault.FaultId), fault))
	require.NoError(t, f.keeper.BuilderFaultPruneIndex.Set(f.ctx, types.NewBuilderFaultPruneKey(fault.PruneHeight, fault.BuilderAddress, fault.FaultId)))
	storedFault, err := f.keeper.BuilderFault.Get(f.ctx, types.NewBuilderFaultKey(fault.BuilderAddress, fault.FaultId))
	require.NoError(t, err)
	rawBuilder, err := sdk.AccAddressFromBech32(fault.BuilderAddress)
	require.NoError(t, err)
	require.Equal(t, []byte(rawBuilder), storedFault.BuilderAddress)

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	require.Len(t, exported.ServiceKeyResponsibilities, 1)
	require.Len(t, exported.BuilderFaults, 1)
	require.Equal(t, uint32(1), exported.Builders[0].PendingEvidenceSubmissionCount)
	restarted := initFixture(t)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	indexKey := types.NewServiceKeyResponsibilityByTaskKey(
		hex.EncodeToString(locator.SessionId), hex.EncodeToString(locator.TaskId),
		shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, identity.Address, receipt.ResponsibilityId,
	)
	has, err := restarted.keeper.ServiceKeyResponsibilityByTaskIndex.Has(restarted.ctx, indexKey)
	require.NoError(t, err)
	require.True(t, has)

	badCount := *exported
	badCount.Builders = append([]types.BuilderState(nil), exported.Builders...)
	badCount.Builders[0].PendingEvidenceSubmissionCount = 0
	require.ErrorContains(t, badCount.Validate(), "pending_evidence_submission_count")

	badNonce := *exported
	badNonce.ServiceKeyResponsibilities = append(
		[]types.ServiceKeyResponsibilityState(nil), exported.ServiceKeyResponsibilities...,
	)
	badNonce.ServiceKeyResponsibilities[0].ServiceAuthorizationNonce++
	require.ErrorContains(t, badNonce.Validate(), "does not match Builder binding")

	badID := *exported
	badID.ServiceKeyResponsibilities = append(
		[]types.ServiceKeyResponsibilityState(nil), exported.ServiceKeyResponsibilities...,
	)
	badID.ServiceKeyResponsibilities[0].ResponsibilityId = bytes.Repeat([]byte{0x7f}, 32)
	rejected := initFixture(t)
	require.ErrorContains(t, rejected.keeper.InitGenesis(rejected.ctx, badID), "registered preimage")
}
