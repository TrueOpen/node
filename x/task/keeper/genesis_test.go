package keeper_test

import (
	"bytes"
	"testing"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	proto "github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func TestGenesisV03RoundTripRebuildsDerivedIndexes(t *testing.T) {
	source := initFixture(t)
	input := taskGenesisV1(t, genesisChainID(source))
	report := types.DataUnavailableReportState{
		TaskId:                            append([]byte(nil), input.TaskCores[0].TaskId...),
		VerifyRound:                       types.VerifyRoundV1,
		VerifierOperatorAddress:           genesisVerifier,
		UnavailableTaskBuilderBitmap:      []byte{1},
		ServiceAuthorizationNonceSnapshot: 1,
		ReportHeight:                      7,
		ReportDigest:                      bytes.Repeat([]byte{0x94}, types.Hash32Len),
	}
	input.DataUnavailableReports = []types.DataUnavailableReportState{report}
	aggregate := types.BuilderDataUnavailableAggregateState{
		TaskId:                 append([]byte(nil), input.TaskCores[0].TaskId...),
		VerifyRound:            types.VerifyRoundV1,
		BuilderOperatorAddress: genesisBuilder,
		ValidReportCount:       1,
		RequiredReportCount:    1,
		ReportDigestsByVerifierSlot: []types.BuilderDataUnavailableSlotV1{
			{XReportDigest: &types.BuilderDataUnavailableSlotV1_ReportDigest{ReportDigest: append([]byte(nil), report.ReportDigest...)}},
			{},
		},
		AggregateHash:          bytes.Repeat([]byte{0x95}, types.Hash32Len),
		ThresholdReachedHeight: 8,
		Status:                 types.BuilderDataUnavailableAggregateStatusV1_BUILDER_DATA_UNAVAILABLE_AGGREGATE_STATUS_V1_CONFIRMED,
	}
	input.BuilderDataUnavailableAggregates = []types.BuilderDataUnavailableAggregateState{aggregate}
	require.NoError(t, source.keeper.InitGenesis(source.ctx, *input))
	selection := input.TaskBuilderSelections[0]
	ownerBytes, err := sdk.AccAddressFromBech32(input.SessionNonces[0].UserAddress)
	require.NoError(t, err)
	nonceStore, err := source.keeper.SessionNonce.Get(source.ctx, input.SessionNonces[0].UserAddress)
	require.NoError(t, err)
	require.Equal(t, []byte(ownerBytes), nonceStore.UserAddress)
	streamStore, err := source.keeper.Stream.Get(source.ctx, types.NewSessionKey(input.Streams[0].SessionId))
	require.NoError(t, err)
	require.Equal(t, []byte(ownerBytes), streamStore.OwnerUserAddress)
	assignmentStore, err := source.keeper.TaskAssignment.Get(source.ctx, types.NewTaskKey(input.TaskAssignments[0].TaskId))
	require.NoError(t, err)
	winnerAddress, err := sdk.AccAddressFromBech32(input.TaskAssignments[0].WinnerWorker)
	require.NoError(t, err)
	require.Equal(t, []byte(winnerAddress), assignmentStore.WinnerWorker)
	candidate := input.TaskCandidateFacts[0]
	candidateKey := types.NewTaskCandidateFactKey(types.NewTaskKey(candidate.TaskId), candidate.Stage, candidate.Slot)
	candidateStore, err := source.keeper.TaskCandidateFact.Get(source.ctx, candidateKey)
	require.NoError(t, err)
	candidateAddress, err := sdk.AccAddressFromBech32(candidate.OperatorAddress)
	require.NoError(t, err)
	require.Equal(t, []byte(candidateAddress), candidateStore.OperatorAddress)
	proposal := input.BuilderStageProposals[0]
	proposalKey := types.NewBuilderStageProposalKey(types.NewTaskKey(proposal.TaskId), proposal.Stage, proposal.ProposalDigest)
	proposalStore, err := source.keeper.BuilderStageProposal.Get(source.ctx, proposalKey)
	require.NoError(t, err)
	proposalAddress, err := sdk.AccAddressFromBech32(proposal.ProposerOperator)
	require.NoError(t, err)
	require.Equal(t, []byte(proposalAddress), proposalStore.ProposerOperator)
	reportKey := types.NewVerifyActorKey(types.NewTaskKey(report.TaskId), report.VerifyRound, report.VerifierOperatorAddress)
	reportStore, err := source.keeper.DataUnavailableReport.Get(source.ctx, reportKey)
	require.NoError(t, err)
	reportAddress, err := sdk.AccAddressFromBech32(report.VerifierOperatorAddress)
	require.NoError(t, err)
	require.Equal(t, []byte(reportAddress), reportStore.VerifierOperatorAddress)
	aggregateKey := types.NewVerifyActorKey(types.NewTaskKey(aggregate.TaskId), aggregate.VerifyRound, aggregate.BuilderOperatorAddress)
	aggregateStore, err := source.keeper.BuilderDataUnavailableAggregate.Get(source.ctx, aggregateKey)
	require.NoError(t, err)
	aggregateAddress, err := sdk.AccAddressFromBech32(aggregate.BuilderOperatorAddress)
	require.NoError(t, err)
	require.Equal(t, []byte(aggregateAddress), aggregateStore.BuilderOperatorAddress)
	require.Len(t, aggregateStore.ReportDigestsByVerifierSlot, 2)
	require.True(t, aggregateStore.ReportDigestsByVerifierSlot[0].HasReportDigest)
	require.False(t, aggregateStore.ReportDigestsByVerifierSlot[1].HasReportDigest)
	stored, err := source.keeper.TaskBuilderSelection.Get(source.ctx, types.NewTaskKey(selection.TaskId))
	require.NoError(t, err)
	encoded, err := stored.Marshal()
	require.NoError(t, err)
	for _, address := range selection.SelectedTaskBuilders {
		raw, err := sdk.AccAddressFromBech32(address)
		require.NoError(t, err)
		require.False(t, bytes.Contains(encoded, []byte(address)))
		require.True(t, bytes.Contains(encoded, raw))
	}

	exported, err := source.keeper.ExportGenesis(source.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(input, exported))

	taskKey := types.NewTaskKey(input.TaskCores[0].TaskId)
	inferKey := types.NewDeadlineIndexKey(input.TaskAssignments[0].InferDeadlineHeight, taskKey)
	has, err := source.keeper.InferDeadlineIndex.Has(source.ctx, inferKey)
	require.NoError(t, err)
	require.True(t, has)
	has, err = source.keeper.WorkerActiveTaskIndex.Has(source.ctx,
		types.NewWorkerActiveTaskKey(input.TaskAssignments[0].WinnerWorker, taskKey))
	require.NoError(t, err)
	require.True(t, has)
	has, err = source.keeper.SessionByOwnerIndex.Has(source.ctx,
		types.NewSessionByOwnerKey(input.Streams[0].OwnerUserAddress, input.Streams[0].SessionId))
	require.NoError(t, err)
	require.True(t, has)

	restarted := initFixture(t)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	reexported, err := restarted.keeper.ExportGenesis(restarted.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(exported, reexported))
}

