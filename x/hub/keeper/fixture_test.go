package keeper_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"testing"

	collcodec "cosmossdk.io/collections/codec"
	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/keeper"
	module "github.com/TrueOpen/node/x/hub/module"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

type fixture struct {
	ctx    context.Context
	keeper keeper.Keeper
	bank   *hubMockBankKeeper
	auth   *hubMockAuthKeeper
}

// testServiceBondMinInitial mirrors params.Service.ServiceBondMinInitial.
//
// Ruling 16 deleted keeper.MinServiceBond (500_000): it was an unregistered
// duplicate of the §18.0 field 12 parameter and had drifted from it. Every test
// that needs "the minimum a service bond may hold" now reads the parameter.
var testServiceBondMinInitial = func() uint64 {
	value, err := types.AmountToUint64(types.DefaultHubParams().Service.ServiceBondMinInitial, false)
	if err != nil {
		panic(err)
	}
	return value
}()

const (
	testEpochLength = types.DefaultEpochLengthBlocks
	testHubChainID  = "trueopen-hub-test"
)

func initFixture(t *testing.T) *fixture {
	t.Helper()

	encCfg := moduletestutil.MakeTestEncodingConfig(module.AppModule{})
	addressCodec := addresscodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix())
	storeKey := storetypes.NewKVStoreKey(types.StoreKey)
	storeService := runtime.NewKVStoreService(storeKey)
	transientKey := storetypes.NewTransientStoreKey("transient_test")
	transientStoreService := runtime.NewTransientStoreService(transientKey)
	ctx := testutil.DefaultContextWithDB(t, storeKey, transientKey).Ctx
	ctx = ctx.WithChainID(testHubChainID)
	authority := authtypes.NewModuleAddress(types.GovModuleName)
	bank := newHubMockBankKeeper()
	auth := newHubMockAuthKeeper()

	k := keeper.NewKeeper(
		storeService,
		transientStoreService,
		encCfg.Codec,
		addressCodec,
		authority,
		auth,
		bank,
	)

	return &fixture{
		ctx:    ctx,
		keeper: k,
		bank:   bank,
		auth:   auth,
	}
}

func serviceSlashRequestForTest(operator string, duty shared.Duty, marker string, requested, height uint64) keeper.ApplyServiceSlashRequest {
	sourceID := hubHashBytes("service-slash-source-" + marker)
	return keeper.ApplyServiceSlashRequest{
		OperatorAddress: operator, Duty: duty,
		TaskID: hubHashBytes("service-slash-task-" + marker), Requested: requested, Height: height,
		SummaryID: sourceID, SourceKind: types.SlashSourceKind_SLASH_SOURCE_KIND_ROLE_FAULT,
		SourceID: sourceID, Destination: types.SlashDestination_SLASH_DESTINATION_TREASURY,
	}
}

func seedRoleFaultForTest(t *testing.T, f *fixture, operator string, duty shared.Duty, taskID, evidenceDigest []byte, recordedHeight uint64, marker string) types.RoleFaultState {
	t.Helper()
	state := types.RoleFaultState{
		FaultId: hubHashBytes("role-fault-" + marker), TaskId: append([]byte(nil), taskID...),
		OperatorAddress: operator, Duty: duty, FaultClass: types.FaultKind_FAULT_KIND_TIMEOUT,
		ClassificationSource: shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_DEADLINE,
		EvidenceDigest:       append([]byte(nil), evidenceDigest...), RecordedHeight: recordedHeight,
		Status: types.RoleFaultStatus_ROLE_FAULT_STATUS_CONFIRMED,
	}
	require.NoError(t, state.Validate())
	key := shared.Hash32Key(state.FaultId)
	require.NoError(t, f.keeper.WriteRoleFaultValue(f.ctx, key, state))
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	require.NoError(t, f.keeper.RoleFaultPruneIndex.Set(
		f.ctx, types.NewRoleFaultPruneKey(recordedHeight+params.Service.RecordRetentionBlocks, key),
	))
	// Both derived directions, exactly like the production writer: the by-task
	// index is what a §6.6 fault vector read walks.
	require.NoError(t, f.keeper.RoleFaultByTaskIndex.Set(
		f.ctx, types.NewRoleFaultByTaskKey(state.TaskId, key),
	))
	return state
}

func registerTestModelProfile(t *testing.T, f *fixture, modelID string, profileVersion uint32, minStake, height uint64) {
	t.Helper()
	proposer := hubAddress(t, 250)
	if model, err := f.keeper.GetModel(f.ctx, modelID); err == nil {
		proposer = model.ProposerAddress
	}
	projection := testModelProfileProjection(modelID, profileVersion, minStake)
	digest, _, err := types.ModelRegistrationDigest(sdk.UnwrapSDKContext(f.ctx).ChainID(), proposer, projection)
	require.NoError(t, err)
	_, _, _, replay, err := f.keeper.RegisterModelProfileState(
		f.ctx, proposer, projection, digest, minStake, types.ModelRegistrationFeeMinMicroUSDC, height,
	)
	require.NoError(t, err)
	require.False(t, replay)
}

