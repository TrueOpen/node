package keeper

import (
	"bytes"
	"context"
	"testing"

	"github.com/TrueOpen/wire/dispatch"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

type valueEvidenceTestHub struct {
	*verificationHubStub
	facts  []shared.VerifierValueEvidenceFactV1
	faults map[string]hubtypes.RoleFaultState
}

func (h *valueEvidenceTestHub) RecordVerifierValueEvidence(ctx context.Context, fact shared.VerifierValueEvidenceFactV1) (hubtypes.RoleFaultState, error) {
	operator, err := types.CanonicalOperatorAddressBytes("value target", fact.TargetOperatorAddress)
	if err != nil {
		return hubtypes.RoleFaultState{}, err
	}
	id, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainRoleFaultV1)).Raw([]byte(sdkChainID(ctx)), fact.TaskId, operator, shared.EnumBE(uint32(fact.FaultedDuty)), shared.EnumBE(uint32(fact.FaultClass)), fact.EvidenceDigest).Sum()
	if err != nil {
		return hubtypes.RoleFaultState{}, err
	}
	h.facts = append(h.facts, fact)
	row := hubtypes.RoleFaultState{FaultId: id, TaskId: fact.TaskId, OperatorAddress: fact.TargetOperatorAddress, Duty: fact.FaultedDuty, FaultClass: fact.FaultClass, ClassificationSource: shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_OBJECTIVE_EVIDENCE, EvidenceDigest: fact.EvidenceDigest, RecordedHeight: uint64(sdkContextFrom(ctx).BlockHeight()), Status: hubtypes.RoleFaultStatus_ROLE_FAULT_STATUS_CONFIRMED}
	if h.faults == nil {
		h.faults = make(map[string]hubtypes.RoleFaultState)
	}
	h.faults[string(id)] = row
	return row, nil
}

func (h *valueEvidenceTestHub) ReadRoleFaultValue(_ context.Context, id shared.Hash32Key) (hubtypes.RoleFaultState, error) {
	return h.faults[string(id)], nil
}

func TestValueEvidenceSettlementUsesOriginalFaultAcrossPositionOrder(t *testing.T) {
	for _, laterBlock := range []bool{false, true} {
		t.Run(map[bool]string{false: "same block", true: "later block"}[laterBlock], func(t *testing.T) {
			f, _, evidence := valueEvidenceFixture(t, false)
			var original []byte
			for i, index := range []int{1, 0} {
				if i == 1 && laterBlock {
					f.ctx = sdk.WrapSDKContext(sdkContextFrom(f.ctx).WithBlockHeight(11))
				}
				raw, err := evidence[index].Marshal()
				require.NoError(t, err)
				result, err := f.server.SubmitVerifierValueEvidence(f.ctx, &types.MsgSubmitVerifierValueEvidence{EvidenceBytes: raw, SubmitterAddress: f.operators[2]})
				require.NoError(t, err)
				if i == 0 {
					original = bytes.Clone(result.FaultId)
				}
			}
			selected, err := f.keeper.firstValueEvidenceReceipts(f.ctx, f.taskID)
			require.NoError(t, err)
			require.Len(t, selected, 1)
			require.Equal(t, uint64(1), selected[0].Position)
			fault, err := f.keeper.hubKeeper.ReadRoleFaultValue(f.ctx, original)
			require.NoError(t, err)
			require.Equal(t, fault.EvidenceDigest, selected[0].EvidenceDigest)
			forOperator, found, err := f.keeper.valueEvidenceForOperator(f.ctx, f.taskID, selected[0].TargetOperatorAddress)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, fault.EvidenceDigest, forOperator.EvidenceDigest)
		})
	}
}

