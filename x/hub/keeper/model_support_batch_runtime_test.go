package keeper_test

import (
	"bytes"
	"sort"
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

// declaredSupportForTest creates model-level support without task activation.
func declaredSupportForTest(t *testing.T, f *fixture, operator, modelLabel string, profileVersion uint32) []byte {
	t.Helper()
	registerTestModelProfile(t, f, modelLabel, profileVersion, testServiceBondMinInitial, batchTestHeight)
	modelID := testModelID(testHubChainID, hubAddress(t, 250), modelLabel)
	result, err := f.keeper.DeclareModelSupport(
		f.ctx, operator, modelID, true, true, batchTestHeight,
	)
	require.NoError(t, err)
	require.False(t, result.Support.SupportActive)
	require.Equal(t, types.ModelSupportActivationNone, result.Support.ActivationKind)
	return modelID
}

// activateSupportForTest pins the activation facts a completed task would have
// written. RecordTaskSupportCompletion needs a RELEASED task liability, which is
// out of scope here; only activation_kind matters to the daily refresh, so the
// fixture writes the minimal VERIFIER-assigned activation the state validator
// accepts. The stake snapshots and model aggregates are left exactly as
// DeclareModelSupport wrote them, so applyModelSupportMutation still balances.
func activateSupportForTest(t *testing.T, f *fixture, operator string, modelID []byte, profileVersion uint32, marker string) {
	t.Helper()
	support, err := f.keeper.GetModelSupportState(f.ctx, operator, modelID)
	require.NoError(t, err)
	support.ActivationKind = types.ModelSupportActivationVerifierAssignedValid
	support.FirstActivationDuty = shared.DutyVerifier
	support.FirstSupportTaskId = hubHashBytes("support-activation-" + marker)
	support.FirstSupportProfileVersion = profileVersion
	require.NoError(t, support.Validate())
	require.NoError(t, f.keeper.ModelSupport.Set(
		f.ctx, types.NewModelSupportKey(operator, modelID), support,
	))
}

// supportConfirmationForTest builds one service-key-signed confirmation over the
// given models, exactly as a Cortex node would before handing it to a relayer.
func supportConfirmationForTest(
	t *testing.T,
	f *fixture,
	operator string,
	identity hubTestIdentity,
	models [][]byte,
) types.ModelSupportConfirmationV1 {
	t.Helper()
	operatorBytes, err := sdk.AccAddressFromBech32(operator)
	require.NoError(t, err)
	node, err := f.keeper.GetCortexNodeState(f.ctx, operator)
	require.NoError(t, err)
	expiryHeight := batchTestHeight + 100
	signingBytes, err := types.CanonicalDailySupportConfirmationSigningBytesV1(
		sdk.UnwrapSDKContext(f.ctx).ChainID(), operatorBytes, batchTestEpoch,
		node.ServiceAuthorizationNonce, expiryHeight, models,
	)
	require.NoError(t, err)
	return types.ModelSupportConfirmationV1{
		OperatorAddress: operator, SupportedModels: models,
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

// TestBatchConfirmModelSupportSkipsUnactivatedModel pins the per-item skip.
//
// refreshModelSupport refuses a declaration that never earned an activation, and
// that refusal used to abort processModelSupportBatch outright. An operator
// listing one activated and one merely declared model therefore had its whole
// confirmation rejected, even though the activated half was refreshable. The
// unactivated item must now be skipped and the rest of the list applied.
func TestBatchConfirmModelSupportSkipsUnactivatedModel(t *testing.T) {
	f := initBatchFixture(t)
	operator := hubAddress(t, 0x11)
	identity := hubIdentity(t, 0x31)
	registerSupportOperatorForTest(t, f, operator, identity)

	const activatedModel = "model-activated"
	const declaredModel = "model-declared-only"
	activatedID := declaredSupportForTest(t, f, operator, activatedModel, 1)
	declaredID := declaredSupportForTest(t, f, operator, declaredModel, 1)
	activateSupportForTest(t, f, operator, activatedID, 1, "single-operator")

	models := [][]byte{activatedID, declaredID}
	sort.Slice(models, func(i, j int) bool { return bytes.Compare(models[i], models[j]) < 0 })
	confirmation := supportConfirmationForTest(t, f, operator, identity, models)

	before, err := f.keeper.GetModelSupportState(f.ctx, operator, declaredID)
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
	require.Equal(t, uint32(1), response.RefreshedModelCount, "only the activated model refreshes")
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Status)

	activated, err := f.keeper.GetModelSupportState(f.ctx, operator, activatedID)
	require.NoError(t, err)
	require.True(t, activated.SupportActive, "the activated model is refreshed into active support")
	require.Equal(t, batchTestHeight, activated.LastRefreshHeight)

	// A skipped item writes nothing at all: same row, byte for byte.
	after, err := f.keeper.GetModelSupportState(f.ctx, operator, declaredID)
	require.NoError(t, err)
	require.Equal(t, before, after, "the skipped model row is untouched")
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
	modelID := declaredSupportForTest(t, f, staleOperator, sharedModel, 1)
	// The healthy operator supports the same model, so only its activation state
	// differs from the stale one.
	_, err := f.keeper.DeclareModelSupport(
		f.ctx, healthyOperator, modelID, true, true, batchTestHeight,
	)
	require.NoError(t, err)
	activateSupportForTest(t, f, healthyOperator, modelID, 1, "two-operators")

	models := [][]byte{modelID}
	confirmations := []types.ModelSupportConfirmationV1{
		supportConfirmationForTest(t, f, staleOperator, staleIdentity, models),
		supportConfirmationForTest(t, f, healthyOperator, healthyIdentity, models),
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
	require.Equal(t, uint32(1), response.RefreshedModelCount)

	healthy, err := f.keeper.GetModelSupportState(f.ctx, healthyOperator, modelID)
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
	modelHash := declaredSupportForTest(t, f, operator, modelID, 1)
	activateSupportForTest(t, f, operator, modelHash, 1, "bad-signature")

	models := [][]byte{modelHash}
	confirmation := supportConfirmationForTest(t, f, operator, identity, models)
	// Signed by a key that is not this operator's current service key.
	confirmation.ServiceSignature = supportConfirmationForTest(
		t, f, operator, hubIdentity(t, 0x41), models,
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
