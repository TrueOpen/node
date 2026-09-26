package keeper_test

import (
	"bytes"
	"math"
	"testing"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestServiceSlashWaterfallMovesCustodyWritesSummaryAndTerminatesUnbonding(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	identity := hubIdentity(t, 231)
	operator := identity.Address
	const initialBond = uint64(1_000_000)
	registerCortexNodeIdentityForTest(t, f, operator, identity, initialBond, 2, 1)
	activateServiceBondForTest(t, f, operator, 1)

	bond, unbonding, err := f.keeper.BeginServiceUnstake(f.ctx, operator, 400_000, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(600_000), bond.ActiveBond)

	taskID := bytes.Repeat([]byte{0xaa}, 32)
	sessionID := bytes.Repeat([]byte{0xbb}, 32)
	require.NoError(t, f.keeper.CreditTaskSettlementEarnings(f.ctx, sessionID, taskID,
		[]types.TaskEarningsCredit{{Beneficiary: operator, Amount: shared.NewAmount(20)}}, 21))
	f.bank.seedModule(types.RewardsModuleName, 20)

	fault := seedRoleFaultForTest(
		t, f, operator, shared.DutyWorker, taskID, hubHashBytes("service-waterfall-evidence"), 30, "waterfall",
	)
	request := keeper.ApplyServiceSlashRequest{
		OperatorAddress: operator, Duty: shared.DutyWorker, TaskID: taskID,
		Requested: initialBond + 20 + 7, Height: 30,
		SummaryID:   append([]byte(nil), fault.FaultId...),
		SourceKind:  types.SlashSourceKind_SLASH_SOURCE_KIND_ROLE_FAULT,
		SourceID:    append([]byte(nil), fault.FaultId...),
		Destination: types.SlashDestination_SLASH_DESTINATION_TREASURY,
	}
	result, err := f.keeper.ApplyServiceSlash(f.ctx, request)
	require.NoError(t, err)
	require.Zero(t, result.PendingEarningsApplied)
	require.Equal(t, uint64(600_000), result.ActiveBondApplied)
	require.Equal(t, uint64(400_000), result.UnbondingApplied)
	require.Equal(t, uint64(20), result.ClaimableApplied)
	require.Equal(t, uint64(7), result.Unfilled)

	fault.XSlashSummaryId = &types.RoleFaultState_SlashSummaryId{SlashSummaryId: append([]byte(nil), fault.FaultId...)}
	require.NoError(t, f.keeper.WriteRoleFaultValue(f.ctx, fault.FaultId, fault))
	require.NoError(t, f.keeper.EnsureSlashSummaryInvariant(f.ctx))
	require.NoError(t, f.keeper.EnsureServiceBondInvariant(f.ctx))
	require.NoError(t, f.keeper.EnsureRewardsEarningsInvariant(f.ctx))
	require.Zero(t, f.bank.moduleBalance(types.ServiceBondModuleName))
	require.Zero(t, f.bank.moduleBalance(types.RewardsModuleName))
	require.Equal(t, result.Applied, f.bank.moduleBalance(types.TreasuryModuleName))

	idKey := shared.Hash32Key(unbonding.UnbondingId)
	_, err = f.keeper.ReadUnbondingValue(f.ctx, types.NewUnbondingKey(operator, idKey))
	require.ErrorIs(t, err, collections.ErrNotFound)
	receipt, err := f.keeper.ReadUnbondingReceiptValue(f.ctx, idKey)
	require.NoError(t, err)
	storedReceipt, err := f.keeper.UnbondingReceipt.Get(f.ctx, idKey)
	require.NoError(t, err)
	operatorBytes, err := sdk.AccAddressFromBech32(receipt.OperatorAddress)
	require.NoError(t, err)
	require.Equal(t, []byte(operatorBytes), storedReceipt.OperatorAddress)
	require.Equal(t, types.UnbondingReceiptTerminalStatus_UNBONDING_RECEIPT_TERMINAL_STATUS_FULLY_SLASHED, receipt.TerminalStatus)
	require.Equal(t, uint64(400_000), receipt.SlashedAmount)
	summary, err := f.keeper.ReadSlashSummaryValue(f.ctx, types.NewSlashSummaryKey(
		types.SlashSourceKind_SLASH_SOURCE_KIND_ROLE_FAULT, fault.FaultId, 0,
	))
	require.NoError(t, err)
	storedSummary, err := f.keeper.SlashSummary.Get(f.ctx, types.NewSlashSummaryKey(
		types.SlashSourceKind_SLASH_SOURCE_KIND_ROLE_FAULT, fault.FaultId, 0,
	))
	require.NoError(t, err)
	summaryOperatorBytes, err := sdk.AccAddressFromBech32(summary.OperatorAddress)
	require.NoError(t, err)
	require.Equal(t, []byte(summaryOperatorBytes), storedSummary.OperatorAddress)
	require.Equal(t, shared.NewAmount(400_000), summary.UnbondingDebit)
	require.Equal(t, shared.NewAmount(7), summary.UnfilledAmount)

	beforeTreasury := f.bank.moduleBalance(types.TreasuryModuleName)
	replayRequest := request
	replayRequest.Height++
	replayed, err := f.keeper.ApplyServiceSlash(f.ctx, replayRequest)
	require.NoError(t, err)
	require.Equal(t, result, replayed)
	require.Equal(t, beforeTreasury, f.bank.moduleBalance(types.TreasuryModuleName))

	query, err := keeper.NewQueryServerImpl(f.keeper).Fault(f.ctx, &types.QueryFaultRequest{FaultId: fault.FaultId})
	require.NoError(t, err)
	require.NotNil(t, query.SlashSummary)
	require.Equal(t, summary, *query.SlashSummary)

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	restarted := initFixture(t)
	treasuryBalance, err := shared.ParseAmount(exported.Treasury.Balance)
	require.NoError(t, err)
	restarted.bank.seedModule(types.TreasuryModuleName, treasuryBalance)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	require.NoError(t, restarted.keeper.EnsureSlashSummaryInvariant(restarted.ctx))

	// receipt_hash is the only surviving proof of a terminated unbonding once the
	// primary row is gone, and its preimage is chain-id bound. Types validation only
	// checks that it is 32 bytes, so an unrecomputed import would accept - and then
	// serve - a digest no node can re-derive, including one copied from another
	// chain. InitGenesis has to recompute it and refuse the mismatch.
	tampered := *exported
	tampered.ServiceUnbondingReceipts = append([]types.UnbondingReceiptState(nil), exported.ServiceUnbondingReceipts...)
	require.NotEmpty(t, tampered.ServiceUnbondingReceipts)
	tampered.ServiceUnbondingReceipts[0].ReceiptHash = hubHashBytes("not the canonical receipt preimage")
	require.NoError(t, tampered.Validate(), "the forged hash must be individually well-formed, so only recomputation can catch it")
	forged := initFixture(t)
	require.ErrorContains(
		t, forged.keeper.InitGenesis(forged.ctx, tampered),
		"receipt_hash does not match its canonical preimage",
	)
}

func TestServiceUnbondingCapIncludesMatureRowsAndRoundTrips(t *testing.T) {
	f := initFixture(t)
	genesis := types.DefaultGenesis()
	genesis.Params.Service.MaxOpenUnbondingEntriesPerOperator = 2
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	identity := hubIdentity(t, 232)
	operator := identity.Address
	registerCortexNodeIdentityForTest(t, f, operator, identity, testServiceBondMinInitial, 2, 1)
	activateServiceBondForTest(t, f, operator, 1)

	_, first, err := f.keeper.BeginServiceUnstake(f.ctx, operator, 1, 10)
	require.NoError(t, err)
	_, second, err := f.keeper.BeginServiceUnstake(f.ctx, operator, 1, 11)
	require.NoError(t, err)
	_, _, err = f.keeper.ProcessUnbondingMaturities(f.ctx, second.MatureHeight, 10, 1<<20)
	require.NoError(t, err)
	first, err = f.keeper.ReadUnbondingValue(f.ctx, types.NewUnbondingKey(operator, first.UnbondingId))
	require.NoError(t, err)
	second, err = f.keeper.ReadUnbondingValue(f.ctx, types.NewUnbondingKey(operator, second.UnbondingId))
	require.NoError(t, err)
	require.Equal(t, types.UnbondingStatusMature, first.Status)
	require.Equal(t, types.UnbondingStatusMature, second.Status)

	_, _, err = f.keeper.BeginServiceUnstake(f.ctx, operator, 1, second.MatureHeight+1)
	require.ErrorContains(t, err, "max open unbonding entries reached")
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	restarted := initFixture(t)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
}

func TestApplyServiceSlashBankFailureRollsBackStateAndSummary(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	operator := hubAddress(t, 233)
	const activeBond = uint64(math.MaxInt64) + 1
	require.NoError(t, f.keeper.WriteServiceBondValue(f.ctx, operator, types.ServiceBondState{
		OperatorAddress: operator, ActiveBond: activeBond, EffectiveActiveBond: activeBond,
		BondVersion: 1, Status: types.ServiceBondStatusActive,
	}))
	request := serviceSlashRequestForTest(operator, shared.DutyWorker, "bank-failure", activeBond, 10)
	_, err := f.keeper.ApplyServiceSlash(f.ctx, request)
	require.ErrorContains(t, err, "module account hub_service_bond balance below required amount")
	bond, err := f.keeper.GetServiceBondState(f.ctx, operator)
	require.NoError(t, err)
	require.Equal(t, activeBond, bond.ActiveBond)
	_, err = f.keeper.ReadSlashSummaryValue(f.ctx, types.NewSlashSummaryKey(
		request.SourceKind, request.SourceID, request.EffectIndex,
	))
	require.ErrorIs(t, err, collections.ErrNotFound)
}

func TestApplyServiceSlashRejectsCrossNamespaceDestination(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	request := serviceSlashRequestForTest(hubAddress(t, 235), shared.DutyWorker, "wrong-destination", 1, 10)
	request.Destination = types.SlashDestination_SLASH_DESTINATION_CHALLENGE_EFFECT_POOL
	_, err := f.keeper.ApplyServiceSlash(f.ctx, request)
	require.ErrorContains(t, err, "role-fault slash source identity is invalid")
}

func TestTaskRoleFaultConsumesLiabilitySlashesCustodyAndJails(t *testing.T) {
	f := initCandidatePoolFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	identity := hubIdentity(t, 234)
	operator := identity.Address
	registerCortexNodeIdentityForTest(t, f, operator, identity, testServiceBondMinInitial, 2, 1)
	activateServiceBondForTest(t, f, operator, 1)
	capabilityVersion := declareTaskLiabilitySupportForTest(
		t, f, operator, types.ServiceBondRoleWorker, "model-task-role-fault", 1, testServiceBondMinInitial, 3,
	)
	taskID := hubHash("task-role-fault")
	reservation := seedOwnedTaskLiabilityForTest(t, f, taskID, operator, shared.DutyWorker, capabilityVersion, 4)
	before, err := f.keeper.GetServiceBondState(f.ctx, operator)
	require.NoError(t, err)
	beforeServiceCustody := f.bank.moduleBalance(types.ServiceBondModuleName)

	_, err = f.keeper.ApplyTaskRoleFault(f.ctx, types.TaskRoleFaultFact{
		SessionID: hubHashBytes("task-role-fault-session"), TaskID: reservation.TaskId,
		OperatorAddress: operator, Duty: shared.DutyWorker, FaultType: types.FaultTypeWorkerInferTimeout,
		ClassificationSource: shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_DEADLINE,
		EvidenceDigest:       hubHashBytes("task-role-fault-evidence"), SlashBps: 5_000, Height: 10,
	})
	require.NoError(t, err)
	expected := types.SlashByFraction(reservation.ReservedAmount, 5_000, types.ObjectiveForgerySlashDenom)
	after, err := f.keeper.GetServiceBondState(f.ctx, operator)
	require.NoError(t, err)
	require.Equal(t, before.ActiveBond-expected, after.ActiveBond)
	require.Equal(t, beforeServiceCustody-expected, f.bank.moduleBalance(types.ServiceBondModuleName))
	require.Equal(t, expected, f.bank.moduleBalance(types.TreasuryModuleName))
	stored, err := f.keeper.ReadTaskLiabilityValue(f.ctx, types.NewTaskLiabilityReservationKey(reservation.TaskId, shared.DutyWorker, operator))
	require.NoError(t, err)
	require.Equal(t, types.TaskLiabilityStatusSlashed, stored.Status)
	fault := onlyRoleFaultForTask(t, f, reservation.TaskId)
	require.Len(t, fault.GetSlashSummaryId(), 32)
	// WORKER_INFER_TIMEOUT is a purely economic deadline miss: it closes the
	// liability and moves custody, but shouldJailForFault deliberately excludes
	// it, so neither the receipt nor the operator-global counter may move.
	require.Zero(t, fault.JailDelta)
	require.Zero(t, after.JailCount)
	require.Equal(t, before.Status, after.Status)

	// A jailing worker-duty timeout drives the operator-global counter to the
	// tombstone threshold; jail_delta is written on the receipt only when the
	// counter actually advanced.
	params := types.DefaultHubParams()
	for index := uint32(0); index < params.Service.TombstoneJailCountThreshold; index++ {
		nextTaskID := hubHash("task-role-fault-" + string(rune('a'+index)))
		next := seedOwnedTaskLiabilityForTest(t, f, nextTaskID, operator, shared.DutyWorker, capabilityVersion, uint64(10+index))
		_, applyErr := f.keeper.ApplyTaskRoleFault(f.ctx, types.TaskRoleFaultFact{
			SessionID: hubHashBytes("task-role-fault-session-" + string(rune('a'+index))), TaskID: next.TaskId,
			OperatorAddress: operator, Duty: shared.DutyWorker, FaultType: types.FaultTypeWorkerRevealTimeout,
			ClassificationSource: shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_DEADLINE,
			EvidenceDigest:       hubHashBytes("task-role-fault-evidence-" + string(rune('a'+index))), Height: uint64(20 + index),
		})
		require.NoError(t, applyErr)
		jailed, err := f.keeper.GetServiceBondState(f.ctx, operator)
		require.NoError(t, err)
		require.Equal(t, index+1, jailed.JailCount)
		require.Equal(t, uint32(1), onlyRoleFaultForTask(t, f, next.TaskId).JailDelta)
	}
	tombstoned, err := f.keeper.GetServiceBondState(f.ctx, operator)
	require.NoError(t, err)
	require.Equal(t, params.Service.TombstoneJailCountThreshold, tombstoned.JailCount)
	require.Equal(t, types.ServiceBondStatusTombstoned, tombstoned.Status)
	require.NoError(t, f.keeper.EnsureSlashSummaryInvariant(f.ctx))
}

func onlyRoleFaultForTask(t *testing.T, f *fixture, taskID []byte) types.RoleFaultState {
	t.Helper()
	iter, err := f.keeper.RoleFault.Iterate(f.ctx, nil)
	require.NoError(t, err)
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		stored, err := iter.Value()
		require.NoError(t, err)
		state, err := f.keeper.ProjectRoleFaultStore(stored)
		require.NoError(t, err)
		if bytes.Equal(state.TaskId, taskID) {
			return state
		}
	}
	t.Fatal("role fault not found")
	return types.RoleFaultState{}
}

