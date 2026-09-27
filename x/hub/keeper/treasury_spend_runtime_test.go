package keeper_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

type governanceReplayCheckerStub struct {
	reExecutable map[string]bool
	calls        []types.TreasurySpendReceiptKeyPair
	err          error
}

func (s *governanceReplayCheckerStub) IsGovernanceActionReExecutable(_ context.Context, proposalID uint64, itemIndex uint32) (bool, error) {
	key := types.NewTreasurySpendReceiptKey(proposalID, itemIndex)
	s.calls = append(s.calls, key)
	return s.reExecutable[treasuryReceiptLocator(proposalID, itemIndex)], s.err
}

func treasuryReceiptLocator(proposalID uint64, itemIndex uint32) string {
	return fmt.Sprintf("%d/%d", proposalID, itemIndex)
}

func treasurySpendFixture(t *testing.T, height uint64) (*fixture, *governanceReplayCheckerStub) {
	t.Helper()
	f := initFixture(t)
	sdkCtx := sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(height)).WithChainID("trueopen-treasury-runtime-test")
	f.ctx = sdk.WrapSDKContext(sdkCtx)
	checker := &governanceReplayCheckerStub{reExecutable: map[string]bool{}}
	f.keeper = f.keeper.WithGovernanceActionReplayChecker(checker)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	return f, checker
}

func acceptedTreasuryContext(f *fixture, proposalID uint64, itemIndex uint32) keeper.AcceptedGovernanceActionContext {
	return keeper.AcceptedGovernanceActionContext{
		AuthorityAddress: sdk.AccAddress(f.keeper.GetAuthority()).String(),
		ProposalID:       proposalID, ItemIndex: itemIndex, Accepted: true,
	}
}

func treasurySpendAction(t *testing.T, proposalID uint64, itemIndex uint32, amount uint64) types.ExecuteTreasurySpendV1 {
	t.Helper()
	return types.ExecuteTreasurySpendV1{
		ProposalId: proposalID, ItemIndex: itemIndex, Recipient: hubAddress(t, 211),
		Amount: shared.NewAmount(amount), PurposeCode: types.TreasuryPurposeCode_TREASURY_PURPOSE_CODE_INFRASTRUCTURE,
		NotBeforeHeight: 5, ExpiryHeight: 30,
	}
}

func seedTreasury(t *testing.T, f *fixture, amount, version uint64) {
	t.Helper()
	require.NoError(t, f.keeper.Treasury.Set(f.ctx, types.TreasuryState{Balance: shared.NewAmount(amount), TreasuryVersion: version}))
	f.bank.seedModule(types.TreasuryModuleName, amount)
}

func TestExecuteTreasurySpendV1AppliesAndReplaysExactly(t *testing.T) {
	f, _ := treasurySpendFixture(t, 10)
	seedTreasury(t, f, 100, 1)
	action := treasurySpendAction(t, 41, 2, 40)
	execution := acceptedTreasuryContext(f, action.ProposalId, action.ItemIndex)

	result, err := f.keeper.ExecuteTreasurySpendV1(f.ctx, execution, action)
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, result.Status)
	require.Len(t, result.Receipt.ActionDigest, 32)
	require.Equal(t, "ddddd5b2a8f8788b1472d56eabc545b803c12c61d365f286932fc61cf9ec9a36", fmt.Sprintf("%x", result.Receipt.ActionDigest))
	require.Equal(t, uint64(2), result.Receipt.TreasuryVersionAfter)
	_, epochEnd, err := shared.EpochHeightRange(0, types.DefaultHubParams().Epoch.EpochLengthBlocks)
	require.NoError(t, err)
	require.Equal(t, epochEnd+types.DefaultHubParams().Treasury.GovernanceActionReplayWindowBlocks, result.Receipt.PruneHeight)

	state, err := f.keeper.Treasury.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, shared.NewAmount(60), state.Balance)
	require.Equal(t, uint64(2), state.TreasuryVersion)
	require.Equal(t, uint64(60), f.bank.moduleBalance(types.TreasuryModuleName))
	require.Equal(t, uint64(40), f.bank.accountBalance(action.Recipient))
	require.True(t, hasEventType(sdk.UnwrapSDKContext(f.ctx).EventManager().Events(), "hub.v1.EventTreasuryAdjusted"))
	eventCount := len(sdk.UnwrapSDKContext(f.ctx).EventManager().Events())

	replayCtx := sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(31)
	f.ctx = sdk.WrapSDKContext(replayCtx)
	replay, err := f.keeper.ExecuteTreasurySpendV1(f.ctx, execution, action)
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, replay.Status)
	require.Equal(t, result.Receipt, replay.Receipt)
	require.Equal(t, uint64(60), f.bank.moduleBalance(types.TreasuryModuleName))
	require.Equal(t, uint64(40), f.bank.accountBalance(action.Recipient))
	require.Len(t, sdk.UnwrapSDKContext(f.ctx).EventManager().Events(), eventCount)

	conflict := action
	conflict.Amount = shared.NewAmount(39)
	_, err = f.keeper.ExecuteTreasurySpendV1(f.ctx, execution, conflict)
	require.ErrorContains(t, err, "conflicts")
}