func valueEvidenceFixture(t *testing.T, workerInvalid bool) (*verificationFixture, *valueEvidenceTestHub, []*types.VerifierValueEvidenceV1) {
	t.Helper()
	f := newVerificationFixture(t)
	hub := &valueEvidenceTestHub{verificationHubStub: f.hub}
	f.keeper.hubKeeper = hub
	f.server = msgServer{k: f.keeper}
	core, err := f.keeper.TaskCore.Get(f.ctx, f.taskID)
	require.NoError(t, err)
	core.AcceptedTaskHash = bytes32(0x81)
	core.CreatedHeight = 1
	core.ObjectiveForgerySlashBpsSnapshot = 1000
	require.NoError(t, f.keeper.TaskCore.Set(f.ctx, f.taskID, core))
	worker := sdk.AccAddress(bytes.Repeat([]byte{0x44}, 20)).String()
	assignment, err := f.keeper.ReadTaskAssignment(f.ctx, f.taskID)
	require.NoError(t, err)
	assignment.WinnerWorker = worker
	f.hub.profile.ExecutionSnapshot.RequiredTopK = 2
	f.hub.profile.ExecutionSnapshot.VerificationProfile.Metrics = shared.MetricSpec{CompareLogprobDiff: true, CompareRankDelta: true}
	profileHash, err := hubtypes.ProfileExecutionSnapshotHash(f.hub.profile.ExecutionSnapshot)
	require.NoError(t, err)
	f.hub.profile.ExecutionSnapshotHash = profileHash
	assignment.ProfileExecutionSnapshotHash = profileHash
	require.NoError(t, f.keeper.WriteTaskAssignment(f.ctx, f.taskID, assignment))
	f.assignment.RevealDeadlineHeight = 60
	require.NoError(t, f.keeper.WriteVerifierAssignment(f.ctx, types.NewVerifyRoundKey(f.taskID, 1), f.assignment))
	require.NoError(t, f.keeper.WriteVerificationRound(f.ctx, types.NewVerifyRoundKey(f.taskID, 1), types.VerificationRoundState{TaskId: f.taskID, TaskHash: core.AcceptedTaskHash, VerifyRound: 1, RoundId: bytes32(0x82), RoundOpenHeight: 1, RoundCloseDeadlineHeight: 200, GeneratedTokenCount: 2}))
	require.NoError(t, f.keeper.extendValueEvidenceWindow(f.ctx, f.taskID, 60))
	workerRaw, err := f.keeper.sessionAddressToStore("Worker", worker)
	require.NoError(t, err)
	verifierRaw, err := f.keeper.sessionAddressToStore("Verifier", f.operators[0])
	require.NoError(t, err)
	signed := func(n int64) []byte { return shared.Uint64BE(uint64(n)) }
	token2 := uint32(7)
	if workerInvalid {
		token2 = 2
	}
	topk, err := shared.CanonicalRepeatedFramesV1([]shared.CanonicalFrameV1{shared.FlatCanonicalFrameV1(shared.Uint32BE(2), signed(-1_250_000)), shared.FlatCanonicalFrameV1(shared.Uint32BE(token2), signed(-2_000_000))}).Bytes()
	require.NoError(t, err)
	var workers, verifiers, metrics [][]byte
	for p := uint32(0); p < 2; p++ {
		workers = append(workers, shared.CanonicalFrameBytes([]byte(sdkChainID(f.ctx)), f.taskID, core.AcceptedTaskHash, workerRaw, shared.Uint32BE(p), shared.Uint32BE(2), signed(-1_250_000), shared.Uint32BE(1), topk, []byte{0}, []byte{1}))
		verifiers = append(verifiers, shared.CanonicalFrameBytes([]byte(sdkChainID(f.ctx)), f.taskID, core.AcceptedTaskHash, shared.Uint32BE(1), verifierRaw, shared.Uint32BE(p), shared.Uint32BE(2), signed(-1_200_000), shared.Uint32BE(1), bytes32(0x83), []byte{0}, []byte{1}))
	}
	proofTree := func(domain, tree string, leaves [][]byte) ([]byte, [][][]byte) {
		digests := make([][]byte, len(leaves))
		inner := make([][]byte, len(leaves))
		for i, leaf := range leaves {
			digest := dispatch.HashFields(domain, shared.Uint32BE(1), leaf)
			digests[i] = bytes.Clone(digest[:])
			inner[i] = dispatch.MerkleRootV1(tree, [][]byte{digest[:]})
		}
		return dispatch.MerkleRootV1(tree, digests), [][][]byte{{inner[1]}, {inner[0]}}
	}
	wr, wp := proofTree("TRUEOPEN_PREFILL_WORKER_VALUE_LEAF_V1", "TRUEOPEN_PREFILL_WORKER_VALUE_ROOT_V1", workers)
	vr, vp := proofTree("TRUEOPEN_PREFILL_VERIFIER_VALUE_LEAF_V1", "TRUEOPEN_PREFILL_VERIFIER_VALUE_ROOT_V1", verifiers)
	profile := f.hub.profile.ExecutionSnapshot
	verification := profile.VerificationProfile
	for p := uint32(0); p < 2; p++ {
		// Both positions commit an incorrect local difference, with otherwise
		// authentic task/profile/value roots. The Worker branch takes precedence.
		metrics = append(metrics, shared.CanonicalFrameBytes([]byte(sdkChainID(f.ctx)), f.taskID, core.AcceptedTaskHash, shared.Uint32BE(1), core.ModelId, shared.Uint32BE(core.ProfileVersion), []byte(verification.JudgmentFunctionVersion), []byte(verification.CanonicalEncodingVersion), verification.EvidenceSchemaHash, []byte(verification.MetricAggregateProofVersion), profile.TokenizerHash, f.genDigest, shared.Uint32BE(p), shared.Uint32BE(2), shared.Uint32BE(2), signed(-1_250_000), signed(-1_200_000), shared.Uint64BE(50_001), shared.Uint32BE(1), shared.Uint32BE(1), signed(0), []byte{0}, []byte{0}, []byte{0}, []byte{1}, vr))
	}
	mr, mp := proofTree("TRUEOPEN_PREFILL_TOKEN_METRIC_LEAF_V3", "TRUEOPEN_PREFILL_TOKEN_METRIC_ROOT_V3", metrics)
	commitment, _, err := types.WorkerValueCommitment(types.WorkerValueCommitmentV3{SchemaVersion: 3, ChainId: sdkChainID(f.ctx), TaskId: f.taskID, AcceptedTaskHash: core.AcceptedTaskHash, WorkerOperatorAddress: worker, EvidenceSchemaHash: assignment.EvidenceSchemaHash, WorkerValueRoot: wr, WorkerValuesEncodedSizeBytes: 2048})
	require.NoError(t, err)
	require.NoError(t, f.keeper.WriteInferReceipt(f.ctx, f.taskID, types.InferReceiptState{TaskId: f.taskID, WinnerWorker: worker, InferReceiptHash: bytes32(0x84), GenerationParamsDigest: f.genDigest, GeneratedTokenCount: 2, RequiredEvidenceCommitments: []types.EvidenceCommitmentV1{{EvidenceKind: shared.EvidenceKind_EVIDENCE_KIND_WORKER_VALUE_OPENING, EvidenceHashOrRoot: commitment[:], EncodedSizeBytes: 2048}}, EvidenceCommitmentCount: 1}))
	commitKey, err := types.DeriveCommitKey(sdkChainID(f.ctx), f.taskID, 1, f.operators[0])
	require.NoError(t, err)
	require.NoError(t, f.keeper.WriteResultReceipt(f.ctx, commitKey[:], types.ResultReceiptState{CommitKey: commitKey[:], TaskId: f.taskID, VerifyRound: 1, VerifierOperatorAddress: f.operators[0], MetricRoot: mr, MetricLeafCount: 2, VerifierValueRoot: vr, AcceptedHeight: 9}))
	var out []*types.VerifierValueEvidenceV1
	for p := uint64(0); p < 2; p++ {
		out = append(out, &types.VerifierValueEvidenceV1{SchemaVersion: 1, TaskId: f.taskID, VerifyRound: 1, TargetOperatorAddress: f.operators[0], EvidenceKind: types.VerifierValueEvidenceKindV1_VERIFIER_VALUE_EVIDENCE_KIND_V1_VERIFIER_VALUE_MISMATCH, WorkerValueCommitmentOpening: &types.WorkerValueCommitmentOpeningV1{WorkerValueRoot: wr, WorkerValuesEncodedSizeBytes: 2048}, Positions: []*types.VerifierValuePositionOpeningV1{{Position: p, WorkerValueLeaf: workers[p], WorkerValuePath: wp[p], VerifierValueLeaf: verifiers[p], VerifierValuePath: vp[p], MetricLeaf: metrics[p], MetricPath: mp[p]}}})
	}
	return f, hub, out
}