func TestSettlementReceiptAddressesStoreRawBytesAndRoundTrip(t *testing.T) {
	f := initFixture(t)
	genesis := taskGenesisV1(t, genesisChainID(f))
	genesis.TaskCores[0].FinalityStatus = shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL
	genesis.TaskCores[0].XTaskFinalityHeight = &types.TaskCoreState_TaskFinalityHeight{TaskFinalityHeight: 10}
	payout := types.VerifierPayoutState{
		TaskId:                append([]byte(nil), genesis.TaskCores[0].TaskId...),
		SettlementId:          bytes.Repeat([]byte{0x92}, types.Hash32Len),
		OperatorAddress:       genesisVerifier,
		SelectedVerifierIndex: 1,
		Gross:                 shared.NewAmount(3),
		Maintenance:           shared.NewAmount(1),
		Net:                   shared.NewAmount(2),
	}
	genesis.TaskSettlements = []types.TaskSettlementState{{
		TaskId:                 append([]byte(nil), payout.TaskId...),
		SettlementId:           append([]byte(nil), payout.SettlementId...),
		XWorkerOperatorAddress: &types.TaskSettlementState_WorkerOperatorAddress{WorkerOperatorAddress: genesisWorker},
		WorkerGross:            shared.NewAmount(0),
		WorkerMaintenance:      shared.NewAmount(0),
		WorkerNet:              shared.NewAmount(0),
		VerifierSlotGross:      shared.NewAmount(0),
		MaintenanceFee:         shared.NewAmount(0),
		RefundAmount:           shared.NewAmount(0),
		OriginalReservedAmount: shared.NewAmount(0),
		GasReimbursedTotal:     shared.NewAmount(0),
	}}
	genesis.VerifierPayouts = []types.VerifierPayoutState{payout}
	reimbursement := types.TaskGasReimbursementV1{
		TaskId:            append([]byte(nil), payout.TaskId...),
		TxHash:            bytes.Repeat([]byte{0x93}, types.Hash32Len),
		ReimbursementKind: types.TaskReimbursementKindV1_TASK_REIMBURSEMENT_KIND_V1_VERIFY_COMMIT,
		FeePayer:          genesisUser,
		GasBasis:          2,
		ActualFeePaid:     shared.NewAmount(2),
		NecessaryFee:      shared.NewAmount(2),
		ReimbursedAmount:  shared.NewAmount(2),
		FeePolicyVersion:  1,
		AcceptedHeight:    7,
	}
	genesis.TaskGasReimbursements = []types.TaskGasReimbursementV1{reimbursement}
	genesis.TaskSettlements[0].GasReimbursedTotal = shared.NewAmount(2)
	factsHash := bytes.Repeat([]byte{0x9b}, types.Hash32Len)
	genesis.TaskSettlements[0].SettlementFactsHash = factsHash
	facts := types.SettlementFactsRetainedState{
		TaskId:                        append([]byte(nil), payout.TaskId...),
		TaskHash:                      append([]byte(nil), genesis.TaskCores[0].AcceptedTaskHash...),
		SettlementId:                  append([]byte(nil), payout.SettlementId...),
		SettlementFactsHash:           factsHash,
		SettlementDutyBuilderOperator: genesisBuilder,
		RecomputedVerdict:             types.TaskVerdict_TASK_VERDICT_PASS,
		FailureClass:                  types.TaskFailureClass_TASK_FAILURE_CLASS_NONE,
	}
	genesis.SettlementFactsRetained = []types.SettlementFactsRetainedState{facts}
	genesis.VerificationRounds = []types.VerificationRoundState{{
		TaskId:         append([]byte(nil), payout.TaskId...),
		VerifyRound:    types.ChallengeVerifyRoundV1,
		RoundId:        bytes.Repeat([]byte{0x9c}, types.Hash32Len),
		XOpenerAddress: &types.VerificationRoundState_OpenerAddress{OpenerAddress: genesisUser},
	}}
	funding := types.RoundFundingState{
		TaskId:                  append([]byte(nil), payout.TaskId...),
		VerifyRound:             types.ChallengeVerifyRoundV1,
		OpenerAddress:           genesisUser,
		ChallengeOpenBond:       shared.NewAmount(0),
		ChallengeSlotFee:        shared.NewAmount(0),
		VerifierBudget:          shared.NewAmount(0),
		TotalLock:               shared.NewAmount(0),
		BondLockedAmount:        shared.NewAmount(0),
		VerifierBudgetRemaining: shared.NewAmount(0),
		VerifierPaid:            shared.NewAmount(0),
		BondRefund:              shared.NewAmount(0),
		BondSlash:               shared.NewAmount(0),
		OpenerRecovery:          shared.NewAmount(0),
		TreasuryResidual:        shared.NewAmount(0),
		FundingLockHash:         bytes.Repeat([]byte{0x9d}, types.Hash32Len),
		XRoundOutcome:           &types.RoundFundingState_RoundOutcome{RoundOutcome: shared.RoundOutcomeV1_ROUND_OUTCOME_V1_CONCURRED},
		XFundingResolutionHash:  &types.RoundFundingState_FundingResolutionHash{FundingResolutionHash: bytes.Repeat([]byte{0x9e}, types.Hash32Len)},
		XClosedHeight:           &types.RoundFundingState_ClosedHeight{ClosedHeight: 9},
	}
	genesis.RoundFundings = []types.RoundFundingState{funding}
	require.NoError(t, genesis.Validate())
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	key := types.NewVerifierPayoutKey(types.NewTaskKey(payout.TaskId), payout.SelectedVerifierIndex)
	stored, err := f.keeper.VerifierPayout.Get(f.ctx, key)
	require.NoError(t, err)
	raw, err := sdk.AccAddressFromBech32(payout.OperatorAddress)
	require.NoError(t, err)
	require.Equal(t, []byte(raw), stored.OperatorAddress)
	gasKey := types.NewTaskGasReimbursementKey(types.NewTaskKey(reimbursement.TaskId), reimbursement.TxHash, reimbursement.ItemIndex)
	gasStore, err := f.keeper.TaskGasReimbursement.Get(f.ctx, gasKey)
	require.NoError(t, err)
	rawPayer, err := sdk.AccAddressFromBech32(reimbursement.FeePayer)
	require.NoError(t, err)
	require.Equal(t, []byte(rawPayer), gasStore.FeePayer)
	factsStore, err := f.keeper.SettlementFactsRetained.Get(f.ctx, types.NewTaskKey(facts.TaskId))
	require.NoError(t, err)
	builderAddress, err := sdk.AccAddressFromBech32(facts.SettlementDutyBuilderOperator)
	require.NoError(t, err)
	require.Equal(t, []byte(builderAddress), factsStore.SettlementDutyBuilderOperator)
	settlementStore, err := f.keeper.TaskSettlement.Get(f.ctx, types.NewTaskKey(payout.TaskId))
	require.NoError(t, err)
	workerAddress, err := sdk.AccAddressFromBech32(genesisWorker)
	require.NoError(t, err)
	require.True(t, settlementStore.HasWorkerOperatorAddress)
	require.Equal(t, []byte(workerAddress), settlementStore.WorkerOperatorAddress)
	fundingKey := types.NewVerifyRoundKey(types.NewTaskKey(funding.TaskId), funding.VerifyRound)
	fundingStore, err := f.keeper.RoundFunding.Get(f.ctx, fundingKey)
	require.NoError(t, err)
	openerAddress, err := sdk.AccAddressFromBech32(funding.OpenerAddress)
	require.NoError(t, err)
	require.Equal(t, []byte(openerAddress), fundingStore.OpenerAddress)
	require.True(t, fundingStore.HasRoundOutcome)
	require.True(t, fundingStore.HasFundingResolutionHash)
	require.True(t, fundingStore.HasClosedHeight)
	roundStore, err := f.keeper.VerificationRound.Get(f.ctx, fundingKey)
	require.NoError(t, err)
	require.Equal(t, []byte(openerAddress), roundStore.GetOpenerAddress())
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(genesis, exported))
	restarted := initFixture(t)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	reexported, err := restarted.keeper.ExportGenesis(restarted.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(exported, reexported))
}

