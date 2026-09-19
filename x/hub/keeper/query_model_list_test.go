package keeper_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	gateway "github.com/cosmos/gogogateway"
	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// The frozen registry surface exposes the direct Model, Profile and
// ProfileCapability lookups plus the Models discovery walk. The removed
// list/alias routes remain absent.

func TestAllHubModelIDQueriesRejectUnsafeIDs(t *testing.T) {
	f := initFixture(t)
	queryServer := keeper.NewQueryServerImpl(f.keeper)

	for _, modelID := range []string{"model/id", "model.id", "model?id", "model#id", "model%2Fid"} {
		_, err := queryServer.Model(f.ctx, &types.QueryModelRequest{ModelId: modelID})
		require.Equal(t, codes.InvalidArgument, status.Code(err), modelID)
		_, err = queryServer.Profile(f.ctx, &types.QueryProfileRequest{ModelId: modelID, ProfileVersion: 1})
		require.Equal(t, codes.InvalidArgument, status.Code(err), modelID)
		// ModelProfile (the deleted §16.3 alias) and ModelCapability (renamed to
		// the profile-scoped ProfileCapability) are gone; ProfileCapability is
		// the surviving node-level capability query and must reject the same IDs.
		_, err = queryServer.ProfileCapability(f.ctx, &types.QueryProfileCapabilityRequest{OperatorAddress: hubAddress(t, 240), ModelId: modelID, ProfileVersion: 1})
		require.Equal(t, codes.InvalidArgument, status.Code(err), modelID)
		_, err = queryServer.ModelSupport(f.ctx, &types.QueryModelSupportRequest{OperatorAddress: hubAddress(t, 240), ModelId: modelID, ProfileVersion: 1})
		require.Equal(t, codes.InvalidArgument, status.Code(err), modelID)
		_, err = queryServer.FreezeSignals(f.ctx, &types.QueryFreezeSignalsRequest{
			ModelId: modelID, ProfileVersion: 1, Status: types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_OPEN,
		})
		require.Equal(t, codes.InvalidArgument, status.Code(err), modelID)
	}
}