func TestExecuteTreasurySpendV1RejectsUntrustedContextAndInvalidAction(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*keeper.AcceptedGovernanceActionContext, *types.ExecuteTreasurySpendV1)
	}{
		{name: "not accepted", mutate: func(c *keeper.AcceptedGovernanceActionContext, _ *types.ExecuteTreasurySpendV1) { c.Accepted = false }},
		{name: "wrong authority", mutate: func(c *keeper.AcceptedGovernanceActionContext, _ *types.ExecuteTreasurySpendV1) {
			c.AuthorityAddress = hubAddress(t, 212)
		}},
		{name: "locator mismatch", mutate: func(c *keeper.AcceptedGovernanceActionContext, _ *types.ExecuteTreasurySpendV1) { c.ItemIndex++ }},
		{name: "zero amount", mutate: func(_ *keeper.AcceptedGovernanceActionContext, a *types.ExecuteTreasurySpendV1) {
			a.Amount = shared.NewAmount(0)
		}},
		{name: "unset purpose", mutate: func(_ *keeper.AcceptedGovernanceActionContext, a *types.ExecuteTreasurySpendV1) {
			a.PurposeCode = types.TreasuryPurposeCode_TREASURY_PURPOSE_CODE_UNSPECIFIED
		}},
		{name: "invalid window", mutate: func(_ *keeper.AcceptedGovernanceActionContext, a *types.ExecuteTreasurySpendV1) {
			a.NotBeforeHeight, a.ExpiryHeight = 12, 11
		}},
		{name: "self recipient", mutate: func(_ *keeper.AcceptedGovernanceActionContext, a *types.ExecuteTreasurySpendV1) {
			a.Recipient = authtypes.NewModuleAddress(types.TreasuryModuleName).String()
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f, _ := treasurySpendFixture(t, 10)
			seedTreasury(t, f, 100, 1)
			action := treasurySpendAction(t, 7, 0, 10)
			execution := acceptedTreasuryContext(f, action.ProposalId, action.ItemIndex)
			test.mutate(&execution, &action)
			_, err := f.keeper.ExecuteTreasurySpendV1(f.ctx, execution, action)
			require.Error(t, err)
			state, getErr := f.keeper.Treasury.Get(f.ctx)
			require.NoError(t, getErr)
			require.Equal(t, shared.NewAmount(100), state.Balance)
			has, hasErr := f.keeper.TreasurySpendReceipt.Has(f.ctx, types.NewTreasurySpendReceiptKey(7, 0))
			require.NoError(t, hasErr)
			require.False(t, has)
			require.Equal(t, uint64(100), f.bank.moduleBalance(types.TreasuryModuleName))
		})
	}
}