func TestGenesisV03RebuildsVerifierWindowIndexes(t *testing.T) {
	f := initFixture(t)
	input := verifierSourceGenesisV1(t, genesisChainID(f))
	receipt := types.WorkerEvidenceReceiptState{
		SchemaVersion:            types.WorkerEvidenceSchemaVersionV1,
		TaskId:                   append([]byte(nil), input.TaskCores[0].TaskId...),
		WorkerOperatorAddress:    genesisWorker,
		EvidenceKind:             types.WorkerEvidenceKindV1_WORKER_EVIDENCE_KIND_V1_OUTPUT_CHUNK_EQUIVOCATION,
		AcceptedInferReceiptHash: append([]byte(nil), input.InferReceipts[0].InferReceiptHash...),
		EvidenceDigest:           bytes.Repeat([]byte{0x96}, types.Hash32Len),
		FaultId:                  bytes.Repeat([]byte{0x97}, types.Hash32Len),
		AcceptedHeight:           11,
	}
	input.WorkerEvidenceReceipts = []types.WorkerEvidenceReceiptState{receipt}
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *input))
	infer := input.InferReceipts[0]
	inferStore, err := f.keeper.InferReceipt.Get(f.ctx, types.NewTaskKey(infer.TaskId))
	require.NoError(t, err)
	winnerAddress, err := sdk.AccAddressFromBech32(infer.WinnerWorker)
	require.NoError(t, err)
	require.Equal(t, []byte(winnerAddress), inferStore.WinnerWorker)
	receiptKey := types.NewWorkerEvidenceReceiptKey(types.NewTaskKey(receipt.TaskId), receipt.WorkerOperatorAddress, receipt.EvidenceKind, receipt.Seq)
	storedReceipt, err := f.keeper.WorkerEvidenceReceipt.Get(f.ctx, receiptKey)
	require.NoError(t, err)
	workerAddress, err := sdk.AccAddressFromBech32(receipt.WorkerOperatorAddress)
	require.NoError(t, err)
	require.Equal(t, []byte(workerAddress), storedReceipt.WorkerOperatorAddress)
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(input, exported))
	restarted := initFixture(t)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	reexported, err := restarted.keeper.ExportGenesis(restarted.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(exported, reexported))

	window := input.VerifierCandidateWindows[0]
	taskKey := types.NewTaskKey(window.TaskId)
	has, err := f.keeper.VerifierWindowBuildIndex.Has(f.ctx,
		types.NewVerifyRoundIndexKey(window.WindowRandomnessHeight, taskKey, window.VerifyRound))
	require.NoError(t, err)
	require.True(t, has)
	has, err = f.keeper.VerifierHandraiseCloseIndex.Has(f.ctx,
		types.NewVerifyRoundIndexKey(window.HandraiseCloseHeight, taskKey, window.VerifyRound))
	require.NoError(t, err)
	require.True(t, has)
	has, err = f.keeper.VerifyOpenDeadlineIndex.Has(f.ctx,
		types.NewDeadlineIndexKey(window.AssignmentDeadlineHeight, taskKey))
	require.NoError(t, err)
	require.True(t, has)
}