func TestValueEvidenceReceiptsDeduplicateAndDeferConsequences(t *testing.T) {
	for _, workerInvalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "Verifier", true: "Worker"}[workerInvalid], func(t *testing.T) {
			f, hub, evidence := valueEvidenceFixture(t, workerInvalid)
			submit := func(e *types.VerifierValueEvidenceV1) (*types.MsgSubmitVerifierValueEvidenceResponse, error) {
				raw, err := e.Marshal()
				require.NoError(t, err)
				return f.server.SubmitVerifierValueEvidence(f.ctx, &types.MsgSubmitVerifierValueEvidence{EvidenceBytes: raw, SubmitterAddress: f.operators[2]})
			}
			first, err := submit(evidence[0])
			require.NoError(t, err)
			require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, first.Status)
			events := len(sdkContextFrom(f.ctx).EventManager().Events())
			replay, err := submit(evidence[0])
			require.NoError(t, err)
			require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, replay.Status)
			require.Equal(t, first.FaultId, replay.FaultId)
			require.Len(t, sdkContextFrom(f.ctx).EventManager().Events(), events)
			second, err := submit(evidence[1])
			require.NoError(t, err)
			require.Equal(t, first.FaultId, second.FaultId)
			require.Len(t, hub.facts, 1)
			if workerInvalid {
				require.Equal(t, shared.DutyWorker, first.FaultedDuty)
				require.NotEqual(t, evidence[0].TargetOperatorAddress, first.TargetOperatorAddress)
			} else {
				require.Equal(t, shared.DutyVerifier, first.FaultedDuty)
			}
			query := &queryServer{k: f.keeper}
			page, err := query.VerifierValueEvidence(f.ctx, &types.QueryVerifierValueEvidenceRequest{TaskId: f.taskID, VerifyRound: 1, TargetOperatorAddress: first.TargetOperatorAddress, Page: shared.QueryPageRequestV1{Limit: 1}})
			require.NoError(t, err)
			require.Len(t, page.Receipts, 1)
			require.Equal(t, uint64(0), page.Receipts[0].Position)
			require.NotEmpty(t, page.Page.NextPageToken)
			next, err := query.VerifierValueEvidence(f.ctx, &types.QueryVerifierValueEvidenceRequest{TaskId: f.taskID, VerifyRound: 1, TargetOperatorAddress: first.TargetOperatorAddress, Page: shared.QueryPageRequestV1{Limit: 1, PageToken: page.Page.NextPageToken}})
			require.NoError(t, err)
			require.Len(t, next.Receipts, 1)
			require.Equal(t, uint64(1), next.Receipts[0].Position)
			require.NoError(t, f.keeper.EnsureValueEvidenceInvariant(f.ctx))
			summary, err := f.keeper.TaskRoundSummary.Get(f.ctx, f.taskID)
			require.NoError(t, err)
			require.Nil(t, summary.XRoundsClosedHeight)
			late := sdk.WrapSDKContext(sdkContextFrom(f.ctx).WithBlockHeight(int64(summary.GetVerifierValueEvidenceCloseHeight() + 1)))
			raw, err := evidence[0].Marshal()
			require.NoError(t, err)
			_, err = f.server.SubmitVerifierValueEvidence(late, &types.MsgSubmitVerifierValueEvidence{EvidenceBytes: raw, SubmitterAddress: f.operators[2]})
			require.ErrorIs(t, err, types.ErrDeadlineExpired)
		})
	}
}