// TestSettlementVerifierFaultKeepsTaskClassificationSource pins the Task->Hub
// classification authority.
//
// A verifier deadline omission is discoverable two ways: the deadline sweep and
// a Tx-driven settlement that freezes its facts at the cutoff. Hub's own
// classifyRoleFaultType maps VERIFIER_MISS and COMMIT_NO_RESULT to DEADLINE, so
// a Hub that re-derives the source from the fault type alone stamps DEADLINE
// onto a fault whose Task-side TaskFailureClassState authority says SETTLEMENT.
// Nothing rejects that at write time — App.ensureRoleFaultReferences only
// compares the two ends on the next export or restart, so the chain would fail
// to export a genesis it could not re-import.
// TaskRoleFaultFact.ClassificationSource carries the Task verdict verbatim;
// this test fails if that stops being true.
func TestSettlementVerifierFaultKeepsTaskClassificationSource(t *testing.T) {
	f := initCandidatePoolFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	identity := hubIdentity(t, 236)
	operator := identity.Address
	registerCortexNodeIdentityForTest(t, f, operator, identity, testServiceBondMinInitial, 2, 1)
	activateServiceBondForTest(t, f, operator, 1)
	capabilityVersion := declareTaskLiabilitySupportForTest(
		t, f, operator, types.ServiceBondRoleVerifier, "model-settlement-source", 1, testServiceBondMinInitial, 3,
	)
	taskID := hubHash("settlement-verifier-source")
	reservation := seedOwnedTaskLiabilityForTest(t, f, taskID, operator, shared.DutyVerifier, capabilityVersion, 4)
	evidenceDigest := hubHashBytes("settlement-verifier-evidence")

	// Exactly what applyVerifierDeadlineFaults hands over when the settlement
	// transition — not the deadline sweep — froze the classification.
	_, settlementFaultErr := f.keeper.ApplyTaskRoleFault(f.ctx, types.TaskRoleFaultFact{
		SessionID: hubHashBytes("settlement-verifier-session"), TaskID: reservation.TaskId,
		OperatorAddress: operator, Duty: shared.DutyVerifier, FaultType: types.FaultTypeVerifierMiss,
		ClassificationSource: shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_SETTLEMENT,
		EvidenceDigest:       evidenceDigest, SlashBps: 2_500, Height: 40,
	})
	require.NoError(t, settlementFaultErr)

	fault := onlyRoleFaultForTask(t, f, reservation.TaskId)
	require.Equal(t,
		shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_SETTLEMENT,
		fault.ClassificationSource,
		"Hub must store the Task verdict, not re-derive DEADLINE from the fault type",
	)
	require.Equal(t, evidenceDigest, fault.EvidenceDigest)

	// The same fact recorded by the deadline sweep is a different fault, not a
	// replay of this one: source is part of the fault identity.
	_, deadlineFaultErr := f.keeper.ApplyTaskRoleFault(f.ctx, types.TaskRoleFaultFact{
		SessionID: hubHashBytes("settlement-verifier-session"), TaskID: reservation.TaskId,
		OperatorAddress: operator, Duty: shared.DutyVerifier, FaultType: types.FaultTypeVerifierMiss,
		ClassificationSource: shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_DEADLINE,
		EvidenceDigest:       evidenceDigest, SlashBps: 2_500, Height: 40,
	})
	require.Error(t, deadlineFaultErr)
	require.Equal(t,
		shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_SETTLEMENT,
		onlyRoleFaultForTask(t, f, reservation.TaskId).ClassificationSource,
	)

	// A VERIFICATION_ROUND-sourced verifier miss has no Task authority that could have
	// produced it and must be refused outright.
	_, challengeFaultErr := f.keeper.ApplyTaskRoleFault(f.ctx, types.TaskRoleFaultFact{
		SessionID: hubHashBytes("settlement-verifier-session-2"), TaskID: reservation.TaskId,
		OperatorAddress: operator, Duty: shared.DutyVerifier, FaultType: types.FaultTypeVerifierMiss,
		ClassificationSource: shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_VERIFICATION_ROUND,
		EvidenceDigest:       evidenceDigest, SlashBps: 2_500, Height: 41,
	})
	require.ErrorContains(t, challengeFaultErr, "cannot use classification_source")

	// The slash this fault drove must be visible to the runtime Summary
	// invariant, i.e. the SETTLEMENT-sourced path writes the same
	// RoleFault<->SlashSummary pair the DEADLINE-sourced path does.
	require.Len(t, fault.GetSlashSummaryId(), 32)
	summary, err := f.keeper.ReadSlashSummaryValue(f.ctx, types.NewSlashSummaryKey(
		types.SlashSourceKind_SLASH_SOURCE_KIND_ROLE_FAULT, fault.FaultId, 0,
	))
	require.NoError(t, err)
	require.Equal(t, fault.FaultId, summary.SourceId)
	require.Equal(t, types.SlashDestination_SLASH_DESTINATION_TREASURY, summary.Destination)
	require.Equal(t, shared.DutyVerifier, summary.Duty)
	require.NoError(t, f.keeper.EnsureSlashSummaryInvariant(f.ctx))
	// The Genesis round-trip of the RoleFault/SlashSummary pair itself is
	// covered by TestServiceSlashWaterfallMovesCustodyWritesSummaryAndTerminatesUnbonding;
	// this fixture seeds a candidate pool header whose snapshot_id is not the
	// canonical (chain_id, epoch, pool_hash) derivation, so exporting here would
	// test the fixture rather than the boundary.
}
