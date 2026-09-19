package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// TestWithdrawFullySlashedUnbondingIsAppliedWithoutEvent pins the reporting
// contract for the one withdraw shape A-14 questions: a live unbonding row whose
// remaining amount is already zero because a slash consumed all of it.
//
// Such a row cannot be produced by the slash path - slashServiceUnbondingQueue
// (service_slash.go:620) terminates a row the moment slash_applied_amount
// reaches amount, so the FULLY_SLASHED receipt is written there and the primary
// disappears in the same transaction. UnbondingState.Validate
// (types/service_support.go:106) does accept the shape though, so a genesis
// import can carry one in, which is what this test reconstructs.
//
// The two facts asserted here are the ones an indexer depends on:
//
//   - no §5.11 code 103 event fires, because no funds moved. The emission is
//     gated on withdrawn_amount > 0 (service_bond_runtime.go:342).
//   - the response is still APPLIED with withdrawn_items == 1, because state
//     genuinely advanced: §10.0c withdraw step 6 deletes the row and writes the
//     exact-replay receipt unconditionally. NOOP is this module's "no write, no
//     event" answer (service_bond_runtime.go:277 for an empty batch, :220 for a
//     receipt replay); reporting it here would deny a store mutation the client
//     cannot repeat - the second attempt takes the receipt-replay branch and is
//     the request that legitimately answers NOOP.
//
// The trailing withdraw of a normal row gives the "no event" assertion its
// discriminating power: the same call shape on a row that does move funds must
// emit code 103.
func TestWithdrawFullySlashedUnbondingIsAppliedWithoutEvent(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	operator := hubIdentity(t, 211)
	serviceKey := hubIdentity(t, 212)
	server := keeper.NewMsgServerImpl(f.keeper)

	const registerHeight = uint64(100)
	registerCortexNodeIdentityForTest(t, f, operator.Address, serviceKey, 2*testServiceBondMinInitial, registerHeight, 0)
	activateServiceBondForTest(t, f, operator.Address, 0)

	// The paying row: a real unstake, so its id, indexes and accounting are the
	// ones the production path writes.
	paying, err := server.BeginServiceUnstake(heightCtx(f, registerHeight), &types.MsgBeginServiceUnstake{
		OperatorAddress: operator.Address, Amount: shared.NewAmount(testServiceBondMinInitial),
	})
	require.NoError(t, err)
	payingID := shared.Hash32Key(paying.UnbondingId)

	// The fully-slashed row, in the shape a genesis import can deliver: amount
	// fully covered by slash_applied_amount. pending_unbonding_total is
	// deliberately not raised for it - a slash that consumed the row already
	// debited that total, and invariants.go:946 sums the *unwithdrawn* remainder,
	// which is zero here.
	slashedRow := types.UnbondingState{
		UnbondingId:        hubHashBytes("fully-slashed-unbonding"),
		OperatorAddress:    operator.Address,
		Amount:             testServiceBondMinInitial,
		SlashAppliedAmount: testServiceBondMinInitial,
		RequestHeight:      registerHeight,
		MatureHeight:       paying.MatureHeight,
		Status:             types.UnbondingStatusOpen,
	}
	require.NoError(t, slashedRow.Validate())
	slashedID := shared.Hash32Key(slashedRow.UnbondingId)
	require.NoError(t, f.keeper.Unbonding.Set(f.ctx, types.NewUnbondingKey(operator.Address, slashedID), slashedRow))
	require.NoError(t, f.keeper.UnbondingMaturityIndex.Set(
		f.ctx, types.NewUnbondingMaturityIndexKey(slashedRow.MatureHeight, operator.Address, slashedID),
	))
	require.NoError(t, f.keeper.UnbondingByOperatorStatusIndex.Set(
		f.ctx, types.NewUnbondingByOperatorStatusKey(operator.Address, slashedRow.Status, slashedRow.MatureHeight, slashedID),
	))

	// Probing one block early first is what makes the height used below
	// load-bearing: without this, every withdraw in this test would still pass if
	// the maturity gate were deleted outright. The by-id path
	// (service_bond_runtime.go:205) is the only place that gate rejects -- the
	// batch path at :243 silently skips an immature row instead -- so this is the
	// single assertion standing between an operator and funds still locked.
	_, err = server.WithdrawServiceUnbonded(heightCtx(f, paying.MatureHeight-1), &types.MsgWithdrawServiceUnbonded{
		OperatorAddress: operator.Address,
		Locator: types.UnbondingLocatorV1{Locator: &types.UnbondingLocatorV1_ById{
			ById: &shared.ByIDV1{Id: paying.UnbondingId},
		}},
	})
	require.ErrorIs(t, err, types.ErrDeadlineNotReached)

	withdrawCtx := heightCtx(f, paying.MatureHeight)
	operatorBefore := f.bank.accountBalance(operator.Address)
	moduleBefore := f.bank.moduleBalance(types.ServiceBondModuleName)
	eventsBefore := len(hubEventsOfType(t, sdk.UnwrapSDKContext(f.ctx), &types.EventServiceUnbondingWithdrawn{}))

	slashedResponse, err := server.WithdrawServiceUnbonded(withdrawCtx, &types.MsgWithdrawServiceUnbonded{
		OperatorAddress: operator.Address,
		Locator: types.UnbondingLocatorV1{Locator: &types.UnbondingLocatorV1_ById{
			ById: &shared.ByIDV1{Id: slashedRow.UnbondingId},
		}},
	})
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, slashedResponse.Status,
		"the row was deleted and a receipt written, so the mutation did apply")
	require.Equal(t, shared.NewAmount(0), slashedResponse.WithdrawnAmount)
	require.Equal(t, uint32(1), slashedResponse.WithdrawnItems,
		"withdrawn_items counts rows retired, not rows that paid out")
	require.Equal(t, eventsBefore, len(hubEventsOfType(t, sdk.UnwrapSDKContext(f.ctx), &types.EventServiceUnbondingWithdrawn{})),
		"§5.11 code 103 reports a fund movement; a zero-amount withdraw must stay silent")
	require.Equal(t, operatorBefore, f.bank.accountBalance(operator.Address))
	require.Equal(t, moduleBefore, f.bank.moduleBalance(types.ServiceBondModuleName))

	// The row is retired through the same terminal writer as a paying withdraw,
	// which is why APPLIED is the honest answer.
	_, err = f.keeper.Unbonding.Get(f.ctx, types.NewUnbondingKey(operator.Address, slashedID))
	require.Error(t, err)
	receipt, err := f.keeper.UnbondingReceipt.Get(f.ctx, slashedID)
	require.NoError(t, err)
	require.Equal(t, types.UnbondingReceiptTerminalStatus_UNBONDING_RECEIPT_TERMINAL_STATUS_FULLY_SLASHED, receipt.TerminalStatus)
	require.Equal(t, testServiceBondMinInitial, receipt.OriginalAmount)
	require.Equal(t, testServiceBondMinInitial, receipt.SlashedAmount)
	require.Zero(t, receipt.WithdrawnAmount)

	// Replaying the very same request is the case the NOOP convention exists for:
	// nothing is written and nothing is emitted the second time.
	replay, err := server.WithdrawServiceUnbonded(withdrawCtx, &types.MsgWithdrawServiceUnbonded{
		OperatorAddress: operator.Address,
		Locator: types.UnbondingLocatorV1{Locator: &types.UnbondingLocatorV1_ById{
			ById: &shared.ByIDV1{Id: slashedRow.UnbondingId},
		}},
	})
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, replay.Status)
	require.Equal(t, shared.NewAmount(0), replay.WithdrawnAmount)
	require.Zero(t, replay.WithdrawnItems)

	// Discriminating control: an identical call on the paying row does emit code
	// 103, so the absence asserted above is a property of the zero amount and not
	// of the fixture never emitting the event at all.
	payingResponse, err := server.WithdrawServiceUnbonded(withdrawCtx, &types.MsgWithdrawServiceUnbonded{
		OperatorAddress: operator.Address,
		Locator: types.UnbondingLocatorV1{Locator: &types.UnbondingLocatorV1_ById{
			ById: &shared.ByIDV1{Id: paying.UnbondingId},
		}},
	})
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, payingResponse.Status)
	require.Equal(t, shared.NewAmount(testServiceBondMinInitial), payingResponse.WithdrawnAmount)
	require.Equal(t, uint32(1), payingResponse.WithdrawnItems)
	emitted := hubEventsOfType(t, sdk.UnwrapSDKContext(f.ctx), &types.EventServiceUnbondingWithdrawn{})
	require.Len(t, emitted, eventsBefore+1)
	withdrawnEvent, ok := emitted[len(emitted)-1].(*types.EventServiceUnbondingWithdrawn)
	require.True(t, ok)
	require.Equal(t, operator.Address, withdrawnEvent.Operator)
	require.Equal(t, shared.NewAmount(testServiceBondMinInitial), withdrawnEvent.WithdrawnAmount)
	require.Equal(t, uint32(1), withdrawnEvent.WithdrawnItems)

	payingReceipt, err := f.keeper.UnbondingReceipt.Get(f.ctx, payingID)
	require.NoError(t, err)
	require.Equal(t, types.UnbondingReceiptTerminalStatus_UNBONDING_RECEIPT_TERMINAL_STATUS_WITHDRAWN, payingReceipt.TerminalStatus)
	require.Equal(t, operatorBefore+testServiceBondMinInitial, f.bank.accountBalance(operator.Address))
}