func testModelProfileProjection(modelID string, profileVersion uint32, minStake uint64) shared.ModelProfileProjection {
	requiredHash := bytes.Repeat([]byte{byte(profileVersion + 1)}, 32)
	projection := shared.ModelProfileProjection{
		ModelId: modelID, ProfileVersion: profileVersion,
		ManifestHash: requiredHash, TokenizerHash: requiredHash,
		RuntimeClass: "CAUSAL_LM_PREFILL_LOGPROBS_V1", RequiredTopK: 8,
		TaskTypes:                 []shared.TaskType{shared.TaskType_TASK_TYPE_TEXT_GENERATION},
		GenerationType:            shared.GenerationType_GENERATION_TYPE_SAMPLED,
		ResourceTier:              1,
		MinStake:                  sdk.NewCoin(types.DefaultBusinessDenom, sdkmath.NewIntFromUint64(minStake)),
		ChallengeOpenWindowBlocks: types.ProfileChallengeOpenWindowMinBlocks,
		VerificationProfile: shared.VerificationProfile{
			VerificationProfileId:       1,
			VerificationMode:            shared.VerificationMode_VERIFICATION_MODE_SINGLE_SAMPLE,
			TokenScope:                  shared.TokenScope_TOKEN_SCOPE_ALL_GENERATED_OUTPUT_TOKENS,
			JudgmentFunctionVersion:     "PREFILL_GENERATED_TOKEN_METRICS_V1",
			CanonicalEncodingVersion:    "CANONICAL_OUTPUT_TEXT_V1",
			MetricAggregateProofVersion: "PREFILL_METRIC_AGGREGATE_PROOF_V1",
			EvidenceSchema:              shared.NewWorkerValueEvidenceSchemaV1(1 << 30),
			Metrics: shared.MetricSpec{
				ComparedTopK: 8,
				NumericScale: shared.NumericScale_NUMERIC_SCALE_FP_1E6,
			},
		},
		PricingProfile: shared.PricingProfile{
			InitialOutputPrice: 1_000, VerifyRatioBps: 1_000, MinOrderValue: 1_000,
		},
		SchemaHash: requiredHash, PreviousProfileVersion: profileVersion - 1,
		RegistrationFee: sdk.NewCoin(types.DefaultBusinessDenom, sdkmath.NewIntFromUint64(types.ModelRegistrationFeeMinMicroUSDC)),
	}
	evidenceSchemaHash, err := types.EvidenceSchemaHash(projection)
	if err != nil {
		panic(err)
	}
	projection.VerificationProfile.EvidenceSchemaHash = evidenceSchemaHash
	return projection
}

func mustTestProfileState(modelID, proposer string, profileVersion uint32, minStake, height uint64) types.ProfileState {
	projection := testModelProfileProjection(modelID, profileVersion, minStake)
	// The fixture's chain_id must be the one the keeper validates under, or the
	// row's registration_digest can never match its own canonical preimage.
	digest, _, err := types.ModelRegistrationDigest(testHubChainID, proposer, projection)
	if err != nil {
		panic(err)
	}
	return types.ProfileState{
		ModelId: projection.ModelId, ProfileVersion: projection.ProfileVersion,
		ManifestHash: projection.ManifestHash, TokenizerHash: projection.TokenizerHash,
		RuntimeClass: projection.RuntimeClass, RequiredTopK: projection.RequiredTopK,
		TaskTypes: projection.TaskTypes, GenerationType: projection.GenerationType,
		ResourceTier: projection.ResourceTier, MinStake: minStake,
		ChallengeOpenWindowBlocks: projection.ChallengeOpenWindowBlocks,
		VerificationProfile:       projection.VerificationProfile,
		VerificationThresholds:    projection.VerificationThresholds,
		BatchVerification:         projection.BatchVerification,
		PricingProfile:            projection.PricingProfile,
		RefPrice:                  projection.PricingProfile.InitialOutputPrice,
		TimeoutBootstrapProfile:   projection.TimeoutBootstrapProfile,
		SchemaHash:                projection.SchemaHash,
		Status:                    types.ModelStatusActive,
		StatusSource:              types.ProfileStatusSourceAutoSupport,
		RegistrationFeePaid:       types.ModelRegistrationFeeMinMicroUSDC,
		PreviousProfileVersion:    projection.PreviousProfileVersion,
		ProposerAddress:           proposer, RegistrationDigest: digest,
		CreatedHeight: height, UpdatedHeight: height,
	}
}

func encodeCustodyKey[K any](t *testing.T, codec collcodec.KeyCodec[K], key K) []byte {
	t.Helper()
	encoded := make([]byte, codec.Size(key))
	written, err := codec.Encode(encoded, key)
	require.NoError(t, err)
	require.Equal(t, len(encoded), written)
	return encoded
}

func signCompare(value int) int {
	if value < 0 {
		return -1
	}
	if value > 0 {
		return 1
	}
	return 0
}

func beaconTestBlockHash(label string) []byte {
	digest := sha256.Sum256([]byte(label))
	return digest[:]
}

func beaconWriteContext(ctx context.Context, height uint64, blockHash []byte) context.Context {
	return sdk.UnwrapSDKContext(ctx).
		WithBlockHeight(int64(height)).
		WithHeaderHash(blockHash)
}

func hubEventsOfType(t *testing.T, ctx sdk.Context, target proto.Message) []proto.Message {
	t.Helper()
	typeName := proto.MessageName(target)
	messages := make([]proto.Message, 0)
	for _, event := range ctx.EventManager().ABCIEvents() {
		if event.Type != typeName {
			continue
		}
		parsed, err := sdk.ParseTypedEvent(event)
		require.NoError(t, err)
		messages = append(messages, parsed)
	}
	return messages
}