func TestExecuteTreasurySpendV1InsufficientOrMismatchedBalanceWritesNothing(t *testing.T) {
	tests := []struct{ logical, bank uint64 }{{30, 30}, {100, 90}}
	for _, test := range tests {
		f, _ := treasurySpendFixture(t, 10)
		require.NoError(t, f.keeper.Treasury.Set(f.ctx, types.TreasuryState{Balance: shared.NewAmount(test.logical), TreasuryVersion: 1}))
		f.bank.seedModule(types.TreasuryModuleName, test.bank)
		action := treasurySpendAction(t, 8, 1, 40)
		_, err := f.keeper.ExecuteTreasurySpendV1(f.ctx, acceptedTreasuryContext(f, 8, 1), action)
		require.Error(t, err)
		state, getErr := f.keeper.Treasury.Get(f.ctx)
		require.NoError(t, getErr)
		require.Equal(t, shared.NewAmount(test.logical), state.Balance)
		has, hasErr := f.keeper.TreasurySpendReceipt.Has(f.ctx, types.NewTreasurySpendReceiptKey(8, 1))
		require.NoError(t, hasErr)
		require.False(t, has)
		require.Zero(t, f.bank.accountBalance(action.Recipient))
		require.Empty(t, sdk.UnwrapSDKContext(f.ctx).EventManager().Events())
	}
}

func TestTreasurySpendCapsAcceptExactAndRejectNextAtomically(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*types.HubParamsV2)
		second    func(*types.ExecuteTreasurySpendV1)
	}{
		{name: "proposal", configure: func(p *types.HubParamsV2) { p.Treasury.TreasurySpendLimitPerProposal = shared.NewAmount(10) }, second: func(a *types.ExecuteTreasurySpendV1) { a.Recipient = hubAddress(t, 212) }},
		{name: "epoch", configure: func(p *types.HubParamsV2) { p.Treasury.TreasurySpendLimitPerEpoch = shared.NewAmount(10) }, second: func(a *types.ExecuteTreasurySpendV1) {
			a.ProposalId++
			a.Recipient = hubAddress(t, 212)
		}},
		{name: "recipient", configure: func(p *types.HubParamsV2) { p.Treasury.TreasurySpendLimitPerRecipient = shared.NewAmount(10) }, second: func(a *types.ExecuteTreasurySpendV1) { a.ProposalId++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f, _ := treasurySpendFixture(t, 10)
			params := types.DefaultHubParams()
			test.configure(&params)
			require.NoError(t, f.keeper.Params.Set(f.ctx, params))
			seedTreasury(t, f, 100, 1)
			first := treasurySpendAction(t, 90, 0, 10)
			_, err := f.keeper.ExecuteTreasurySpendV1(f.ctx, acceptedTreasuryContext(f, first.ProposalId, first.ItemIndex), first)
			require.NoError(t, err)

			second := treasurySpendAction(t, 90, 1, 1)
			test.second(&second)
			_, err = f.keeper.ExecuteTreasurySpendV1(f.ctx, acceptedTreasuryContext(f, second.ProposalId, second.ItemIndex), second)
			require.ErrorContains(t, err, "treasury "+test.name+" spend cap exceeded")
			has, getErr := f.keeper.TreasurySpendReceipt.Has(f.ctx, types.NewTreasurySpendReceiptKey(second.ProposalId, second.ItemIndex))
			require.NoError(t, getErr)
			require.False(t, has)
			require.Equal(t, uint64(90), f.bank.moduleBalance(types.TreasuryModuleName))
		})
	}
}

func TestTreasuryEpochCleanupHeightStaysFrozenAcrossReplayWindowChange(t *testing.T) {
	f, _ := treasurySpendFixture(t, 10)
	params := types.DefaultHubParams()
	params.Epoch.EpochLengthBlocks, params.Epoch.DeltaWBlocks, params.Epoch.DeltaMBlocks = 10, 1, 2
	params.Treasury.GovernanceActionReplayWindowBlocks = 5
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	seedTreasury(t, f, 100, 1)
	first := treasurySpendAction(t, 100, 0, 10)
	first.ExpiryHeight = 50
	firstResult, err := f.keeper.ExecuteTreasurySpendV1(f.ctx, acceptedTreasuryContext(f, 100, 0), first)
	require.NoError(t, err)
	require.Equal(t, uint64(24), firstResult.Receipt.PruneHeight)

	params.Treasury.GovernanceActionReplayWindowBlocks = 20
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(11))
	second := treasurySpendAction(t, 101, 0, 10)
	second.ExpiryHeight = 50
	secondResult, err := f.keeper.ExecuteTreasurySpendV1(f.ctx, acceptedTreasuryContext(f, 101, 0), second)
	require.NoError(t, err)
	epoch, err := f.keeper.TreasurySpendEpoch.Get(f.ctx, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(24), epoch.CleanupHeight, "the first epoch spend freezes cleanup")
	require.Equal(t, uint64(31), secondResult.Receipt.PruneHeight, "new receipts use the current replay window")
}