func TestQueryProfileReturnsTypedEvidenceSchema(t *testing.T) {
	f := initFixture(t)
	params := types.DefaultHubParams()
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	profile := mustTestProfileState("model-evidence", hubAddress(t, 241), 1, testServiceBondMinInitial, 1)
	require.NoError(t, f.keeper.Profile.Set(f.ctx, types.NewProfileStateKey(profile.ModelId, profile.ProfileVersion), profile))
	queryServer := keeper.NewQueryServerImpl(f.keeper)

	response, err := queryServer.Profile(f.ctx, &types.QueryProfileRequest{
		ModelId: profile.ModelId, ProfileVersion: profile.ProfileVersion,
	})
	require.NoError(t, err)
	require.Equal(t, profile.VerificationProfile.EvidenceSchemaHash, response.Profile.VerificationProfile.EvidenceSchemaHash)
	require.Equal(t, shared.EvidenceSchemaVersionV1, response.Profile.VerificationProfile.EvidenceSchema.SchemaVersion)
	require.Equal(t, []shared.InferEvidenceRequirementV1{{
		EvidenceKind:            shared.EvidenceKind_EVIDENCE_KIND_WORKER_VALUE_OPENING,
		CommitmentSchemaVersion: shared.WorkerValueCommitmentSchemaVersionV2,
		MaxEncodedSizeBytes:     1 << 30,
	}}, response.Profile.VerificationProfile.EvidenceSchema.RequiredInferEvidence)

	originalEvidenceHash := append([]byte(nil), profile.VerificationProfile.EvidenceSchemaHash...)
	profile.VerificationProfile.EvidenceSchemaHash[0]++
	require.NoError(t, f.keeper.Profile.Set(f.ctx, types.NewProfileStateKey(profile.ModelId, profile.ProfileVersion), profile))
	_, err = queryServer.Profile(f.ctx, &types.QueryProfileRequest{
		ModelId: profile.ModelId, ProfileVersion: profile.ProfileVersion,
	})
	require.Equal(t, codes.Internal, status.Code(err))

	profile.VerificationProfile.EvidenceSchemaHash = originalEvidenceHash
	params.Price.PriceHardMax = profile.PricingProfile.InitialOutputPrice - 1
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	require.NoError(t, f.keeper.Profile.Set(f.ctx, types.NewProfileStateKey(profile.ModelId, profile.ProfileVersion), profile))
	_, err = queryServer.Profile(f.ctx, &types.QueryProfileRequest{
		ModelId: profile.ModelId, ProfileVersion: profile.ProfileVersion,
	})
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestQueryModelListsGatewayContract(t *testing.T) {
	f, queryServer := newModelListQueryFixture(t)
	mux := runtime.NewServeMux(runtime.WithMarshalerOption(runtime.MIMEWildcard, &gateway.JSONPb{
		OrigName:     true,
		EmitDefaults: true,
	}))
	require.NoError(t, types.RegisterQueryHandlerServer(context.Background(), mux, queryServer))

	// The seeded models/profiles are still in the store, so a 404 here proves the
	// route is unregistered rather than merely empty. Hard rule: a public surface
	// that is not closed out, or that has been deleted, is not registered.
	for path, expectedStatus := range map[string]int{
		"/TrueOpen/hub/v1/profiles?model_id=model-a":  http.StatusNotFound,
		"/TrueOpen/hub/v1/model/model-a/profile/1":    http.StatusNotFound,
		"/TrueOpen/hub/v1/model_capability/model-a/1": http.StatusNotFound,
		"/TrueOpen/hub/v1/model/model%2Fbad":          http.StatusNotFound,
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(f.ctx)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		require.Equal(t, expectedStatus, response.Code, path)
	}

	// The single-row registry routes and the Models discovery walk must answer.
	// `?status=ACTIVE` is an unknown query parameter rather than a route: the
	// gateway ignores it, so it must not read as a supported status filter.
	for path, expectedStatus := range map[string]int{
		"/TrueOpen/hub/v1/models":               http.StatusOK,
		"/TrueOpen/hub/v1/models?status=ACTIVE": http.StatusOK,
		"/TrueOpen/hub/v1/model/model-a":        http.StatusOK,
		"/TrueOpen/hub/v1/profile/model-a/1":    http.StatusOK,
		"/TrueOpen/hub/v1/model/missing-model":  http.StatusNotFound,
		"/TrueOpen/hub/v1/profile/model-a/99":   http.StatusNotFound,
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(f.ctx)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		require.Equal(t, expectedStatus, response.Code, path)
	}
}

func TestModelAndProfileStatusTransitionsWritePrimaryState(t *testing.T) {
	f := initFixture(t)
	// SetProfileStatus used to re-run the registration min_stake clamp against
	// params.Service.ServiceBondMinInitial (Ruling 16) and so needed the params row.
	// It no longer does — that clamp is registration admission only, because
	// min_stake is fixed at registration while the floor is a live parameter — but
	// the fixture stays genesis-initialized so this test exercises a realistic
	// module store rather than a bare one.
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	proposer := hubAddress(t, 240)
	model := modelListState("model-transition", types.ModelStatusRegistered, proposer, 1)
	profile := mustTestProfileState(model.ModelId, proposer, 1, testServiceBondMinInitial, 1)
	profile.Status = types.ModelStatusRegistered
	putModelListState(t, f, model)
	putProfileListState(t, f, profile)

	_, err := f.keeper.SetModelStatus(f.ctx, model.ModelId, types.ModelStatusFrozen, types.GovernanceReason_GOVERNANCE_REASON_FREEZE, 2)
	require.NoError(t, err)
	storedModel, err := f.keeper.Model.Get(f.ctx, model.ModelId)
	require.NoError(t, err)
	require.Equal(t, types.ModelStatusFrozen, storedModel.Status)
	require.NoError(t, storedModel.Validate())

	_, err = f.keeper.SetProfileStatus(f.ctx, model.ModelId, profile.ProfileVersion, types.ModelStatusFrozen, 3)
	require.NoError(t, err)
	storedProfile, err := f.keeper.Profile.Get(f.ctx, types.NewProfileStateKey(model.ModelId, profile.ProfileVersion))
	require.NoError(t, err)
	require.Equal(t, types.ModelStatusFrozen, storedProfile.Status)
	require.NoError(t, storedProfile.Validate())
}

func TestGovernanceStatusMessagesEnforceSourceAndReason(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(10))
	proposer := hubAddress(t, 242)
	model := modelListState("model-governance", types.ModelStatusRegistered, proposer, 1)
	profile := mustTestProfileState(model.ModelId, proposer, 1, testServiceBondMinInitial, 1)
	profile.Status = types.ModelStatusRegistered
	profile.StatusSource = types.ProfileStatusSourceAutoSupport
	putModelListState(t, f, model)
	putProfileListState(t, f, profile)

	authority := authtypes.NewModuleAddress(types.GovModuleName).String()
	server := keeper.NewMsgServerImpl(f.keeper)
	eventCount := len(sdk.UnwrapSDKContext(f.ctx).EventManager().Events())

	_, err := server.SetModelStatus(f.ctx, &types.MsgSetModelStatus{
		ModelId: model.ModelId, NewStatus: types.ModelStatusEmergencyFrozen,
		ReasonCode: types.GovernanceReason_GOVERNANCE_REASON_FREEZE, Authority: authority,
	})
	require.ErrorContains(t, err, "reserved for the validator emergency quorum")
	storedModel, err := f.keeper.GetModel(f.ctx, model.ModelId)
	require.NoError(t, err)
	require.Equal(t, types.ModelStatusRegistered, storedModel.Status)
	require.Len(t, sdk.UnwrapSDKContext(f.ctx).EventManager().Events(), eventCount)

	_, err = server.SetModelStatus(f.ctx, &types.MsgSetModelStatus{
		ModelId: model.ModelId, NewStatus: types.ModelStatusFrozen,
		ReasonCode: types.GovernanceReason_GOVERNANCE_REASON_DELIST, Authority: authority,
	})
	require.ErrorContains(t, err, "does not match reason")

	modelResponse, err := server.SetModelStatus(f.ctx, &types.MsgSetModelStatus{
		ModelId: model.ModelId, NewStatus: types.ModelStatusFrozen,
		ReasonCode: types.GovernanceReason_GOVERNANCE_REASON_FREEZE, Authority: authority,
	})
	require.NoError(t, err)
	require.Equal(t, types.ModelStatusFrozen, modelResponse.NewStatus)
	storedModel, err = f.keeper.GetModel(f.ctx, model.ModelId)
	require.NoError(t, err)
	require.Equal(t, types.ModelStatusSourceGovernance, storedModel.StatusSource)

	_, err = server.SetProfileStatus(f.ctx, &types.MsgSetProfileStatus{
		ModelId: model.ModelId, ProfileVersion: profile.ProfileVersion,
		NewStatus:  types.ModelStatusEmergencyFrozen,
		ReasonCode: types.GovernanceReason_GOVERNANCE_REASON_FREEZE, Authority: authority,
	})
	require.ErrorContains(t, err, "reserved for the validator emergency quorum")

	profileResponse, err := server.SetProfileStatus(f.ctx, &types.MsgSetProfileStatus{
		ModelId: model.ModelId, ProfileVersion: profile.ProfileVersion,
		NewStatus:  types.ModelStatusFrozen,
		ReasonCode: types.GovernanceReason_GOVERNANCE_REASON_FREEZE, Authority: authority,
	})
	require.NoError(t, err)
	require.Equal(t, types.ModelStatusFrozen, profileResponse.NewStatus)

	_, err = server.SetProfileStatus(f.ctx, &types.MsgSetProfileStatus{
		ModelId: model.ModelId, ProfileVersion: profile.ProfileVersion,
		NewStatus:  types.ModelStatusRegistered,
		ReasonCode: types.GovernanceReason_GOVERNANCE_REASON_EMERGENCY_RECOVERY, Authority: authority,
	})
	require.ErrorContains(t, err, "does not match reason")

	profileResponse, err = server.SetProfileStatus(f.ctx, &types.MsgSetProfileStatus{
		ModelId: model.ModelId, ProfileVersion: profile.ProfileVersion,
		NewStatus:  types.ModelStatusRegistered,
		ReasonCode: types.GovernanceReason_GOVERNANCE_REASON_UNFREEZE, Authority: authority,
	})
	require.NoError(t, err)
	require.Equal(t, types.ModelStatusRegistered, profileResponse.NewStatus)
	storedProfile, err := f.keeper.GetProfile(f.ctx, model.ModelId, profile.ProfileVersion)
	require.NoError(t, err)
	require.Equal(t, types.ProfileStatusSourceAutoSupport, storedProfile.StatusSource)

	storedProfile.Status = types.ModelStatusEmergencyFrozen
	storedProfile.StatusSource = types.ProfileStatusSourceEmergency
	require.NoError(t, storedProfile.Validate())
	require.NoError(t, f.keeper.Profile.Set(
		f.ctx, types.NewProfileStateKey(storedProfile.ModelId, storedProfile.ProfileVersion), storedProfile,
	))
	_, err = server.SetProfileStatus(f.ctx, &types.MsgSetProfileStatus{
		ModelId: model.ModelId, ProfileVersion: profile.ProfileVersion,
		NewStatus:  types.ModelStatusRegistered,
		ReasonCode: types.GovernanceReason_GOVERNANCE_REASON_UNFREEZE, Authority: authority,
	})
	require.ErrorContains(t, err, "does not match reason")

	profileResponse, err = server.SetProfileStatus(f.ctx, &types.MsgSetProfileStatus{
		ModelId: model.ModelId, ProfileVersion: profile.ProfileVersion,
		NewStatus:  types.ModelStatusRegistered,
		ReasonCode: types.GovernanceReason_GOVERNANCE_REASON_EMERGENCY_RECOVERY, Authority: authority,
	})
	require.NoError(t, err)
	require.Equal(t, types.ModelStatusRegistered, profileResponse.NewStatus)
}

