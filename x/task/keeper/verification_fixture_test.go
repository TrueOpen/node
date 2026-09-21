package keeper

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

type verificationHubStub struct {
	internalStubHubKeeper
	nonce                        uint64
	signatureErr                 error
	profile                      hubtypes.ProfileStateSnapshot
	pool                         hubtypes.CandidatePoolSnapshotState
	poolBitmap                   []byte
	poolRetained                 bool
	responsibilities             []hubtypes.ServiceKeyResponsibilityState
	responsibilityFailureAttempt int
	builderProofOperator         string
	builderProofKey              []byte
}

func (s *verificationHubStub) GetBuilderEvidenceProofKey(_ context.Context, operatorAddress string, authorizationNonce uint64) (hubtypes.CurrentServiceKeySnapshot, error) {
	if s.builderProofOperator == "" {
		return s.internalStubHubKeeper.GetBuilderEvidenceProofKey(context.Background(), operatorAddress, authorizationNonce)
	}
	if operatorAddress != s.builderProofOperator || authorizationNonce != 1 {
		return hubtypes.CurrentServiceKeySnapshot{}, errors.New("Builder evidence proof key mismatch")
	}
	return hubtypes.CurrentServiceKeySnapshot{
		ParticipantType:    shared.ParticipantTypeBuilder,
		OperatorAddress:    operatorAddress,
		ServicePubkey:      hex.EncodeToString(s.builderProofKey),
		AuthorizationNonce: authorizationNonce,
		Status:             hubtypes.ServiceKeyStatusActive,
	}, nil
}

func (s *verificationHubStub) GetCortexNode(_ sdk.Context, address sdk.AccAddress) (hubtypes.CortexNodeSnapshot, bool) {
	return hubtypes.CortexNodeSnapshot{
		OperatorAddress: address.String(), ServiceAuthorizationNonce: s.nonce,
		ServiceKeyStatus: hubtypes.ServiceKeyStatusActive,
	}, true
}

func (s *verificationHubStub) VerifyCurrentCortexServiceDigest(context.Context, string, []byte, []byte, uint64) error {
	return s.signatureErr
}

func (s *verificationHubStub) GetProfileState(sdk.Context, string, uint32) (hubtypes.ProfileStateSnapshot, bool) {
	return s.profile, true
}

func (s *verificationHubStub) GetCandidatePoolSnapshot(_ sdk.Context, snapshotID []byte) (hubtypes.CandidatePoolSnapshotState, bool) {
	if !bytes.Equal(snapshotID, s.pool.SnapshotId) {
		return hubtypes.CandidatePoolSnapshotState{}, false
	}
	return s.pool, true
}

func (s *verificationHubStub) HasCandidatePoolTaskRef(context.Context, []byte, []byte) (bool, error) {
	return s.poolRetained, nil
}

func (s *verificationHubStub) GetCandidatePoolLayout(context.Context, []byte) (uint32, uint32, uint32, bool) {
	return s.pool.SlotCapacity, uint32(len(s.poolBitmap)), 1, true
}

func (s *verificationHubStub) CandidatePoolSegmentBitmap(context.Context, []byte, uint32) ([]byte, error) {
	return append([]byte(nil), s.poolBitmap...), nil
}

func (s *verificationHubStub) ReserveServiceKeyResponsibility(_ context.Context, responsibility hubtypes.ServiceKeyResponsibilityState) error {
	if s.responsibilityFailureAttempt != 0 && len(s.responsibilities)+1 == s.responsibilityFailureAttempt {
		return errors.New("injected Builder responsibility reserve failure")
	}
	s.responsibilities = append(s.responsibilities, responsibility)
	return nil
}

type verificationFixture struct {
	*internalFixture
	hub         *verificationHubStub
	ctx         context.Context
	server      msgServer
	taskID      []byte
	taskKey     types.TaskKey
	operators   []string
	assignment  types.VerifierAssignmentState
	genDigest   []byte
	profileHash []byte
}

