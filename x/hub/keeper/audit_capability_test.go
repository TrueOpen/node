package keeper_test

import (
	"context"
	"errors"
	"testing"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// consensusIdentityStub answers the block-producer lookup and records what it
// was asked about.
type consensusIdentityStub struct {
	overlap bool
	err     error
	calls   *int
	pubkey  *[]byte
}

func (s consensusIdentityStub) IsConsensusIdentity(_ context.Context, operator, _ sdk.AccAddress, servicePubkey []byte) (bool, error) {
	if s.calls != nil {
		*s.calls++
	}
	if s.pubkey != nil {
		*s.pubkey = append([]byte(nil), servicePubkey...)
	}
	if len(operator) == 0 {
		return false, errors.New("operator is required")
	}
	return s.overlap, s.err
}

func auditFixture(t *testing.T, checker types.ConsensusIdentityChecker) (*fixture, string, []byte) {
	t.Helper()
	f := initBatchFixture(t)
	params := types.DefaultHubParams()
	params.Service.MinValidatorSeparation = true
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	if checker != nil {
		f.keeper = f.keeper.WithConsensusIdentityChecker(checker)
	}
	operator := hubAddress(t, 0x11)
	registerSupportOperatorForTest(t, f, operator, hubIdentity(t, 0x31))
	registerTestModelProfile(t, f, "audit-model", 1, testServiceBondMinInitial, batchTestHeight)
	return f, operator, testModelID(testHubChainID, hubAddress(t, 250), "audit-model")
}

func auditSnapshot(t *testing.T, f *fixture, operator string, modelID []byte) (types.ModelCapabilitySnapshot, bool) {
	t.Helper()
	address, err := sdk.AccAddressFromBech32(operator)
	require.NoError(t, err)
	return f.keeper.GetModelCapability(sdk.UnwrapSDKContext(f.ctx), address, modelID)
}

// A node with neither inference nor verification capability may declare panel
// audit. The row is declared and fresh, never active, adds nothing to the
// model's support aggregates and becomes drawable in the next epoch.
func TestAuditOnlyDeclarationIsAcceptedAndCarriesNoSupportWeight(t *testing.T) {
	calls := 0
	var seenPubkey []byte
	f, operator, modelID := auditFixture(t, consensusIdentityStub{calls: &calls, pubkey: &seenPubkey})
	before, err := f.keeper.GetModel(f.ctx, modelID)
	require.NoError(t, err)

	result, err := f.keeper.DeclareModelSupport(f.ctx, operator, modelID, false, false, true, batchTestHeight)
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, result.Status)
	require.Equal(t, 1, calls)
	require.Equal(t, hubIdentity(t, 0x31).PubKey, seenPubkey)
	require.True(t, result.Capability.AuditCapability)
	require.False(t, result.Capability.InferenceCapability)
	require.False(t, result.Capability.VerificationCapability)
	require.Equal(t, uint64(1), result.Capability.CapabilityVersion)
	require.True(t, result.Support.DeclaredSupport)
	require.False(t, result.Support.SupportActive)
	require.Zero(t, result.Support.ActiveSupportStakeSnapshot)
	require.Equal(t, types.ModelSupportActivationNone, result.Support.ActivationKind)
	require.Equal(t, types.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_NONE, result.Support.SuspendReason)
	require.Equal(t, uint64(2), result.Support.SupportFreshUntilEpoch)

	after, err := f.keeper.GetModel(f.ctx, modelID)
	require.NoError(t, err)
	require.Equal(t, before.ActiveSupportStake, after.ActiveSupportStake)
	require.Equal(t, before.ActiveSupporterCount, after.ActiveSupporterCount)
	require.Equal(t, before.Status, after.Status)

	snapshot, ok := auditSnapshot(t, f, operator, modelID)
	require.True(t, ok)
	require.True(t, snapshot.AuditCapability)
	require.Equal(t, uint64(1), snapshot.AuditEffectiveFromEpoch, "declared in epoch 0, drawable from epoch 1")

	requireSupportInvariants(t, f)
}

// Without a block-producer lookup every audit declaration is refused, and the
// existing capabilities are untouched by that.
func TestAuditDeclarationIsRefusedWithoutIdentityLookup(t *testing.T) {
	f, operator, modelID := auditFixture(t, nil)

	_, err := f.keeper.DeclareModelSupport(f.ctx, operator, modelID, false, false, true, batchTestHeight)
	require.ErrorContains(t, err, "not available")
	_, err = f.keeper.DeclareModelSupport(f.ctx, operator, modelID, true, false, true, batchTestHeight)
	require.ErrorContains(t, err, "not available")
	_, err = f.keeper.GetModelCapabilityState(f.ctx, operator, modelID)
	require.Error(t, err, "a refused declaration writes nothing")

	result, err := f.keeper.DeclareModelSupport(f.ctx, operator, modelID, true, true, false, batchTestHeight)
	require.NoError(t, err)
	require.False(t, result.Capability.AuditCapability)
}