func TestVerifierWindowMemberAddressStoresRawBytesAndRoundTrips(t *testing.T) {
	f := initFixture(t)
	input := verifierReadyGenesisV1(t, genesisChainID(f))
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *input))
	member := input.VerifierCandidateWindowMembers[0]
	key := types.NewVerifierWindowMemberKey(types.NewTaskKey(member.TaskId), member.VerifyRound, member.RankIndex)
	stored, err := f.keeper.VerifierCandidateWindowMember.Get(f.ctx, key)
	require.NoError(t, err)
	raw, err := sdk.AccAddressFromBech32(member.OperatorAddress)
	require.NoError(t, err)
	require.Equal(t, []byte(raw), stored.OperatorAddress)
	assignment := input.VerifierAssignments[0]
	assignmentKey := types.NewVerifyRoundKey(types.NewTaskKey(assignment.TaskId), assignment.VerifyRound)
	storedAssignment, err := f.keeper.VerifierAssignment.Get(f.ctx, assignmentKey)
	require.NoError(t, err)
	require.Len(t, storedAssignment.SelectedVerifiers, len(assignment.SelectedVerifiers))
	for i, selected := range assignment.SelectedVerifiers {
		selectedRaw, err := sdk.AccAddressFromBech32(selected.OperatorAddress)
		require.NoError(t, err)
		require.Equal(t, []byte(selectedRaw), storedAssignment.SelectedVerifiers[i].OperatorAddress)
	}
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(input, exported))
	restarted := initFixture(t)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	reexported, err := restarted.keeper.ExportGenesis(restarted.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(exported, reexported))
}