func newVerificationFixture(t *testing.T) *verificationFixture {
	t.Helper()
	base := initInternalFixture(t)
	sdkCtx := sdk.UnwrapSDKContext(base.ctx).WithBlockHeight(10).WithChainID("trueopen-test-1")
	ctx := sdk.WrapSDKContext(sdkCtx)
	poolID := bytes.Repeat([]byte{0x52}, types.Hash32Len)
	poolHash := bytes.Repeat([]byte{0x53}, types.Hash32Len)
	hub := &verificationHubStub{
		nonce: 7, poolRetained: true,
		pool: hubtypes.CandidatePoolSnapshotState{
			SchemaVersion: 1, SnapshotId: poolID, PoolHash: poolHash,
			Status:       hubtypes.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_ACTIVE,
			SlotCapacity: 8,
		},
		poolBitmap: []byte{0},
	}
	base.keeper.hubKeeper = hub
	operators := []string{
		sdk.AccAddress(bytes.Repeat([]byte{0x11}, 20)).String(),
		sdk.AccAddress(bytes.Repeat([]byte{0x22}, 20)).String(),
		sdk.AccAddress(bytes.Repeat([]byte{0x33}, 20)).String(),
	}
	taskID := bytes.Repeat([]byte{0x41}, types.Hash32Len)
	taskKey := types.NewTaskKey(taskID)
	genDigest := bytes.Repeat([]byte{0x51}, types.Hash32Len)
	verificationProfile := shared.VerificationProfile{
		VerificationProfileId: 1, JudgmentFunctionVersion: "PREFILL_GENERATED_TOKEN_METRICS_V1",
		VerificationMode:      shared.VerificationMode_VERIFICATION_MODE_SINGLE_SAMPLE,
		TokenScope:            shared.TokenScope_TOKEN_SCOPE_ALL_GENERATED_OUTPUT_TOKENS,
		RequireOutputTokenIds: true, RequireFinishReason: true,
		Metrics:                     shared.MetricSpec{ComparedTopK: 8, NumericScale: shared.NumericScale_NUMERIC_SCALE_FP_1E6},
		CanonicalEncodingVersion:    "CANONICAL_OUTPUT_TEXT_V1",
		MetricAggregateProofVersion: "PREFILL_METRIC_AGGREGATE_PROOF_V1",
		EvidenceSchema:              shared.NewWorkerValueEvidenceSchemaV1(1 << 30),
	}
	profileProjection := shared.ModelProfileProjection{
		ModelId: "model-v1", ProfileVersion: 1, TokenizerHash: bytes.Repeat([]byte{0x32}, types.Hash32Len),
		RequiredTopK: 8, GenerationType: shared.GenerationType_GENERATION_TYPE_SAMPLED,
		VerificationProfile: verificationProfile, SchemaHash: bytes.Repeat([]byte{0x33}, types.Hash32Len),
	}
	evidenceSchemaHash, err := hubtypes.EvidenceSchemaHash(profileProjection)
	require.NoError(t, err)
	verificationProfile.EvidenceSchemaHash = evidenceSchemaHash
	execution := shared.ProfileExecutionSnapshot{
		ManifestHash: bytes.Repeat([]byte{0x34}, types.Hash32Len), TokenizerHash: profileProjection.TokenizerHash,
		RuntimeClass: "CAUSAL_LM_PREFILL_LOGPROBS_V1", RequiredTopK: 8,
		GenerationType:      shared.GenerationType_GENERATION_TYPE_SAMPLED,
		VerificationProfile: verificationProfile, SchemaHash: profileProjection.SchemaHash,
	}
	profileHash, err := hubtypes.ProfileExecutionSnapshotHash(execution)
	require.NoError(t, err)
	hub.profile = hubtypes.ProfileStateSnapshot{
		ModelID: "model-v1", ProfileVersion: 1, Status: hubtypes.ModelStatusActive,
		ExecutionSnapshot: execution, ExecutionSnapshotHash: profileHash,
	}
	assignment := types.VerifierAssignmentState{
		TaskId: taskID, VerifyRound: types.VerifyRoundV1, SelectedVerifierCount: uint32(len(operators)),
		CommitDeadlineHeight: 50, VerifyDeadlineHeight: 200,
	}
	for index, operator := range operators {
		assignment.SelectedVerifiers = append(assignment.SelectedVerifiers, types.SelectedVerifierV1{
			OperatorAddress: operator, Slot: uint32(index + 1), SlotVersion: 1,
		})
	}
	require.NoError(t, base.keeper.TaskCore.Set(ctx, taskKey, types.TaskCoreState{
		TaskId: taskID, SessionId: bytes.Repeat([]byte{0x61}, types.Hash32Len), ModelId: "model-v1", ProfileVersion: 1,
		TaskPhase:          types.TaskPhase_TASK_PHASE_VERIFIER_ASSIGNED,
		VerificationStatus: types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_ASSIGNED,
	}))
	require.NoError(t, base.keeper.TaskAssignment.Set(ctx, taskKey, types.TaskAssignmentState{
		TaskId: taskID, GenerationParamsDigest: genDigest,
		CandidatePoolSnapshotId:      poolID,
		CandidatePoolHash:            poolHash,
		ProfileExecutionSnapshotHash: profileHash,
		EvidenceSchemaHash:           evidenceSchemaHash,
		CanonicalEncodingVersion:     "CANONICAL_OUTPUT_TEXT_V1",
		MetricAggregateProofVersion:  "PREFILL_METRIC_AGGREGATE_PROOF_V1",
	}))
	require.NoError(t, base.keeper.VerifierAssignment.Set(ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1), assignment))
	return &verificationFixture{
		internalFixture: base, hub: hub, ctx: ctx, server: msgServer{k: base.keeper},
		taskID: taskID, taskKey: taskKey, operators: operators, assignment: assignment,
		genDigest: genDigest, profileHash: profileHash,
	}
}

func (f *verificationFixture) setTaskBuilders(t *testing.T, builders ...string) {
	t.Helper()
	builderSetHash := bytes.Repeat([]byte{0xe1}, types.Hash32Len)
	membersHash, err := types.SelectedTaskBuildersHash(
		sdk.UnwrapSDKContext(f.ctx).ChainID(), f.taskID, "builder-set-v1", builderSetHash, builders,
	)
	require.NoError(t, err)
	require.NoError(t, f.keeper.TaskBuilderSelection.Set(f.ctx, f.taskKey, types.TaskBuilderSelectionState{
		TaskId: f.taskID, BuilderSetId: "builder-set-v1", BuilderSetHash: builderSetHash,
		SelectedTaskBuilders: builders, SelectedTaskBuilderCount: uint32(len(builders)),
		SelectedTaskBuildersHash: membersHash, BodyStatus: shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE,
	}))
}

// bytes32 builds a deterministic 32-byte value for fixtures that only need a
// distinct, non-zero hash.
func bytes32(value byte) []byte {
	out := make([]byte, types.Hash32Len)
	for i := range out {
		out[i] = value
	}
	return out
}