func TestTreasuryCleanupCursorGenesisRoundTrip(t *testing.T) {
	f, _ := treasurySpendFixture(t, 10)
	params := types.DefaultHubParams()
	params.Epoch.EpochLengthBlocks, params.Epoch.DeltaWBlocks, params.Epoch.DeltaMBlocks = 10, 1, 2
	params.Treasury.GovernanceActionReplayWindowBlocks = 5
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	seedTreasury(t, f, 100, 1)
	for item := uint32(0); item < 2; item++ {
		action := treasurySpendAction(t, 110, item, 10)
		action.Recipient = hubAddress(t, byte(220+item))
		_, err := f.keeper.ExecuteTreasurySpendV1(f.ctx, acceptedTreasuryContext(f, 110, item), action)
		require.NoError(t, err)
	}
	rows, err := f.keeper.TreasurySpendRecipientEpoch.Iterate(f.ctx, nil)
	require.NoError(t, err)
	require.True(t, rows.Valid())
	recipientKey, err := rows.Key()
	require.NoError(t, err)
	storedRecipient, err := rows.Value()
	require.NoError(t, err)
	encodedRecipient, err := storedRecipient.Marshal()
	require.NoError(t, err)
	require.False(t, bytes.Contains(encodedRecipient, []byte(sdk.AccAddress(recipientKey.K2()).String())))
	require.True(t, bytes.Contains(encodedRecipient, recipientKey.K2()))
	require.NoError(t, rows.Close())
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(24))
	visited, err := f.keeper.ProcessTreasurySpendEpochCleanups(f.ctx, 24, 2)
	require.NoError(t, err)
	require.Equal(t, uint64(2), visited)

	genesis, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Len(t, genesis.TreasurySpendEpochCleanupCursors, 1)
	storedCursor, err := f.keeper.TreasurySpendEpochCleanupCursor.Get(f.ctx, genesis.TreasurySpendEpochCleanupCursors[0].RewardEpoch)
	require.NoError(t, err)
	encodedCursor, err := storedCursor.Marshal()
	require.NoError(t, err)
	lastRecipient := genesis.TreasurySpendEpochCleanupCursors[0].GetLastRecipientAddress()
	require.False(t, bytes.Contains(encodedCursor, []byte(lastRecipient)))
	require.True(t, bytes.Contains(encodedCursor, hubAddressBytes(t, lastRecipient)))
	require.Len(t, genesis.TreasurySpendRecipientEpochs, 1)
	restarted := initFixture(t)
	restarted.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(restarted.ctx).WithChainID(sdk.UnwrapSDKContext(f.ctx).ChainID()))
	restarted.bank.seedModule(types.TreasuryModuleName, 80)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *genesis))
	require.NoError(t, restarted.keeper.EnsureTreasurySpendReceiptInvariant(restarted.ctx))
}

