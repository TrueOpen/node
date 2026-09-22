package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

const (
	batchTestHeight = uint64(10)
	batchTestEpoch  = uint64(0)
)

// declaredSupportForTest brings one operator to "declared support, never
// activated" for a profile: the state MsgDeclareModelSupport alone produces.
func declaredSupportForTest(t *testing.T, f *fixture, operator, modelID string, profileVersion uint32) {
	t.Helper()
	registerTestModelProfile(t, f, modelID, profileVersion, testServiceBondMinInitial, batchTestHeight)
	result, err := f.keeper.DeclareModelSupport(
		f.ctx, operator, modelID, profileVersion, true, true, batchTestHeight,
	)
	require.NoError(t, err)
	require.False(t, result.Support.SupportActive)
	require.Equal(t, types.ModelSupportActivationNone, result.Support.ActivationKind)
}

// activateSupportForTest pins the activation facts a completed task would have
// written. RecordTaskSupportCompletion needs a RELEASED task liability, which is
// out of scope here; only activation_kind matters to the daily refresh, so the
// fixture writes the minimal VERIFIER-assigned activation the state validator
// accepts. The stake snapshots and profile aggregates are left exactly as
// DeclareModelSupport wrote them, so applyModelSupportMutation still balances.
func activateSupportForTest(t *testing.T, f *fixture, operator, modelID string, profileVersion uint32, marker string) {
	t.Helper()
	support, err := f.keeper.GetModelSupportState(f.ctx, operator, modelID, profileVersion)
	require.NoError(t, err)
	support.ActivationKind = types.ModelSupportActivationVerifierAssignedValid
	support.FirstActivationDuty = shared.DutyVerifier
	support.FirstSupportTaskId = hubHashBytes("support-activation-" + marker)
	require.NoError(t, support.Validate())
	require.NoError(t, f.keeper.ModelSupport.Set(
		f.ctx, types.NewModelSupportKey(operator, modelID, profileVersion), support,
	))
}

// supportConfirmationForTest builds one service-key-signed confirmation over the
// given profiles, exactly as a Cortex node would before handing it to a relayer.
func supportConfirmationForTest(
	t *testing.T,
	f *fixture,
	operator string,
	identity hubTestIdentity,
	profiles []types.ProfileKeyV1,
) types.ModelSupportConfirmationV1 {
	t.Helper()
	operatorBytes, err := sdk.AccAddressFromBech32(operator)
	require.NoError(t, err)
	node, err := f.keeper.GetCortexNodeState(f.ctx, operator)
	require.NoError(t, err)
	expiryHeight := batchTestHeight + 100
	signingBytes, err := types.CanonicalDailySupportConfirmationSigningBytesV1(
		sdk.UnwrapSDKContext(f.ctx).ChainID(), operatorBytes, batchTestEpoch,
		node.ServiceAuthorizationNonce, expiryHeight, profiles,
	)
	require.NoError(t, err)
	return types.ModelSupportConfirmationV1{
		OperatorAddress: operator, SupportedProfiles: profiles,
		ServiceAuthorizationNonce: node.ServiceAuthorizationNonce,
		ExpiryHeight:              expiryHeight,
		ServiceSignature:          hubSign(t, identity, signingBytes),
	}
}

func batchConfirmCtxForTest(f *fixture) sdk.Context {
	return sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(batchTestHeight))
}

// initBatchFixture is initFixture plus the default Hub params every path in this
// file reads (epoch length, support window, batch bounds, bond floor).
func initBatchFixture(t *testing.T) *fixture {
	t.Helper()
	f := initFixture(t)
	require.NoError(t, f.keeper.Params.Set(f.ctx, types.DefaultHubParams()))
	return f
}

// registerSupportOperatorForTest stakes an operator and puts its bond in force so
// requireSupportScope admits it.
func registerSupportOperatorForTest(t *testing.T, f *fixture, operator string, identity hubTestIdentity) {
	t.Helper()
	registerCortexNodeIdentityForTest(
		t, f, operator, identity, testServiceBondMinInitial, batchTestHeight, batchTestEpoch,
	)
	activateServiceBondForTest(t, f, operator, batchTestEpoch)
}

// TestBatchConfirmModelSupportSkipsUnactivatedProfile pins the per-item skip.
//
// refreshModelSupport refuses a declaration that never earned an activation, and
// that refusal used to abort processModelSupportBatch outright. An operator
// listing one activated and one merely declared profile therefore had its whole
// confirmation rejected, even though the activated half was refreshable. The
// unactivated item must now be skipped and the rest of the list applied.
func TestBatchConfirmModelSupportSkipsUnactivatedProfile(t *testing.T) {
	f := initBatchFixture(t)
	operator := hubAddress(t, 0x11)
	identity := hubIdentity(t, 0x31)
	registerSupportOperatorForTest(t, f, operator, identity)

	const activatedModel = "model-activated"
	const declaredModel = "model-declared-only"
	declaredSupportForTest(t, f, operator, activatedModel, 1)
	declaredSupportForTest(t, f, operator, declaredModel, 1)
	activateSupportForTest(t, f, operator, activatedModel, 1, "single-operator")

	// Sorted and unique by (model_id, profile_version), as the batch validator
	// requires: "model-activated" < "model-declared-only".
	profiles := []types.ProfileKeyV1{
		{ModelId: activatedModel, ProfileVersion: 1},
		{ModelId: declaredModel, ProfileVersion: 1},
	}
	confirmation := supportConfirmationForTest(t, f, operator, identity, profiles)

	before, err := f.keeper.GetModelSupportState(f.ctx, operator, declaredModel, 1)
	require.NoError(t, err)

	response, err := keeper.NewMsgServerImpl(f.keeper).BatchConfirmModelSupport(
		batchConfirmCtxForTest(f), &types.MsgBatchConfirmModelSupport{
			EpochIndex:       batchTestEpoch,
			Confirmations:    []types.ModelSupportConfirmationV1{confirmation},
			SubmitterAddress: hubAddress(t, 0x77),
		},
	)
	require.NoError(t, err)
	require.Equal(t, uint32(1), response.AcceptedConfirmations)
	require.Equal(t, uint32(1), response.RefreshedProfileCount, "only the activated profile refreshes")
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Status)

	activated, err := f.keeper.GetModelSupportState(f.ctx, operator, activatedModel, 1)
	require.NoError(t, err)
	require.True(t, activated.SupportActive, "the activated profile is refreshed into active support")
	require.Equal(t, batchTestHeight, activated.LastRefreshHeight)

	// A skipped item writes nothing at all: same row, byte for byte.
	after, err := f.keeper.GetModelSupportState(f.ctx, operator, declaredModel, 1)
	require.NoError(t, err)
	require.Equal(t, before, after, "the skipped profile row is untouched")
}

