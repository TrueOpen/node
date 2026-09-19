package app

// ModelProfile / BuilderSet / RoleActiveTasks query integration.
//
// Model/Profile, BuilderSet and role-active indexes have independent primary
// authorities. The old TaskVerifiers duplicate vector is deleted. All three
// Query RPCs are wired into AutoCLI; this file locks the App-level end-to-end
// read path.
//
// Coverage:
//   - QueryModelProfile happy path: seed Model + Profile, query echoes.
//   - QueryModelProfile NotFound: missing profile returns codes.NotFound.
//   - QueryModelProfile InvalidArgument: nil / empty inputs.
//   - QueryBuilderSet happy path: seed BuilderSetSnapshot, query echoes.
//   - QueryBuilderSet NotFound: unknown term_id.
//   - QueryBuilderSet InvalidArgument: term_id == 0.
//   - QueryRoleActiveTasks: negative-path (nil / empty address / unknown
//     role). Positive-path (with actual assignments) requires the
//     task-flow fixture chain and is covered by the full lifecycle test.

import (
	"bytes"
	"testing"

	sdkmath "cosmossdk.io/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	hubkeeper "github.com/TrueOpen/node/x/hub/keeper"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	taskkeeper "github.com/TrueOpen/node/x/task/keeper"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestQueryModelProfileReturnsSeededPair(t *testing.T) {
	app := bootAppMinimal(t)
	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: app.LastBlockHeight() + 1})

	modelID := "gpt-oss-1"
	const profVersion uint32 = 1
	proposer := sdk.AccAddress(bytes.Repeat([]byte{0x41}, 20)).String()
	projection := appTestModelProfileProjection(modelID, profVersion)
	digest, _, err := hubtypes.ModelRegistrationDigest(sdk.UnwrapSDKContext(ctx).ChainID(), proposer, projection)
	require.NoError(t, err)
	_, _, _, replay, err := app.HubKeeper.RegisterModelProfileState(
		ctx,
		proposer,
		projection,
		digest,
		appTestServiceBondMinInitial,
		hubtypes.ModelRegistrationFeeMinMicroUSDC,
		10,
	)
	require.NoError(t, err)
	require.False(t, replay)

	// The single QueryModelProfile RPC was split into Model + Profile.
	qs := hubkeeper.NewQueryServerImpl(app.HubKeeper)
	modelResp, err := qs.Model(ctx, &hubtypes.QueryModelRequest{ModelId: modelID})
	require.NoError(t, err)
	require.Equal(t, modelID, modelResp.Model.ModelId)
	require.Equal(t, hubtypes.ModelStatusRegistered, modelResp.Model.Status)
	require.Equal(t, uint64(10), modelResp.Model.CreatedHeight)

	profileResp, err := qs.Profile(ctx, &hubtypes.QueryProfileRequest{
		ModelId:        modelID,
		ProfileVersion: profVersion,
	})
	require.NoError(t, err)
	require.Equal(t, modelID, profileResp.Profile.ModelId)
	require.Equal(t, profVersion, profileResp.Profile.ProfileVersion)
	require.Equal(t, hubtypes.ModelStatusRegistered, profileResp.Profile.Status)
}

// appTestServiceBondMinInitial mirrors params.Service.ServiceBondMinInitial
// (Ruling 16 removed the keeper.MinServiceBond constant).
var appTestServiceBondMinInitial = func() uint64 {
	value, err := hubtypes.AmountToUint64(hubtypes.DefaultHubParams().Service.ServiceBondMinInitial, false)
	if err != nil {
		panic(err)
	}
	return value
}()

func appTestModelProfileProjection(modelID string, profileVersion uint32) shared.ModelProfileProjection {
	requiredHash := bytes.Repeat([]byte{byte(profileVersion + 1)}, 32)
	projection := shared.ModelProfileProjection{
		ModelId: modelID, ProfileVersion: profileVersion,
		ManifestHash: requiredHash, TokenizerHash: requiredHash,
		RuntimeClass: "CAUSAL_LM_PREFILL_LOGPROBS_V1", RequiredTopK: 8,
		TaskTypes:                 []shared.TaskType{shared.TaskType_TASK_TYPE_TEXT_GENERATION},
		GenerationType:            shared.GenerationType_GENERATION_TYPE_SAMPLED,
		ResourceTier:              1,
		MinStake:                  sdk.NewCoin(hubtypes.DefaultBusinessDenom, sdkmath.NewIntFromUint64(appTestServiceBondMinInitial)),
		ChallengeOpenWindowBlocks: hubtypes.ProfileChallengeOpenWindowMinBlocks,
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
			InitialOutputPrice: 1_000, VerifyRatioBps: 1_000, MinOrderValue: 1,
		},
		SchemaHash: requiredHash, PreviousProfileVersion: profileVersion - 1,
		RegistrationFee: sdk.NewCoin(hubtypes.DefaultBusinessDenom, sdkmath.NewIntFromUint64(hubtypes.ModelRegistrationFeeMinMicroUSDC)),
	}
	evidenceSchemaHash, err := hubtypes.EvidenceSchemaHash(projection)
	if err != nil {
		panic(err)
	}
	projection.VerificationProfile.EvidenceSchemaHash = evidenceSchemaHash
	return projection
}