func TestExecuteTreasurySpendV1BankFailureRollsBackStateReceiptAndEvent(t *testing.T) {
	f, _ := treasurySpendFixture(t, 10)
	seedTreasury(t, f, 100, 1)
	f.bank.sendModuleToAccountError = errors.New("injected bank send failure")
	action := treasurySpendAction(t, 81, 4, 40)
	_, err := f.keeper.ExecuteTreasurySpendV1(f.ctx, acceptedTreasuryContext(f, 81, 4), action)
	require.ErrorContains(t, err, "injected bank send failure")
	state, getErr := f.keeper.Treasury.Get(f.ctx)
	require.NoError(t, getErr)
	require.Equal(t, types.TreasuryState{Balance: shared.NewAmount(100), TreasuryVersion: 1}, state)
	has, hasErr := f.keeper.TreasurySpendReceipt.Has(f.ctx, types.NewTreasurySpendReceiptKey(81, 4))
	require.NoError(t, hasErr)
	require.False(t, has)
	_, epochEnd, rangeErr := shared.EpochHeightRange(0, types.DefaultHubParams().Epoch.EpochLengthBlocks)
	require.NoError(t, rangeErr)
	has, hasErr = f.keeper.TreasurySpendReceiptPruneIndex.Has(f.ctx, types.NewTreasurySpendReceiptPruneKey(epochEnd+types.DefaultHubParams().Treasury.GovernanceActionReplayWindowBlocks, 81, 4))
	require.NoError(t, hasErr)
	require.False(t, has)
	require.Empty(t, sdk.UnwrapSDKContext(f.ctx).EventManager().Events())
}