// TestBatchConfirmModelSupportIsolatesOperators pins the blast radius.
//
// A batch is assembled by a relayer from independently signed confirmations, so
// one operator's stale item must not discard another operator's. Before the skip
// this aborted the whole Tx, which made a shared batch cheap to deny service to.
func TestBatchConfirmModelSupportIsolatesOperators(t *testing.T) {
	f := initBatchFixture(t)
	// Confirmations must be sorted ascending by decoded operator address bytes,
	// and these two addresses are 20 repeated fill bytes, so 0x11 sorts first.
	staleOperator := hubAddress(t, 0x11)
	staleIdentity := hubIdentity(t, 0x31)
	healthyOperator := hubAddress(t, 0x22)
	healthyIdentity := hubIdentity(t, 0x32)
	registerSupportOperatorForTest(t, f, staleOperator, staleIdentity)
	registerSupportOperatorForTest(t, f, healthyOperator, healthyIdentity)

	const sharedModel = "model-shared"
	declaredSupportForTest(t, f, staleOperator, sharedModel, 1)
	// The healthy operator supports the same profile, so only its activation state
	// differs from the stale one.
	_, err := f.keeper.DeclareModelSupport(
		f.ctx, healthyOperator, sharedModel, 1, true, true, batchTestHeight,
	)
	require.NoError(t, err)
	activateSupportForTest(t, f, healthyOperator, sharedModel, 1, "two-operators")

	profiles := []types.ProfileKeyV1{{ModelId: sharedModel, ProfileVersion: 1}}
	confirmations := []types.ModelSupportConfirmationV1{
		supportConfirmationForTest(t, f, staleOperator, staleIdentity, profiles),
		supportConfirmationForTest(t, f, healthyOperator, healthyIdentity, profiles),
	}

	response, err := keeper.NewMsgServerImpl(f.keeper).BatchConfirmModelSupport(
		batchConfirmCtxForTest(f), &types.MsgBatchConfirmModelSupport{
			EpochIndex:       batchTestEpoch,
			Confirmations:    confirmations,
			SubmitterAddress: hubAddress(t, 0x77),
		},
	)
	require.NoError(t, err, "a stale item must not reject the batch")
	require.Equal(t, uint32(2), response.AcceptedConfirmations)
	require.Equal(t, uint32(1), response.RefreshedProfileCount)

	healthy, err := f.keeper.GetModelSupportState(f.ctx, healthyOperator, sharedModel, 1)
	require.NoError(t, err)
	require.True(t, healthy.SupportActive, "the healthy operator's refresh still applies")

	// Both confirmations are recorded, so neither can be replayed this epoch.
	for _, operator := range []string{staleOperator, healthyOperator} {
		has, err := f.keeper.DailySupport.Has(f.ctx, types.NewDailySupportKey(batchTestEpoch, operator))
		require.NoError(t, err)
		require.True(t, has, "accepted confirmation is recorded for %s", operator)
	}
}

// TestBatchConfirmModelSupportRejectsUnsignedConfirmation guards the skip against
// scope creep: only refresh preconditions are skippable, and a malformed or
// unauthorised confirmation must still reject the whole batch.
func TestBatchConfirmModelSupportRejectsUnsignedConfirmation(t *testing.T) {
	f := initBatchFixture(t)
	operator := hubAddress(t, 0x11)
	identity := hubIdentity(t, 0x31)
	registerSupportOperatorForTest(t, f, operator, identity)

	const modelID = "model-activated"
	declaredSupportForTest(t, f, operator, modelID, 1)
	activateSupportForTest(t, f, operator, modelID, 1, "bad-signature")

	profiles := []types.ProfileKeyV1{{ModelId: modelID, ProfileVersion: 1}}
	confirmation := supportConfirmationForTest(t, f, operator, identity, profiles)
	// Signed by a key that is not this operator's current service key.
	confirmation.ServiceSignature = supportConfirmationForTest(
		t, f, operator, hubIdentity(t, 0x41), profiles,
	).ServiceSignature

	_, err := keeper.NewMsgServerImpl(f.keeper).BatchConfirmModelSupport(
		batchConfirmCtxForTest(f), &types.MsgBatchConfirmModelSupport{
			EpochIndex:       batchTestEpoch,
			Confirmations:    []types.ModelSupportConfirmationV1{confirmation},
			SubmitterAddress: hubAddress(t, 0x77),
		},
	)
	require.Error(t, err, "an unauthorised confirmation still rejects the batch")
}