func TestVerificationReceiptAddressesStoreRawBytesAndRoundTrip(t *testing.T) {
	f := initFixture(t)
	input := verifierReadyGenesisV1(t, genesisChainID(f))
	taskID := input.TaskCores[0].TaskId
	verifier := input.VerifierAssignments[0].SelectedVerifiers[0].OperatorAddress
	commitHash, err := types.DeriveCommitKey(genesisChainID(f), taskID, types.VerifyRoundV1, verifier)
	require.NoError(t, err)
	commit := types.CommitState{
		CommitKey: commitHash[:], TaskId: taskID, VerifyRound: types.VerifyRoundV1,
		VerifierOperatorAddress: verifier,
		CommitHash:              bytes.Repeat([]byte{0x98}, types.Hash32Len),
		CommitSigningDigest:     bytes.Repeat([]byte{0x99}, types.Hash32Len),
		SignatureDigest:         bytes.Repeat([]byte{0x9a}, types.Hash32Len),
		CommitHeight:            29,
		Status:                  types.CommitStatusV1_COMMIT_STATUS_V1_ACCEPTED,
	}
	result := types.ResultReceiptState{
		CommitKey: commitHash[:], TaskId: taskID, VerifyRound: types.VerifyRoundV1,
		VerifierOperatorAddress: verifier,
		MetricSummary: types.MetricSummaryV1{
			FiniteCount:            1,
			XTopkJaccardMeanFp_1E6: &types.MetricSummaryV1_TopkJaccardMeanFp_1E6{TopkJaccardMeanFp_1E6: 0},
		},
		AcceptedHeight: 31,
	}
	input.Commits = []types.CommitState{commit}
	input.ResultReceipts = []types.ResultReceiptState{result}
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *input))
	key := types.NewCommitKey(commitHash[:])
	storedCommit, err := f.keeper.CommitState.Get(f.ctx, key)
	require.NoError(t, err)
	storedResult, err := f.keeper.ResultReceiptState.Get(f.ctx, key)
	require.NoError(t, err)
	raw, err := sdk.AccAddressFromBech32(verifier)
	require.NoError(t, err)
	require.Equal(t, []byte(raw), storedCommit.VerifierOperatorAddress)
	require.Equal(t, []byte(raw), storedResult.VerifierOperatorAddress)
	require.True(t, storedResult.MetricSummary.HasTopkJaccardMeanFp_1E6)
	require.False(t, storedResult.MetricSummary.HasUnionJsP99Fp_1E6)
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(input, exported))
	restarted := initFixture(t)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	reexported, err := restarted.keeper.ExportGenesis(restarted.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(exported, reexported))
}