// A block-producer identity cannot declare panel audit. The same identity keeps
// its right to declare inference or verification, and the lookup is not even
// consulted for those.
func TestAuditDeclarationIsRefusedForBlockProducerIdentity(t *testing.T) {
	calls := 0
	f, operator, modelID := auditFixture(t, consensusIdentityStub{overlap: true, calls: &calls})

	_, err := f.keeper.DeclareModelSupport(f.ctx, operator, modelID, false, false, true, batchTestHeight)
	require.ErrorContains(t, err, "block-producer identity")
	_, err = f.keeper.DeclareModelSupport(f.ctx, operator, modelID, true, true, true, batchTestHeight)
	require.ErrorContains(t, err, "block-producer identity")
	require.Equal(t, 2, calls)

	result, err := f.keeper.DeclareModelSupport(f.ctx, operator, modelID, true, true, false, batchTestHeight)
	require.NoError(t, err)
	require.Equal(t, 2, calls, "a declaration without audit does not consult the lookup")
	require.False(t, result.Capability.AuditCapability)
	snapshot, ok := auditSnapshot(t, f, operator, modelID)
	require.True(t, ok)
	require.False(t, snapshot.AuditCapability)
	require.Zero(t, snapshot.AuditEffectiveFromEpoch)
}

// A failing lookup refuses the declaration instead of accepting it unchecked.
func TestAuditDeclarationFailsClosedWhenLookupFails(t *testing.T) {
	f, operator, modelID := auditFixture(t, consensusIdentityStub{err: errors.New("validator set unavailable")})
	_, err := f.keeper.DeclareModelSupport(f.ctx, operator, modelID, false, false, true, batchTestHeight)
	require.ErrorContains(t, err, "validator set unavailable")
	_, err = f.keeper.GetModelCapabilityState(f.ctx, operator, modelID)
	require.Error(t, err)
}

// Repeating an audit-only declaration is its renewal: freshness moves forward,
// the effective epoch does not move back, and a repeat that would not extend
// freshness stays a no-op.
func TestAuditOnlyDeclarationRenewsByRepetition(t *testing.T) {
	f, operator, modelID := auditFixture(t, consensusIdentityStub{})
	first, err := f.keeper.DeclareModelSupport(f.ctx, operator, modelID, false, false, true, batchTestHeight)
	require.NoError(t, err)

	same, err := f.keeper.DeclareModelSupport(f.ctx, operator, modelID, false, false, true, batchTestHeight+1)
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, same.Status)
	require.Equal(t, first.Support, same.Support)

	nextEpochHeight := testEpochLength + 5
	renewed, err := f.keeper.DeclareModelSupport(f.ctx, operator, modelID, false, false, true, nextEpochHeight)
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, renewed.Status)
	require.Equal(t, uint64(3), renewed.Support.SupportFreshUntilEpoch)
	require.Equal(t, nextEpochHeight, renewed.Support.LastRefreshHeight)
	require.Equal(t, first.Capability.CapabilityVersion, renewed.Capability.CapabilityVersion)
	require.Equal(t, first.Support.SupportVersion+1, renewed.Support.SupportVersion)
	require.False(t, renewed.Support.SupportActive)

	snapshot, ok := auditSnapshot(t, f, operator, modelID)
	require.True(t, ok)
	require.Equal(t, uint64(1), snapshot.AuditEffectiveFromEpoch, "a renewal keeps the effective epoch")

	// The old expiry entry is gone and the new one is present.
	has, err := f.keeper.ModelSupportExpiryIndex.Has(f.ctx, types.NewModelSupportExpiryIndexKey(2, operator, modelID))
	require.NoError(t, err)
	require.False(t, has)
	has, err = f.keeper.ModelSupportExpiryIndex.Has(f.ctx, types.NewModelSupportExpiryIndexKey(3, operator, modelID))
	require.NoError(t, err)
	require.True(t, has)
	requireSupportInvariants(t, f)
}

