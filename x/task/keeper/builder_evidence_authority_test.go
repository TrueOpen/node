package keeper

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/wire/bus"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func TestCanonicalBuilderDataUnavailableAcceptsOnlyConfirmedTaskFact(t *testing.T) {
	f := newVerificationFixture(t)
	builder := f.operators[2]
	f.setTaskBuilders(t, builder)
	unionKey := types.NewTaskStageKey(f.taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY)
	union := types.TaskStageHandraiseUnionState{
		SchemaVersion: 1, TaskId: f.taskID,
		Stage:                           types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY,
		Status:                          types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_FINALIZED,
		DataReadyAttestingBuilderBitmap: []byte{0x01},
	}
	require.NoError(t, f.keeper.TaskStageHandraiseUnion.Set(f.ctx, unionKey, union))
	aggregateKey := types.NewVerifyActorKey(f.taskKey, types.VerifyRoundV1, builder)
	aggregate := types.BuilderDataUnavailableAggregateState{
		TaskId: f.taskID, VerifyRound: types.VerifyRoundV1, BuilderOperatorAddress: builder,
		ValidReportCount: 2, RequiredReportCount: 2,
		AggregateHash: bytes.Repeat([]byte{0xa7}, types.Hash32Len), ThresholdReachedHeight: 10,
		Status: types.BuilderDataUnavailableAggregateStatusV1_BUILDER_DATA_UNAVAILABLE_AGGREGATE_STATUS_V1_CONFIRMED,
	}
	require.NoError(t, f.keeper.WriteDataUnavailableAggregate(f.ctx, aggregateKey, aggregate))

	fact, err := f.keeper.canonicalBuilderDataUnavailable(f.ctx, bus.DataUnavailableStateReferenceV1{
		TaskID: f.taskID, VerifyRound: types.VerifyRoundV1, BuilderOperator: builder,
	})
	require.NoError(t, err)
	require.Equal(t, uint32(2), fact.SchemaVersion)
	require.Equal(t, builder, fact.BuilderOperator)
	require.Equal(t, shared.BuilderEvidenceKind_BUILDER_EVIDENCE_KIND_OBJECTIVE_DATA_UNAVAILABLE, fact.EvidenceKind)
	require.Equal(t, f.taskID, fact.ScopeId)
	require.Len(t, fact.CanonicalEvidenceDigest, types.Hash32Len)

	union.DataReadyAttestingBuilderBitmap[0] = 0
	require.NoError(t, f.keeper.TaskStageHandraiseUnion.Set(f.ctx, unionKey, union))
	replayed, err := f.keeper.canonicalBuilderDataUnavailable(f.ctx, bus.DataUnavailableStateReferenceV1{
		TaskID: f.taskID, VerifyRound: types.VerifyRoundV1, BuilderOperator: builder,
	})
	require.NoError(t, err, "the retained CONFIRMED aggregate is the durable attester authority")
	require.Equal(t, fact, replayed)
	union.DataReadyAttestingBuilderBitmap[0] = 1
	require.NoError(t, f.keeper.TaskStageHandraiseUnion.Set(f.ctx, unionKey, union))

	aggregate.Status = types.BuilderDataUnavailableAggregateStatusV1_BUILDER_DATA_UNAVAILABLE_AGGREGATE_STATUS_V1_COLLECTING
	require.NoError(t, f.keeper.WriteDataUnavailableAggregate(f.ctx, aggregateKey, aggregate))
	_, err = f.keeper.canonicalBuilderDataUnavailable(f.ctx, bus.DataUnavailableStateReferenceV1{
		TaskID: f.taskID, VerifyRound: types.VerifyRoundV1, BuilderOperator: builder,
	})
	require.ErrorContains(t, err, "did not make")
}

func TestCanonicalBuilderObjectiveEvidenceRejectsRetiredTagFour(t *testing.T) {
	f := newVerificationFixture(t)
	_, err := f.keeper.canonicalBuilderObjectiveEvidence(f.ctx, "trueopen-test", bus.DecodedBuilderEvidenceV2{SchemaVersion: 2})
	require.ErrorContains(t, err, "exactly one ACTIVE")
}