func TestInferReceiptPrivateProjectionPreservesEvidenceOrder(t *testing.T) {
	f := initFixture(t)
	taskID := bytes.Repeat([]byte{0xa8}, types.Hash32Len)
	state := types.InferReceiptState{
		TaskId:       taskID,
		WinnerWorker: genesisWorker,
		RequiredEvidenceCommitments: []types.EvidenceCommitmentV1{
			{EvidenceKind: shared.EvidenceKind_EVIDENCE_KIND_WORKER_VALUE_OPENING, EvidenceHashOrRoot: bytes.Repeat([]byte{0xa9}, types.Hash32Len), EncodedSizeBytes: 7},
			{EvidenceKind: shared.EvidenceKind_EVIDENCE_KIND_VERIFIER_VALUE_OPENING, EvidenceHashOrRoot: bytes.Repeat([]byte{0xaa}, types.Hash32Len), EncodedSizeBytes: 9},
		},
		EvidenceCommitmentCount: 2,
	}
	key := types.NewTaskKey(taskID)
	require.NoError(t, f.keeper.WriteInferReceipt(f.ctx, key, state))
	stored, err := f.keeper.InferReceipt.Get(f.ctx, key)
	require.NoError(t, err)
	require.Len(t, stored.RequiredEvidenceCommitments, 2)
	require.Equal(t, int32(shared.EvidenceKind_EVIDENCE_KIND_WORKER_VALUE_OPENING), stored.RequiredEvidenceCommitments[0].EvidenceKind)
	require.Equal(t, int32(shared.EvidenceKind_EVIDENCE_KIND_VERIFIER_VALUE_OPENING), stored.RequiredEvidenceCommitments[1].EvidenceKind)
	projected, err := f.keeper.ReadInferReceipt(f.ctx, key)
	require.NoError(t, err)
	require.True(t, proto.Equal(&state, &projected))
}