// A declaration that lapsed and is made again waits one epoch again.
func TestLapsedAuditDeclarationWaitsAgain(t *testing.T) {
	f, operator, modelID := auditFixture(t, consensusIdentityStub{})
	_, err := f.keeper.DeclareModelSupport(f.ctx, operator, modelID, false, false, true, batchTestHeight)
	require.NoError(t, err)

	lapsedHeight := testEpochLength*5 + 1
	again, err := f.keeper.DeclareModelSupport(f.ctx, operator, modelID, false, false, true, lapsedHeight)
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, again.Status)
	require.Equal(t, uint64(7), again.Support.SupportFreshUntilEpoch)
	snapshot, ok := auditSnapshot(t, f, operator, modelID)
	require.True(t, ok)
	require.Equal(t, uint64(6), snapshot.AuditEffectiveFromEpoch)
}

// Adding the audit flag to a live inference/verification row waits one epoch
// from the change; dropping it clears the record; the unchanged legacy
// declaration stays a no-op exactly as before.
func TestAuditFlagOnAnExistingSupportRow(t *testing.T) {
	f, operator, modelID := auditFixture(t, consensusIdentityStub{})
	legacy, err := f.keeper.DeclareModelSupport(f.ctx, operator, modelID, true, true, false, batchTestHeight)
	require.NoError(t, err)
	repeat, err := f.keeper.DeclareModelSupport(f.ctx, operator, modelID, true, true, false, testEpochLength+1)
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, repeat.Status)
	require.Equal(t, legacy.Support, repeat.Support, "an unchanged legacy declaration is never a renewal")
	_, err = f.keeper.AuditCapabilityDeclaredEpoch.Get(f.ctx, types.NewModelCapabilityKey(operator, modelID))
	require.ErrorIs(t, err, collections.ErrNotFound)

	added, err := f.keeper.DeclareModelSupport(f.ctx, operator, modelID, true, true, true, testEpochLength+2)
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, added.Status)
	require.Equal(t, legacy.Capability.CapabilityVersion+1, added.Capability.CapabilityVersion)
	require.Equal(t, legacy.Support.SupportFreshUntilEpoch, added.Support.SupportFreshUntilEpoch, "a live row keeps its freshness")
	require.Equal(t, legacy.Support.LastRefreshHeight, added.Support.LastRefreshHeight)
	snapshot, ok := auditSnapshot(t, f, operator, modelID)
	require.True(t, ok)
	require.True(t, snapshot.AuditCapability)
	require.Equal(t, uint64(2), snapshot.AuditEffectiveFromEpoch)

	mixedRepeat, err := f.keeper.DeclareModelSupport(f.ctx, operator, modelID, true, true, true, testEpochLength+3)
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, mixedRepeat.Status, "only an audit-only row renews by repetition")

	dropped, err := f.keeper.DeclareModelSupport(f.ctx, operator, modelID, true, true, false, testEpochLength+4)
	require.NoError(t, err)
	require.False(t, dropped.Capability.AuditCapability)
	_, err = f.keeper.AuditCapabilityDeclaredEpoch.Get(f.ctx, types.NewModelCapabilityKey(operator, modelID))
	require.ErrorIs(t, err, collections.ErrNotFound)
	requireSupportInvariants(t, f)
}

// The message accepts audit as the only capability and still refuses a
// declaration with none.
func TestDeclareModelSupportMessageCarriesAuditCapability(t *testing.T) {
	f, operator, modelID := auditFixture(t, nil)
	server := keeper.NewMsgServerImpl(f.keeper, types.MsgServerDependencies{ConsensusIdentityChecker: consensusIdentityStub{}})
	ctx := sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(batchTestHeight)))

	_, err := server.DeclareModelSupport(ctx, &types.MsgDeclareModelSupport{OperatorAddress: operator, ModelId: modelID})
	require.ErrorIs(t, err, types.ErrInvalidModelCapability)

	response, err := server.DeclareModelSupport(ctx, &types.MsgDeclareModelSupport{
		OperatorAddress: operator, ModelId: modelID, AuditCapability: true,
	})
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Status)
	require.False(t, response.SupportActive)
	require.Equal(t, uint64(2), response.FreshUntilEpoch)
	capability, err := f.keeper.GetModelCapabilityState(f.ctx, operator, modelID)
	require.NoError(t, err)
	require.True(t, capability.AuditCapability)
	require.NoError(t, capability.Validate())
	requireSupportInvariants(t, f)
}

// requireSupportInvariants checks the support aggregates and the
// support/capability/index agreement over the fixture state.
func requireSupportInvariants(t *testing.T, f *fixture) {
	t.Helper()
	require.NoError(t, f.keeper.EnsureSupportAggregateInvariant(f.ctx))
	require.NoError(t, f.keeper.EnsureSupportLifecycleInvariant(f.ctx))
}