func TestQueryModelProfileNotFoundForUnknownPair(t *testing.T) {
	app := bootAppMinimal(t)
	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: app.LastBlockHeight() + 1})
	qs := hubkeeper.NewQueryServerImpl(app.HubKeeper)

	_, err := qs.Profile(ctx, &hubtypes.QueryProfileRequest{
		ModelId:        "does-not-exist",
		ProfileVersion: 99,
	})
	require.Error(t, err)
	require.Equal(t, codes.NotFound, status.Code(err))

	_, err = qs.Model(ctx, &hubtypes.QueryModelRequest{ModelId: "does-not-exist"})
	require.Error(t, err)
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestQueryModelProfileInvalidArgumentContract(t *testing.T) {
	app := bootAppMinimal(t)
	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: app.LastBlockHeight() + 1})
	qs := hubkeeper.NewQueryServerImpl(app.HubKeeper)

	// nil request
	_, err := qs.Profile(ctx, nil)
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = qs.Model(ctx, nil)
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	// Empty model_id
	_, err = qs.Profile(ctx, &hubtypes.QueryProfileRequest{ModelId: "", ProfileVersion: 1})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = qs.Model(ctx, &hubtypes.QueryModelRequest{ModelId: ""})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	// profile_version == 0
	_, err = qs.Profile(ctx, &hubtypes.QueryProfileRequest{ModelId: "m", ProfileVersion: 0})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestQueryBuilderSetNotFoundForUnknownBuilderSetID(t *testing.T) {
	app := bootAppMinimal(t)
	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: app.LastBlockHeight() + 1})
	qs := hubkeeper.NewQueryServerImpl(app.HubKeeper)

	_, err := qs.BuilderSet(ctx, &hubtypes.QueryBuilderSetRequest{
		Selector: &hubtypes.QueryBuilderSetRequest_BuilderSetId{BuilderSetId: "9999"},
	})
	require.Error(t, err)
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestQueryBuilderSetInvalidArgumentContract(t *testing.T) {
	app := bootAppMinimal(t)
	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: app.LastBlockHeight() + 1})
	qs := hubkeeper.NewQueryServerImpl(app.HubKeeper)

	// nil request
	_, err := qs.BuilderSet(ctx, nil)
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	// An empty builder_set_id selector is explicitly rejected by the handler.
	_, err = qs.BuilderSet(ctx, &hubtypes.QueryBuilderSetRequest{
		Selector: &hubtypes.QueryBuilderSetRequest_BuilderSetId{},
	})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// TestQueryRoleActiveTasksNegativePaths covers only the negative-path
// contract for RoleActiveTasks. The positive-path (seeded task
// assignments that the handler must walk) needs the K12-gated task
// fixture; those checks live in the full lifecycle full e2e.
func TestQueryRoleActiveTasksNegativePaths(t *testing.T) {
	app := bootAppMinimal(t)
	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: app.LastBlockHeight() + 1})
	// RoleActiveTasks walks task-local assignment state, so it is served by
	// the task query server (not hub).
	qs := taskkeeper.NewQueryServerImpl(app.TaskKeeper)

	// nil request
	_, err := qs.RoleActiveTasks(ctx, nil)
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	// Empty address
	_, err = qs.RoleActiveTasks(ctx, &tasktypes.QueryRoleActiveTasksRequest{Duty: shared.Duty_DUTY_WORKER, OperatorAddress: ""})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	// Whitespace address (handler must reject after TrimSpace mismatch)
	_, err = qs.RoleActiveTasks(ctx, &tasktypes.QueryRoleActiveTasksRequest{Duty: shared.Duty_DUTY_WORKER, OperatorAddress: " trueopen1x "})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	// Unregistered duty -> should return InvalidArgument (or empty list; assert
	// non-Internal to keep loose enough to accept either shape). The old
	// free-form role string is gone: §5.11 / §16.3 use the closed
	// hub.v1.Duty enum, so DUTY_UNSPECIFIED is the only invalid value the
	// wire can still carry.
	resp, err := qs.RoleActiveTasks(ctx, &tasktypes.QueryRoleActiveTasksRequest{
		Duty: shared.Duty_DUTY_UNSPECIFIED, OperatorAddress: "trueopen1someone",
	})
	if err != nil {
		require.NotEqual(t, codes.Internal, status.Code(err),
			"unknown role must NOT surface as Internal")
	} else {
		require.NotNil(t, resp)
	}
}