func TestTreasurySpendReceiptPruneDeletesOrReschedulesUnderVisitedCap(t *testing.T) {
	f, checker := treasurySpendFixture(t, 10)
	params := types.DefaultHubParams()
	params.Treasury.GovernanceActionReplayWindowBlocks = 5
	params.Epoch.EpochLengthBlocks, params.Epoch.DeltaWBlocks, params.Epoch.DeltaMBlocks = 10, 1, 2
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	seedTreasury(t, f, 100, 1)
	for item := uint32(0); item < 2; item++ {
		action := treasurySpendAction(t, 9, item, 10)
		_, err := f.keeper.ExecuteTreasurySpendV1(f.ctx, acceptedTreasuryContext(f, 9, item), action)
		require.NoError(t, err)
	}
	checker.reExecutable[treasuryReceiptLocator(9, 0)] = true

	pruneCtx := sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(24)
	f.ctx = sdk.WrapSDKContext(pruneCtx)
	visited, err := f.keeper.ProcessTreasurySpendEpochCleanups(f.ctx, 24, 3)
	require.NoError(t, err)
	require.Equal(t, uint64(3), visited)
	visited, err = f.keeper.ProcessTreasurySpendReceiptPrunes(f.ctx, 24, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	first, err := f.keeper.TreasurySpendReceipt.Get(f.ctx, types.NewTreasurySpendReceiptKey(9, 0))
	require.NoError(t, err)
	require.Equal(t, uint64(29), first.PruneHeight)
	has, err := f.keeper.TreasurySpendReceiptPruneIndex.Has(f.ctx, types.NewTreasurySpendReceiptPruneKey(24, 9, 0))
	require.NoError(t, err)
	require.False(t, has)
	has, err = f.keeper.TreasurySpendReceiptPruneIndex.Has(f.ctx, types.NewTreasurySpendReceiptPruneKey(29, 9, 0))
	require.NoError(t, err)
	require.True(t, has)

	visited, err = f.keeper.ProcessTreasurySpendReceiptPrunes(f.ctx, 24, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	_, err = f.keeper.TreasurySpendReceipt.Get(f.ctx, types.NewTreasurySpendReceiptKey(9, 1))
	require.ErrorIs(t, err, collections.ErrNotFound)
	require.Len(t, checker.calls, 2)
}

func TestEndBlockerSchedulesTreasurySpendReceiptPrune(t *testing.T) {
	f, _ := treasurySpendFixture(t, 10)
	params := types.DefaultHubParams()
	params.Treasury.GovernanceActionReplayWindowBlocks = 5
	params.Epoch.EpochLengthBlocks, params.Epoch.DeltaWBlocks, params.Epoch.DeltaMBlocks = 10, 1, 2
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	seedTreasury(t, f, 100, 1)
	action := treasurySpendAction(t, 19, 0, 10)
	_, err := f.keeper.ExecuteTreasurySpendV1(f.ctx, acceptedTreasuryContext(f, 19, 0), action)
	require.NoError(t, err)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(24))
	require.NoError(t, f.keeper.EndBlocker(f.ctx))
	_, err = f.keeper.TreasurySpendReceipt.Get(f.ctx, types.NewTreasurySpendReceiptKey(19, 0))
	require.ErrorIs(t, err, collections.ErrNotFound)
	remaining, err := f.keeper.EndBlockBudgetRemainingItems.Get(f.ctx)
	require.NoError(t, err)
	require.Less(t, remaining, uint64(params.QueryEvent.MaxEndblockVisitedItemsTotal))
}

func TestTreasurySpendReceiptGenesisRoundTripAndTamperRejection(t *testing.T) {
	f, _ := treasurySpendFixture(t, 10)
	seedTreasury(t, f, 100, 1)
	action := treasurySpendAction(t, 17, 3, 25)
	_, err := f.keeper.ExecuteTreasurySpendV1(f.ctx, acceptedTreasuryContext(f, 17, 3), action)
	require.NoError(t, err)
	storedReceipt, err := f.keeper.TreasurySpendReceipt.Get(f.ctx, types.NewTreasurySpendReceiptKey(17, 3))
	require.NoError(t, err)
	encodedReceipt, err := storedReceipt.Marshal()
	require.NoError(t, err)
	require.False(t, bytes.Contains(encodedReceipt, []byte(action.Recipient)))
	require.True(t, bytes.Contains(encodedReceipt, hubAddressBytes(t, action.Recipient)))
	genesis, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Len(t, genesis.TreasurySpendReceipts, 1)
	require.Len(t, genesis.TreasurySpendProposals, 1)
	require.Len(t, genesis.TreasurySpendEpochs, 1)
	require.Len(t, genesis.TreasurySpendRecipientEpochs, 1)

	restarted := initFixture(t)
	restarted.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(restarted.ctx).WithChainID(sdk.UnwrapSDKContext(f.ctx).ChainID()))
	restarted.bank.seedModule(types.TreasuryModuleName, 75)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *genesis))
	exported, err := restarted.keeper.ExportGenesis(restarted.ctx)
	require.NoError(t, err)
	require.Equal(t, genesis.TreasurySpendReceipts, exported.TreasurySpendReceipts)

	missingIndex := types.NewTreasurySpendReceiptPruneKey(
		genesis.TreasurySpendReceipts[0].PruneHeight, genesis.TreasurySpendReceipts[0].ProposalId, genesis.TreasurySpendReceipts[0].ItemIndex,
	)
	require.NoError(t, restarted.keeper.TreasurySpendReceiptPruneIndex.Remove(restarted.ctx, missingIndex))
	_, err = restarted.keeper.ExportGenesis(restarted.ctx)
	require.ErrorContains(t, err, "missing its prune index")

	tampered := *genesis
	tampered.TreasurySpendReceipts = append([]types.TreasurySpendReceiptState(nil), genesis.TreasurySpendReceipts...)
	tampered.TreasurySpendReceipts[0].ActionDigest = make([]byte, 31)
	require.Error(t, tampered.Validate())
	tampered = *genesis
	tampered.TreasurySpendReceipts = append(append([]types.TreasurySpendReceiptState(nil), genesis.TreasurySpendReceipts...), genesis.TreasurySpendReceipts[0])
	require.Error(t, tampered.Validate())
	tampered = *genesis
	tampered.TreasurySpendProposals = append([]types.TreasurySpendProposalState(nil), genesis.TreasurySpendProposals...)
	tampered.TreasurySpendProposals[0].SpentAmount = shared.NewAmount(24)
	require.ErrorContains(t, tampered.Validate(), "does not match retained receipts")
}

func TestExecuteTreasurySpendV1IsNotPublicMsgRPC(t *testing.T) {
	server := grpc.NewServer()
	t.Cleanup(server.Stop)
	types.RegisterMsgServer(server, &types.UnimplementedMsgServer{})
	service, ok := server.GetServiceInfo()["hub.v1.Msg"]
	require.True(t, ok)
	for _, method := range service.Methods {
		require.NotEqual(t, "ExecuteTreasurySpendV1", method.Name)
	}
}