func TestVerificationRoundPrivateProjectionPreservesOptionalFields(t *testing.T) {
	f := initFixture(t)
	taskID := bytes.Repeat([]byte{0xab}, types.Hash32Len)
	hash := bytes.Repeat([]byte{0xac}, types.Hash32Len)
	state := types.VerificationRoundState{
		TaskId: taskID, TaskHash: hash, VerifyRound: types.ChallengeVerifyRoundV1,
		RoundId: hash, RoundOpenHeight: 1, RoundCloseDeadlineHeight: 2,
		InferReceiptRef: hash, GeneratedTokenCount: 3,
		ProfileExecutionSnapshotHash: hash, GenerationParamsDigest: hash,
		RoundEffectCount:       4,
		XOpenerAddress:         &types.VerificationRoundState_OpenerAddress{OpenerAddress: genesisUser},
		XPrevRoundFactsHash:    &types.VerificationRoundState_PrevRoundFactsHash{PrevRoundFactsHash: hash},
		XPrevRoundVerdict:      &types.VerificationRoundState_PrevRoundVerdict{PrevRoundVerdict: types.TaskVerdict_TASK_VERDICT_PASS},
		XClosedHeight:          &types.VerificationRoundState_ClosedHeight{ClosedHeight: 0},
		XResultReceiptRefsHash: &types.VerificationRoundState_ResultReceiptRefsHash{ResultReceiptRefsHash: hash},
		XConsensusClusterHash:  &types.VerificationRoundState_ConsensusClusterHash{ConsensusClusterHash: hash},
		XVerdict:               &types.VerificationRoundState_Verdict{Verdict: types.TaskVerdict_TASK_VERDICT_PASS},
		XFailureClass:          &types.VerificationRoundState_FailureClass{FailureClass: types.TaskFailureClass_TASK_FAILURE_CLASS_NONE},
		XRoundOutcome:          &types.VerificationRoundState_RoundOutcome{RoundOutcome: shared.RoundOutcomeV1_ROUND_OUTCOME_V1_CONCURRED},
		XRoundFactsHash:        &types.VerificationRoundState_RoundFactsHash{RoundFactsHash: hash},
		XRoundEffectPlanRoot:   &types.VerificationRoundState_RoundEffectPlanRoot{RoundEffectPlanRoot: hash},
		XRoundEffectRoot:       &types.VerificationRoundState_RoundEffectRoot{RoundEffectRoot: hash},
		XFundingLockHash:       &types.VerificationRoundState_FundingLockHash{FundingLockHash: hash},
		XFundingResolutionHash: &types.VerificationRoundState_FundingResolutionHash{FundingResolutionHash: hash},
	}
	key := types.NewVerifyRoundKey(types.NewTaskKey(taskID), state.VerifyRound)
	require.NoError(t, f.keeper.WriteVerificationRound(f.ctx, key, state))
	stored, err := f.keeper.VerificationRound.Get(f.ctx, key)
	require.NoError(t, err)
	raw, err := sdk.AccAddressFromBech32(genesisUser)
	require.NoError(t, err)
	require.Equal(t, []byte(raw), stored.GetOpenerAddress())
	projected, err := f.keeper.ReadVerificationRound(f.ctx, key)
	require.NoError(t, err)
	require.True(t, proto.Equal(&state, &projected))
}

func TestTaskTerminalSummaryAddressStoresRawBytesAndRoundTrips(t *testing.T) {
	f := initFixture(t)
	input := types.DefaultGenesis()
	summary := types.TaskTerminalSummaryState{
		TaskId:           bytes.Repeat([]byte{0xad}, types.Hash32Len),
		SessionId:        bytes.Repeat([]byte{0xae}, types.Hash32Len),
		TaskHash:         bytes.Repeat([]byte{0xaf}, types.Hash32Len),
		TerminalPhase:    types.TaskPhase_TASK_PHASE_SETTLED,
		Verdict:          types.TaskVerdict_TASK_VERDICT_PASS,
		FailureClass:     types.TaskFailureClass_TASK_FAILURE_CLASS_NONE,
		SettlementStatus: types.SettlementStatus_SETTLEMENT_STATUS_FINALIZED,
		FinalityStatus:   shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL,
		XWinnerWorker:    &types.TaskTerminalSummaryState_WinnerWorker{WinnerWorker: genesisWorker},
		XRound2Outcome:   &types.TaskTerminalSummaryState_Round2Outcome{Round2Outcome: shared.RoundOutcomeV1_ROUND_OUTCOME_V1_CONCURRED},
		CreatedHeight:    1,
		CompactedHeight:  2,
	}
	input.TaskTerminalSummaries = []types.TaskTerminalSummaryState{summary}
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *input))
	stored, err := f.keeper.TaskTerminalSummary.Get(f.ctx, types.NewTaskKey(summary.TaskId))
	require.NoError(t, err)
	raw, err := sdk.AccAddressFromBech32(genesisWorker)
	require.NoError(t, err)
	require.Equal(t, []byte(raw), stored.GetWinnerWorker())
	require.NotNil(t, stored.XRound2Outcome)
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(input, exported))
	restarted := initFixture(t)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	reexported, err := restarted.keeper.ExportGenesis(restarted.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(exported, reexported))
}

func TestGenesisV03RejectedImportLeavesNoTaskWrites(t *testing.T) {
	f := initFixture(t)
	input := taskGenesisV1(t, genesisChainID(f))
	input.TaskCores = append(input.TaskCores, input.TaskCores[0])

	err := f.keeper.InitGenesis(f.ctx, *input)
	require.ErrorContains(t, err, "duplicate task core")
	_, err = f.keeper.Params.Get(f.ctx)
	require.ErrorIs(t, err, collections.ErrNotFound)
	has, err := f.keeper.TaskCore.Has(f.ctx, types.NewTaskKey(input.TaskCores[0].TaskId))
	require.NoError(t, err)
	require.False(t, has)
}