// TestUnbondingSlashEmitsNoStakeChangedEvent guards the other half of A-14: an
// unbonding-queue slash must not report itself as a §5.11 code 6
// service_stake_changed.
//
// Code 6 is the stake/top-up fact, and the two producers left
// (emitServiceStakeChangedEventWithAmount) are both on the register/top-up path
// with a positive amount. A slash is code 52 role_slashed, emitted once by the
// fault path (fault_runtime.go:231) and gated on applied|unfilled > 0, so the
// slash primitive itself must stay event-free or the fault path would
// double-report. This test locks that: a slash deep enough to reach layer 2
// still emits nothing.
func TestUnbondingSlashEmitsNoStakeChangedEvent(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	operator := hubIdentity(t, 213)
	serviceKey := hubIdentity(t, 214)

	const registerHeight = uint64(100)
	registerCortexNodeIdentityForTest(t, f, operator.Address, serviceKey, 2*testServiceBondMinInitial, registerHeight, 0)
	activateServiceBondForTest(t, f, operator.Address, 0)
	_, err := keeper.NewMsgServerImpl(f.keeper).BeginServiceUnstake(
		heightCtx(f, registerHeight), &types.MsgBeginServiceUnstake{
			OperatorAddress: operator.Address, Amount: shared.NewAmount(testServiceBondMinInitial),
		},
	)
	require.NoError(t, err)

	stakeChangedBefore := len(hubEventsOfType(t, sdk.UnwrapSDKContext(f.ctx), &types.EventServiceStakeChanged{}))
	slashed, err := f.keeper.ApplyServiceSlash(f.ctx, serviceSlashRequestForTest(
		operator.Address, shared.DutyWorker, "unbonding-layer", testServiceBondMinInitial+100_000, registerHeight+10,
	))
	require.NoError(t, err)
	require.Equal(t, uint64(100_000), slashed.UnbondingApplied,
		"the slash must actually reach the unbonding queue for this test to mean anything")
	require.Equal(t, stakeChangedBefore, len(hubEventsOfType(t, sdk.UnwrapSDKContext(f.ctx), &types.EventServiceStakeChanged{})),
		"code 6 is the stake fact; a slash reports through code 52 instead")
}