func TestInvalidValueEvidenceAndCumulativeCapAreZeroWrite(t *testing.T) {
	f, hub, evidence := valueEvidenceFixture(t, false)
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	params.Evidence.MaxVerifierValueEvidencePositions = 1
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	good, err := evidence[0].Marshal()
	require.NoError(t, err)
	bad := bytes.Clone(good)
	bad = append(bad, 0x78, 0x01)
	_, err = f.server.SubmitVerifierValueEvidence(f.ctx, &types.MsgSubmitVerifierValueEvidence{EvidenceBytes: bad, SubmitterAddress: f.operators[2]})
	require.ErrorIs(t, err, types.ErrEvidenceOpeningInvalid)
	require.Empty(t, hub.facts)
	_, err = f.server.SubmitVerifierValueEvidence(f.ctx, &types.MsgSubmitVerifierValueEvidence{EvidenceBytes: good, SubmitterAddress: f.operators[2]})
	require.NoError(t, err)
	second, err := evidence[1].Marshal()
	require.NoError(t, err)
	_, err = f.server.SubmitVerifierValueEvidence(f.ctx, &types.MsgSubmitVerifierValueEvidence{EvidenceBytes: second, SubmitterAddress: f.operators[2]})
	require.ErrorIs(t, err, types.ErrEvidenceOpeningInvalid)
	require.Len(t, hub.facts, 1)
	rows, err := collectMapValues(f.ctx, f.keeper.ValueEvidenceReceipt)
	require.NoError(t, err)
	require.Len(t, rows, 1)
}