func newModelListQueryFixture(t *testing.T) (*fixture, types.QueryServer) {
	t.Helper()
	f := initFixture(t)
	// Models reads the query page caps out of Params, so a bare module store
	// would answer Internal before it ever reached the registry walk.
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	proposer := hubAddress(t, 240)

	putModelListState(t, f, modelListState("model-a", types.ModelStatusRegistered, proposer, 10))
	putModelListState(t, f, modelListState("model-b", types.ModelStatusActive, proposer, 1))
	putModelListState(t, f, modelListState("model-c", types.ModelStatusFrozen, proposer, 0))

	profiles := []struct {
		modelID        string
		profileVersion uint32
		status         types.ModelProfileStatus
	}{
		{modelID: "model-a", profileVersion: 1, status: types.ModelStatusActive},
		{modelID: "model-a", profileVersion: 2, status: types.ModelStatusRegistered},
		{modelID: "model-a", profileVersion: 10, status: types.ModelStatusFrozen},
		{modelID: "model-b", profileVersion: 1, status: types.ModelStatusActive},
	}
	for _, seed := range profiles {
		profile := mustTestProfileState(seed.modelID, proposer, seed.profileVersion, testServiceBondMinInitial, 1)
		profile.Status = seed.status
		profile.StatusSource = profileStatusSourceForTest(seed.status)
		putProfileListState(t, f, profile)
	}

	return f, keeper.NewQueryServerImpl(f.keeper)
}