// With min_validator_separation unset (the default) the rule is off: the
// declaration is accepted without consulting the lookup, bound or not.
func TestAuditDeclarationWithoutSeparationRule(t *testing.T) {
	calls := 0
	f, operator, modelID := auditFixture(t, consensusIdentityStub{overlap: true, calls: &calls})
	require.False(t, types.DefaultHubParams().Service.MinValidatorSeparation)
	require.NoError(t, f.keeper.Params.Set(f.ctx, types.DefaultHubParams()))

	result, err := f.keeper.DeclareModelSupport(f.ctx, operator, modelID, false, false, true, batchTestHeight)
	require.NoError(t, err)
	require.True(t, result.Capability.AuditCapability)
	require.Zero(t, calls)

	unbound, operator2, modelID2 := auditFixture(t, nil)
	require.NoError(t, unbound.keeper.Params.Set(unbound.ctx, types.DefaultHubParams()))
	_, err = unbound.keeper.DeclareModelSupport(unbound.ctx, operator2, modelID2, false, false, true, batchTestHeight)
	require.NoError(t, err)
}

// confirmSupportAt sends one operator's daily support confirmation for the
// epoch of height, as a relayer would, and returns how many rows it refreshed.
func confirmSupportAt(t *testing.T, f *fixture, operator string, identity hubTestIdentity, height uint64, models ...[]byte) uint32 {
	t.Helper()
	epoch := height / testEpochLength
	operatorBytes, err := sdk.AccAddressFromBech32(operator)
	require.NoError(t, err)
	node, err := f.keeper.GetCortexNodeState(f.ctx, operator)
	require.NoError(t, err)
	expiry := height + 100
	signingBytes, err := types.CanonicalDailySupportConfirmationSigningBytesV1(
		sdk.UnwrapSDKContext(f.ctx).ChainID(), operatorBytes, epoch, node.ServiceAuthorizationNonce, expiry, models,
	)
	require.NoError(t, err)
	response, err := keeper.NewMsgServerImpl(f.keeper).BatchConfirmModelSupport(
		sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(height)), &types.MsgBatchConfirmModelSupport{
			EpochIndex: epoch, SubmitterAddress: hubAddress(t, 0x77),
			Confirmations: []types.ModelSupportConfirmationV1{{
				OperatorAddress: operator, SupportedModels: models,
				ServiceAuthorizationNonce: node.ServiceAuthorizationNonce, ExpiryHeight: expiry,
				ServiceSignature: hubSign(t, identity, signingBytes),
			}},
		},
	)
	require.NoError(t, err)
	return response.RefreshedModelCount
}

// The daily support confirmation, signed by the service key and relayed like
// any other, keeps an audit-only row fresh. The operator key is not needed
// after the declaration.
func TestDailyConfirmationRenewsAuditOnlySupport(t *testing.T) {
	f, operator, modelID := auditFixture(t, consensusIdentityStub{})
	identity := hubIdentity(t, 0x31)
	declared, err := f.keeper.DeclareModelSupport(f.ctx, operator, modelID, false, false, true, batchTestHeight)
	require.NoError(t, err)
	require.Equal(t, uint64(2), declared.Support.SupportFreshUntilEpoch)
	before, err := f.keeper.GetModel(f.ctx, modelID)
	require.NoError(t, err)

	require.Zero(t, confirmSupportAt(t, f, operator, identity, batchTestHeight+1, modelID), "nothing to extend inside the declaration epoch")
	height := testEpochLength + 7
	require.Equal(t, uint32(1), confirmSupportAt(t, f, operator, identity, height, modelID))
	renewed, err := f.keeper.GetModelSupportState(f.ctx, operator, modelID)
	require.NoError(t, err)
	require.Equal(t, uint64(3), renewed.SupportFreshUntilEpoch)
	require.Equal(t, height, renewed.LastRefreshHeight)
	require.Equal(t, declared.Support.SupportVersion+1, renewed.SupportVersion)
	require.True(t, renewed.DeclaredSupport)
	require.False(t, renewed.SupportActive, "a confirmation never activates an audit-only row")
	require.Zero(t, renewed.ActiveSupportStakeSnapshot)
	require.Equal(t, types.ModelSupportActivationNone, renewed.ActivationKind)

	after, err := f.keeper.GetModel(f.ctx, modelID)
	require.NoError(t, err)
	require.Equal(t, before.ActiveSupportStake, after.ActiveSupportStake)
	require.Equal(t, before.ActiveSupporterCount, after.ActiveSupporterCount)
	snapshot, ok := auditSnapshot(t, f, operator, modelID)
	require.True(t, ok)
	require.Equal(t, uint64(1), snapshot.AuditEffectiveFromEpoch, "a confirmation does not restart the waiting epoch")
	has, err := f.keeper.ModelSupportExpiryIndex.Has(f.ctx, types.NewModelSupportExpiryIndexKey(3, operator, modelID))
	require.NoError(t, err)
	require.True(t, has)
	has, err = f.keeper.ModelSupportExpiryIndex.Has(f.ctx, types.NewModelSupportExpiryIndexKey(2, operator, modelID))
	require.NoError(t, err)
	require.False(t, has)
	requireSupportInvariants(t, f)

	// Explicit re-declaration still renews as well.
	again, err := f.keeper.DeclareModelSupport(f.ctx, operator, modelID, false, false, true, 2*testEpochLength+1)
	require.NoError(t, err)
	require.Equal(t, uint64(4), again.Support.SupportFreshUntilEpoch)
}

// Without a confirmation or a re-declaration the audit-only row lapses on its
// schedule, like any support.
func TestAuditOnlySupportLapsesWithoutConfirmation(t *testing.T) {
	f, operator, modelID := auditFixture(t, consensusIdentityStub{})
	_, err := f.keeper.DeclareModelSupport(f.ctx, operator, modelID, false, false, true, batchTestHeight)
	require.NoError(t, err)
	row, err := f.keeper.GetModelSupportState(f.ctx, operator, modelID)
	require.NoError(t, err)
	capability, err := f.keeper.GetModelCapabilityState(f.ctx, operator, modelID)
	require.NoError(t, err)
	require.Equal(t, uint64(2), row.SupportFreshUntilEpoch)
	// The row a panel draw would read is stale from epoch 2 on.
	require.Less(t, uint64(1), row.SupportFreshUntilEpoch)
	require.GreaterOrEqual(t, uint64(2), row.SupportFreshUntilEpoch)
	require.True(t, capability.AuditCapability)

	_, err = f.keeper.DeactivateModelSupport(f.ctx, operator, modelID, "SUPPORT_EXPIRED", 2*testEpochLength+1)
	require.NoError(t, err)
	lapsed, err := f.keeper.GetModelSupportState(f.ctx, operator, modelID)
	require.NoError(t, err)
	require.False(t, lapsed.DeclaredSupport)
	require.Zero(t, lapsed.SupportFreshUntilEpoch)
	// A confirmation cannot bring a lapsed declaration back; only the operator can.
	require.Zero(t, confirmSupportAt(t, f, operator, hubIdentity(t, 0x31), 2*testEpochLength+2, modelID))
	still, err := f.keeper.GetModelSupportState(f.ctx, operator, modelID)
	require.NoError(t, err)
	require.Equal(t, lapsed, still)
}

// The refresh rule for inference and verification support is what it was: a
// declared row that never served a task is skipped by the daily confirmation,
// with or without the audit flag next to it.
func TestDailyConfirmationRuleForOtherSupportIsUnchanged(t *testing.T) {
	for name, audit := range map[string]bool{"inference and verification": false, "with the audit flag": true} {
		t.Run(name, func(t *testing.T) {
			f, operator, modelID := auditFixture(t, consensusIdentityStub{})
			_, err := f.keeper.DeclareModelSupport(f.ctx, operator, modelID, true, true, audit, batchTestHeight)
			require.NoError(t, err)
			before, err := f.keeper.GetModelSupportState(f.ctx, operator, modelID)
			require.NoError(t, err)
			require.Zero(t, confirmSupportAt(t, f, operator, hubIdentity(t, 0x31), testEpochLength+7, modelID))
			after, err := f.keeper.GetModelSupportState(f.ctx, operator, modelID)
			require.NoError(t, err)
			require.Equal(t, before, after, "an unactivated row is untouched")

			// Once activated it refreshes into active support, as before.
			activateSupportForTest(t, f, operator, modelID, 1, name)
			// One confirmation per operator and epoch, so this one is for the next epoch.
			require.Equal(t, uint32(1), confirmSupportAt(t, f, operator, hubIdentity(t, 0x31), 2*testEpochLength-1+2, modelID))
			active, err := f.keeper.GetModelSupportState(f.ctx, operator, modelID)
			require.NoError(t, err)
			require.True(t, active.SupportActive)
			require.Equal(t, uint64(4), active.SupportFreshUntilEpoch)
		})
	}
}