func modelListState(modelID string, state types.ModelProfileStatus, proposer string, latestProfileVersion uint32) types.ModelState {
	return types.ModelState{
		ModelId:              modelID,
		LatestProfileVersion: latestProfileVersion,
		Status:               state,
		StatusSource:         modelStatusSourceForTest(state),
		ProposerAddress:      proposer,
		RegistrationFeePaid:  types.ModelRegistrationFeeMinMicroUSDC,
		CreatedHeight:        1,
		UpdatedHeight:        1,
	}
}

func modelStatusSourceForTest(status types.ModelProfileStatus) types.ModelStatusSource {
	switch status {
	case types.ModelStatusRegistered, types.ModelStatusActive:
		return types.ModelStatusSourceAutoProfile
	case types.ModelStatusEmergencyFrozen:
		return types.ModelStatusSourceEmergency
	default:
		return types.ModelStatusSourceGovernance
	}
}

func profileStatusSourceForTest(status types.ModelProfileStatus) types.ProfileStatusSource {
	switch status {
	case types.ModelStatusRegistered, types.ModelStatusActive:
		return types.ProfileStatusSourceAutoSupport
	case types.ModelStatusEmergencyFrozen:
		return types.ProfileStatusSourceEmergency
	default:
		return types.ProfileStatusSourceGovernance
	}
}

func putModelListState(t *testing.T, f *fixture, state types.ModelState) {
	t.Helper()
	require.NoError(t, state.Validate())
	require.NoError(t, f.keeper.Model.Set(f.ctx, state.ModelId, state))
}

func putProfileListState(t *testing.T, f *fixture, state types.ProfileState) {
	t.Helper()
	require.NoError(t, state.Validate())
	require.NoError(t, f.keeper.Profile.Set(f.ctx, types.NewProfileStateKey(state.ModelId, state.ProfileVersion), state))
}
