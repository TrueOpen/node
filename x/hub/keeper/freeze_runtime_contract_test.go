package keeper_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"testing"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

type deterministicFreezeFailureFeed struct {
	windowBlocks uint64
	paginated    bool
	lastLimit    uint32
}

func (f *deterministicFreezeFailureFeed) ScanFreezeSignalFailures(_ context.Context, request types.FreezeSignalFailureScanRequest) (types.FreezeSignalFailureScanResult, error) {
	f.lastLimit = request.Limit
	windowID := (request.RiskWindowStartHeight - 1) / f.windowBlocks
	fill := byte(windowID + 1)
	failure := types.FreezeSignalFailure{
		TaskID: bytes.Repeat([]byte{fill}, 32), SettlementID: bytes.Repeat([]byte{fill + 0x20}, 32),
		FinalityHeight: request.RiskWindowStartHeight, FailureClass: types.FreezeFailureClassMetricThresholdBreach,
		EvidenceDigest: bytes.Repeat([]byte{fill + 0x40}, 32), IncludedInRoot: true,
	}
	if f.paginated && len(request.LastIndexKey) == 0 {
		return types.FreezeSignalFailureScanResult{Failures: []types.FreezeSignalFailure{failure}, LastIndexKey: []byte{fill}, Visited: 1}, nil
	}
	if f.paginated {
		failure.TaskID = bytes.Repeat([]byte{fill + 1}, 32)
		failure.SettlementID = bytes.Repeat([]byte{fill + 0x21}, 32)
		failure.EvidenceDigest = bytes.Repeat([]byte{fill + 0x41}, 32)
	}
	return types.FreezeSignalFailureScanResult{Failures: []types.FreezeSignalFailure{failure}, LastIndexKey: []byte{fill + 1}, Visited: 1, Done: true}, nil
}

func TestSubmitFreezeSignalUsesPerTxBuildBudget(t *testing.T) {
	f := newFreezeRuntimeFixture(t, 20, 20, 40, 3, nil)
	params, err := f.fixture.keeper.Params.Get(f.contextAt(101))
	require.NoError(t, err)
	params.Freeze.MaxFreezeSignalBuildItemsPerBlock = 100
	params.Freeze.MaxFreezeSignalBuildItemsPerTx = 3
	require.NoError(t, f.fixture.keeper.Params.Set(f.contextAt(101), params))

	_ = f.submit(t, 101)
	require.Equal(t, uint32(3), f.feed.lastLimit)
}

type deterministicValidatorSnapshots struct {
	total             uint64
	members           map[string]types.ValidatorSnapshotMember
	historicalEntries uint32
	captureErr        error
}

func (p deterministicValidatorSnapshots) HistoricalEntries(context.Context) (uint32, error) {
	return p.historicalEntries, nil
}

func (p deterministicValidatorSnapshots) CaptureValidatorSetSnapshot(_ sdk.Context, height uint64) (types.ValidatorSnapshot, error) {
	if p.captureErr != nil {
		return types.ValidatorSnapshot{}, p.captureErr
	}
	return types.ValidatorSnapshot{Height: height, ValidatorSetHash: bytes.Repeat([]byte{0xa5}, 32), TotalVotingPower: p.total}, nil
}

func (p deterministicValidatorSnapshots) GetValidatorSnapshotMemberBySigner(_ sdk.Context, snapshot types.ValidatorSnapshot, signer string) (types.ValidatorSnapshotMember, bool, error) {
	if snapshot.TotalVotingPower != p.total || !bytes.Equal(snapshot.ValidatorSetHash, bytes.Repeat([]byte{0xa5}, 32)) {
		return types.ValidatorSnapshotMember{}, false, types.ErrInvariantBroken
	}
	member, found := p.members[signer]
	return member, found, nil
}

type freezeRuntimeFixture struct {
	fixture   *fixture
	feed      *deterministicFreezeFailureFeed
	snapshots deterministicValidatorSnapshots
	msg       types.MsgServer
	submitter string
}

const freezeRuntimeChainID = "trueopen-freeze-test"

func newFreezeRuntimeFixture(t *testing.T, voteWindow, detailRetention, summaryRetention uint64, totalPower uint64, members map[string]types.ValidatorSnapshotMember) *freezeRuntimeFixture {
	return newFreezeRuntimeFixtureWithThreshold(t, voteWindow, detailRetention, summaryRetention, 1, totalPower, members)
}

func newFreezeRuntimeFixtureWithThreshold(t *testing.T, voteWindow, detailRetention, summaryRetention uint64, failureThreshold uint32, totalPower uint64, members map[string]types.ValidatorSnapshotMember) *freezeRuntimeFixture {
	t.Helper()
	f := initFixture(t)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithChainID(freezeRuntimeChainID))
	genesis := hubGenesisWithIndexesForChain(freezeRuntimeChainID)
	genesis.Params.Freeze.FreezeSignalVoteWindowBlocks = voteWindow
	genesis.Params.Freeze.FreezeSignalDetailRetentionBlocks = detailRetention
	genesis.Params.Freeze.FreezeSignalSummaryRetentionBlocks = summaryRetention
	genesis.Params.Freeze.MinFreezeSignalFailureCount = failureThreshold
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	feed := &deterministicFreezeFailureFeed{windowBlocks: genesis.Params.Freeze.FreezeRiskWindowBlocks}
	snapshots := deterministicValidatorSnapshots{total: totalPower, members: members, historicalEntries: 10_000}
	f.keeper = f.keeper.WithFreezeRuntimeDependencies(feed, snapshots)
	dependencies := types.MsgServerDependencies{FreezeTaskValidator: feed, ValidatorSnapshotProvider: snapshots}
	return &freezeRuntimeFixture{
		fixture: f, feed: feed, snapshots: snapshots,
		msg: keeper.NewMsgServerImpl(f.keeper, dependencies), submitter: hubAddress(t, 231),
	}
}

func TestFreezeSignalBelowThresholdAdvancesWaterlineWithoutSignalOrEvent(t *testing.T) {
	f := newFreezeRuntimeFixtureWithThreshold(t, 20, 20, 40, 2, 3, nil)
	ctx := f.contextAt(101)
	before := len(sdk.UnwrapSDKContext(ctx).EventManager().Events())

	response := f.submit(t, 101)
	require.Equal(t, types.FreezeSignalBuildStatus_FREEZE_SIGNAL_BUILD_STATUS_BELOW_THRESHOLD_NOOP, response.BuildStatus)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, response.Status)
	require.Empty(t, response.GetFreezeSignalId())
	require.Len(t, sdk.UnwrapSDKContext(ctx).EventManager().Events(), before)

	profile, err := f.fixture.keeper.GetProfile(ctx, "model-a", 1)
	require.NoError(t, err)
	require.NotNil(t, profile.XLastFreezeRiskWindowEvaluated)
	require.Equal(t, uint64(0), profile.GetLastFreezeRiskWindowEvaluated())
	_, err = f.fixture.keeper.FreezeSignalBuildCursor.Get(ctx, types.NewFreezeSignalBuildCursorKey("model-a", 1))
	require.ErrorIs(t, err, collections.ErrNotFound)
	_, err = f.fixture.keeper.FreezeSignalByWindow.Get(ctx, types.NewFreezeSignalByWindowKey("model-a", 1, 0))
	require.ErrorIs(t, err, collections.ErrNotFound)
	iter, err := f.fixture.keeper.FreezeSignalState.Iterate(ctx, nil)
	require.NoError(t, err)
	defer iter.Close()
	require.False(t, iter.Valid())
}

func TestNewProfileStartsAtLatestClosedFreezeRiskWindow(t *testing.T) {
	f := initFixture(t)
	genesis := types.DefaultGenesis()
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	windowBlocks := genesis.Params.Freeze.FreezeRiskWindowBlocks
	height := uint64(5_000_000)
	registerTestModelProfile(t, f, "late-freeze-profile", 1, testServiceBondMinInitial, height)

	profile, err := f.keeper.GetProfile(f.ctx, "late-freeze-profile", 1)
	require.NoError(t, err)
	require.NotNil(t, profile.XLastFreezeRiskWindowEvaluated)
	latestClosed := (height-1)/windowBlocks - 1
	require.Equal(t, latestClosed, profile.GetLastFreezeRiskWindowEvaluated())
	nextDueHeight := (latestClosed+2)*windowBlocks + 1
	has, err := f.keeper.FreezeRiskWindowScheduleIndex.Has(
		f.ctx, types.NewFreezeRiskWindowScheduleKey(nextDueHeight, profile.ModelId, profile.ProfileVersion),
	)
	require.NoError(t, err)
	require.True(t, has)
}

func TestFreezeFailureRollingRootHasStableVectorAndFieldSeparation(t *testing.T) {
	f := newFreezeRuntimeFixture(t, 20, 20, 40, 3, nil)
	opened := f.submit(t, 101)
	require.Equal(t, types.FreezeSignalBuildStatus_FREEZE_SIGNAL_BUILD_STATUS_OPEN, opened.BuildStatus)
	signal, err := f.fixture.keeper.FreezeSignalState.Get(f.contextAt(101), opened.GetFreezeSignalId())
	require.NoError(t, err)
	require.Equal(t, "588d935ac5e32836570190b66020fff014113b02c6d13006b56288069434889c", hex.EncodeToString(signal.IncludedFailureTaskRefsHash))

	fields := [][]byte{
		make([]byte, 32), bytes.Repeat([]byte{0x01}, 32), bytes.Repeat([]byte{0x21}, 32),
		shared.Uint64BE(1), shared.EnumBE(uint32(types.FreezeFailureClassMetricThresholdBreach)), bytes.Repeat([]byte{0x41}, 32),
	}
	require.Equal(t, signal.IncludedFailureTaskRefsHash, shared.CanonicalHashBytes(shared.MustDomain(shared.DomainFreezeFailureRefsV1), fields...))
	for index := range fields {
		mutated := make([][]byte, len(fields))
		for fieldIndex := range fields {
			mutated[fieldIndex] = append([]byte(nil), fields[fieldIndex]...)
		}
		mutated[index][0] ^= 0x80
		require.NotEqual(t, signal.IncludedFailureTaskRefsHash, shared.CanonicalHashBytes(shared.MustDomain(shared.DomainFreezeFailureRefsV1), mutated...), "field %d did not affect the root", index)
	}
}

func (f *freezeRuntimeFixture) contextAt(height int64) context.Context {
	sdkCtx := sdk.UnwrapSDKContext(f.fixture.ctx).WithChainID(freezeRuntimeChainID).WithBlockHeight(height)
	return sdk.WrapSDKContext(sdkCtx)
}

func (f *freezeRuntimeFixture) submit(t *testing.T, height int64) *types.MsgSubmitFreezeSignalResponse {
	t.Helper()
	response, err := f.msg.SubmitFreezeSignal(f.contextAt(height), &types.MsgSubmitFreezeSignal{
		ModelId: "model-a", ProfileVersion: 1, SubmitterAddress: f.submitter,
	})
	require.NoError(t, err)
	return response
}

func TestFreezeSignalAcceptsAtQuorumAndPreservesExactReplay(t *testing.T) {
	validator := hubAddress(t, 232)
	consensusAddress := bytes.Repeat([]byte{0x11}, 20)
	f := newFreezeRuntimeFixture(t, 100, 20, 40, 3, map[string]types.ValidatorSnapshotMember{
		validator: {SignerAddress: validator, ConsensusAddress: consensusAddress, VotingPower: 2},
	})
	opened := f.submit(t, 101)
	require.Equal(t, types.FreezeSignalBuildStatus_FREEZE_SIGNAL_BUILD_STATUS_OPEN, opened.BuildStatus)
	require.Len(t, opened.GetFreezeSignalId(), 32)

	vote := &types.MsgEmergencyFreezeVote{FreezeSignalId: opened.GetFreezeSignalId(), Vote: types.EmergencyFreezeVote_EMERGENCY_FREEZE_VOTE_ACCEPT, ValidatorAddress: validator}
	voteCtx := f.contextAt(101)
	eventStart := len(sdk.UnwrapSDKContext(voteCtx).EventManager().Events())
	accepted, err := f.msg.EmergencyFreezeVote(voteCtx, vote)
	require.NoError(t, err)
	require.Equal(t, types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_ACCEPTED, accepted.SignalStatus)
	require.Equal(t, uint64(2), accepted.AcceptedVotingPower)

	replay, err := f.msg.EmergencyFreezeVote(f.contextAt(102), vote)
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, replay.Status)
	conflict := *vote
	conflict.Vote = types.EmergencyFreezeVote_EMERGENCY_FREEZE_VOTE_REJECT
	_, err = f.msg.EmergencyFreezeVote(f.contextAt(102), &conflict)
	require.ErrorIs(t, err, types.ErrInvalidFreezeVote)

	profile, err := f.fixture.keeper.GetProfile(f.contextAt(102), "model-a", 1)
	require.NoError(t, err)
	require.Equal(t, types.ModelStatusEmergencyFrozen, profile.Status)
	stored, err := f.fixture.keeper.EmergencyFreezeVoteState.Get(f.contextAt(102), types.NewEmergencyFreezeVoteKey(opened.GetFreezeSignalId(), consensusAddress))
	require.NoError(t, err)
	require.Equal(t, types.EmergencyFreezeVote_EMERGENCY_FREEZE_VOTE_ACCEPT, stored.Vote)
	events := sdk.UnwrapSDKContext(voteCtx).EventManager().Events()[eventStart:]
	require.Len(t, events, 3)
	require.Equal(t, proto.MessageName(&types.EventEmergencyFreezeVoteRecorded{}), events[0].Type)
	require.Equal(t, proto.MessageName(&types.EventEmergencyFreezeAccepted{}), events[1].Type)
	require.Equal(t, proto.MessageName(&types.EventModelProfileStateChanged{}), events[2].Type)
	require.Len(t, sdk.UnwrapSDKContext(voteCtx).EventManager().Events(), eventStart+3, "exact replay must not emit duplicate events")
}

func TestFreezeSignalRejectThresholdAndUnknownSigner(t *testing.T) {
	validator := hubAddress(t, 233)
	f := newFreezeRuntimeFixture(t, 100, 20, 40, 3, map[string]types.ValidatorSnapshotMember{
		validator: {SignerAddress: validator, ConsensusAddress: bytes.Repeat([]byte{0x12}, 20), VotingPower: 2},
	})
	opened := f.submit(t, 101)
	_, err := f.msg.EmergencyFreezeVote(f.contextAt(101), &types.MsgEmergencyFreezeVote{
		FreezeSignalId: opened.GetFreezeSignalId(), Vote: types.EmergencyFreezeVote_EMERGENCY_FREEZE_VOTE_ACCEPT, ValidatorAddress: hubAddress(t, 234),
	})
	require.ErrorIs(t, err, types.ErrInvalidSigner)

	rejected, err := f.msg.EmergencyFreezeVote(f.contextAt(101), &types.MsgEmergencyFreezeVote{
		FreezeSignalId: opened.GetFreezeSignalId(), Vote: types.EmergencyFreezeVote_EMERGENCY_FREEZE_VOTE_REJECT, ValidatorAddress: validator,
	})
	require.NoError(t, err)
	require.Equal(t, types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_REJECTED, rejected.SignalStatus)
	profile, err := f.fixture.keeper.GetProfile(f.contextAt(101), "model-a", 1)
	require.NoError(t, err)
	require.Equal(t, types.ModelStatusActive, profile.Status)
}

func TestFreezeSignalExpiryAndTwoPhasePrune(t *testing.T) {
	validator := hubAddress(t, 235)
	consensusAddress := bytes.Repeat([]byte{0x13}, 20)
	f := newFreezeRuntimeFixture(t, 2, 2, 4, 10, map[string]types.ValidatorSnapshotMember{
		validator: {SignerAddress: validator, ConsensusAddress: consensusAddress, VotingPower: 1},
	})
	opened := f.submit(t, 101)
	_, err := f.msg.EmergencyFreezeVote(f.contextAt(101), &types.MsgEmergencyFreezeVote{
		FreezeSignalId: opened.GetFreezeSignalId(), Vote: types.EmergencyFreezeVote_EMERGENCY_FREEZE_VOTE_ACCEPT, ValidatorAddress: validator,
	})
	require.NoError(t, err)

	processed, err := f.fixture.keeper.CloseExpiredFreezeSignals(f.contextAt(103), 103, 10)
	require.NoError(t, err)
	require.Zero(t, processed)
	processed, err = f.fixture.keeper.CloseExpiredFreezeSignals(f.contextAt(104), 104, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(1), processed)

	processed, err = f.fixture.keeper.ProcessFreezeSignalPrunes(f.contextAt(106), 106, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(1), processed)
	_, err = f.fixture.keeper.EmergencyFreezeVoteState.Get(f.contextAt(106), types.NewEmergencyFreezeVoteKey(opened.GetFreezeSignalId(), consensusAddress))
	require.ErrorIs(t, err, collections.ErrNotFound)
	_, err = f.fixture.keeper.FreezeSignalState.Get(f.contextAt(106), opened.GetFreezeSignalId())
	require.NoError(t, err)

	processed, err = f.fixture.keeper.ProcessFreezeSignalPrunes(f.contextAt(108), 108, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(1), processed)
	_, err = f.fixture.keeper.FreezeSignalState.Get(f.contextAt(108), opened.GetFreezeSignalId())
	require.ErrorIs(t, err, collections.ErrNotFound)
}

func TestFreezeBuildCursorSurvivesRestart(t *testing.T) {
	validator := hubAddress(t, 236)
	f := newFreezeRuntimeFixture(t, 20, 20, 40, 3, map[string]types.ValidatorSnapshotMember{
		validator: {SignerAddress: validator, ConsensusAddress: bytes.Repeat([]byte{0x14}, 20), VotingPower: 1},
	})
	f.feed.paginated = true
	building := f.submit(t, 101)
	require.Equal(t, types.FreezeSignalBuildStatus_FREEZE_SIGNAL_BUILD_STATUS_BUILDING, building.BuildStatus)

	exported, err := f.fixture.keeper.ExportGenesis(f.contextAt(101))
	require.NoError(t, err)
	require.Len(t, exported.FreezeSignalBuildCursors, 1)
	require.Len(t, exported.FreezeSignalWindowBindings, 1)

	restarted := initFixture(t)
	restarted.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(restarted.ctx).WithChainID(freezeRuntimeChainID).WithBlockHeight(101))
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	restarted.keeper = restarted.keeper.WithFreezeRuntimeDependencies(f.feed, f.snapshots)
	restartedMsg := keeper.NewMsgServerImpl(restarted.keeper, types.MsgServerDependencies{FreezeTaskValidator: f.feed, ValidatorSnapshotProvider: f.snapshots})
	response, err := restartedMsg.SubmitFreezeSignal(restarted.ctx, &types.MsgSubmitFreezeSignal{
		ModelId: "model-a", ProfileVersion: 1, SubmitterAddress: f.submitter,
	})
	require.NoError(t, err)
	require.Equal(t, types.FreezeSignalBuildStatus_FREEZE_SIGNAL_BUILD_STATUS_OPEN, response.BuildStatus)
	signal, err := restarted.keeper.FreezeSignalState.Get(restarted.ctx, response.GetFreezeSignalId())
	require.NoError(t, err)
	require.Equal(t, uint32(2), signal.IncludedFailureTaskRefCount)
}

func TestFreezeQueriesUseBoundOpaqueTokens(t *testing.T) {
	f := newFreezeRuntimeFixture(t, 2, 20, 40, 10, nil)
	first := f.submit(t, 101)
	_, err := f.fixture.keeper.CloseExpiredFreezeSignals(f.contextAt(104), 104, 10)
	require.NoError(t, err)
	second := f.submit(t, 202)
	_, err = f.fixture.keeper.CloseExpiredFreezeSignals(f.contextAt(205), 205, 10)
	require.NoError(t, err)
	require.NotEqual(t, first.GetFreezeSignalId(), second.GetFreezeSignalId())

	query := keeper.NewQueryServerImpl(f.fixture.keeper)
	request := &types.QueryFreezeSignalsRequest{
		ModelId: "model-a", ProfileVersion: 1, Status: types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_EXPIRED,
		Page: shared.QueryPageRequestV1{Limit: 1},
	}
	page1, err := query.FreezeSignals(f.contextAt(205), request)
	require.NoError(t, err)
	require.Len(t, page1.Signals, 1)
	require.NotEmpty(t, page1.Page.NextPageToken)
	request.Page.PageToken = page1.Page.NextPageToken
	page2, err := query.FreezeSignals(f.contextAt(205), request)
	require.NoError(t, err)
	require.Len(t, page2.Signals, 1)
	require.Empty(t, page2.Page.NextPageToken)
	require.NotEqual(t, page1.Signals[0].FreezeSignalId, page2.Signals[0].FreezeSignalId)

	_, err = query.FreezeSignals(f.contextAt(206), request)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	wrongSelector := *request
	wrongSelector.Status = types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_REJECTED
	_, err = query.FreezeSignals(f.contextAt(205), &wrongSelector)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestEmergencyFreezeVotesUseBoundOpaqueTokens(t *testing.T) {
	validatorA := hubAddress(t, 238)
	validatorB := hubAddress(t, 239)
	f := newFreezeRuntimeFixture(t, 100, 20, 40, 10, map[string]types.ValidatorSnapshotMember{
		validatorA: {SignerAddress: validatorA, ConsensusAddress: bytes.Repeat([]byte{0x31}, 20), VotingPower: 1},
		validatorB: {SignerAddress: validatorB, ConsensusAddress: bytes.Repeat([]byte{0x32}, 20), VotingPower: 1},
	})
	first := f.submit(t, 101)
	for index, validator := range []string{validatorA, validatorB} {
		vote := types.EmergencyFreezeVote_EMERGENCY_FREEZE_VOTE_ACCEPT
		if index == 1 {
			vote = types.EmergencyFreezeVote_EMERGENCY_FREEZE_VOTE_REJECT
		}
		_, err := f.msg.EmergencyFreezeVote(f.contextAt(101), &types.MsgEmergencyFreezeVote{
			FreezeSignalId: first.GetFreezeSignalId(), Vote: vote, ValidatorAddress: validator,
		})
		require.NoError(t, err)
	}
	_, err := f.fixture.keeper.CloseExpiredFreezeSignals(f.contextAt(202), 202, 10)
	require.NoError(t, err)
	second := f.submit(t, 202)
	require.NotEqual(t, first.GetFreezeSignalId(), second.GetFreezeSignalId())

	query := keeper.NewQueryServerImpl(f.fixture.keeper)
	request := &types.QueryEmergencyFreezeVotesRequest{
		FreezeSignalId: first.GetFreezeSignalId(), Page: shared.QueryPageRequestV1{Limit: 1},
	}
	page1, err := query.EmergencyFreezeVotes(f.contextAt(202), request)
	require.NoError(t, err)
	require.Len(t, page1.Votes, 1)
	require.NotEmpty(t, page1.Page.NextPageToken)
	request.Page.PageToken = page1.Page.NextPageToken
	page2, err := query.EmergencyFreezeVotes(f.contextAt(202), request)
	require.NoError(t, err)
	require.Len(t, page2.Votes, 1)
	require.Empty(t, page2.Page.NextPageToken)
	require.NotEqual(t, page1.Votes[0].ValidatorConsensusAddress, page2.Votes[0].ValidatorConsensusAddress)

	_, err = query.EmergencyFreezeVotes(f.contextAt(203), request)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	wrongSelector := *request
	wrongSelector.FreezeSignalId = second.GetFreezeSignalId()
	_, err = query.EmergencyFreezeVotes(f.contextAt(202), &wrongSelector)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = query.FreezeSignals(f.contextAt(202), &types.QueryFreezeSignalsRequest{
		ModelId: "model-a", ProfileVersion: 1, Status: types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_EXPIRED,
		Page: shared.QueryPageRequestV1{Limit: 1, PageToken: page1.Page.NextPageToken},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

type malformedFreezeFailureFeed struct{}

func (malformedFreezeFailureFeed) ScanFreezeSignalFailures(_ context.Context, request types.FreezeSignalFailureScanRequest) (types.FreezeSignalFailureScanResult, error) {
	return types.FreezeSignalFailureScanResult{
		Failures: []types.FreezeSignalFailure{{
			TaskID: []byte{1}, SettlementID: bytes.Repeat([]byte{2}, 32), FinalityHeight: request.RiskWindowStartHeight,
			FailureClass: types.FreezeFailureClassMetricThresholdBreach, EvidenceDigest: bytes.Repeat([]byte{3}, 32), IncludedInRoot: true,
		}},
		Visited: 1, Done: true,
	}, nil
}

func TestFreezeSignalBuildReschedulesWhenHistoricalSnapshotIsUnavailable(t *testing.T) {
	f := newFreezeRuntimeFixture(t, 20, 20, 40, 3, nil)
	failingSnapshots := f.snapshots
	failingSnapshots.captureErr = types.ErrInvariantBroken
	f.fixture.keeper = f.fixture.keeper.WithFreezeRuntimeDependencies(f.feed, failingSnapshots)

	ctx := f.contextAt(101)
	visited, err := f.fixture.keeper.ProcessFreezeRiskWindowSchedules(ctx, 101, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	visited, err = f.fixture.keeper.ProcessFreezeSignalBuilds(ctx, 101, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)

	cursorKey := types.NewFreezeSignalBuildCursorKey("model-a", 1)
	windowKey := types.NewFreezeSignalByWindowKey("model-a", 1, 0)
	_, err = f.fixture.keeper.FreezeSignalBuildCursor.Get(ctx, cursorKey)
	require.ErrorIs(t, err, collections.ErrNotFound)
	_, err = f.fixture.keeper.FreezeSignalByWindow.Get(ctx, windowKey)
	require.ErrorIs(t, err, collections.ErrNotFound)
	hasRetry, err := f.fixture.keeper.FreezeRiskWindowScheduleIndex.Has(ctx, types.NewFreezeRiskWindowScheduleKey(102, "model-a", 1))
	require.NoError(t, err)
	require.True(t, hasRetry)
	profile, err := f.fixture.keeper.GetProfile(ctx, "model-a", 1)
	require.NoError(t, err)
	require.Nil(t, profile.XLastFreezeRiskWindowEvaluated)

	// A later block with the history restored consumes the one retry schedule
	// and opens exactly one signal for the still-unadvanced window.
	f.fixture.keeper = f.fixture.keeper.WithFreezeRuntimeDependencies(f.feed, f.snapshots)
	retryCtx := f.contextAt(102)
	visited, err = f.fixture.keeper.ProcessFreezeRiskWindowSchedules(retryCtx, 102, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	visited, err = f.fixture.keeper.ProcessFreezeSignalBuilds(retryCtx, 102, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	binding, err := f.fixture.keeper.FreezeSignalByWindow.Get(retryCtx, windowKey)
	require.NoError(t, err)
	require.Equal(t, types.FreezeSignalWindowPhase_FREEZE_SIGNAL_WINDOW_PHASE_OPEN, binding.Phase)
	require.Len(t, binding.GetFreezeSignalId(), 32)
	profile, err = f.fixture.keeper.GetProfile(retryCtx, "model-a", 1)
	require.NoError(t, err)
	require.NotNil(t, profile.XLastFreezeRiskWindowEvaluated)
	require.Equal(t, uint64(0), profile.GetLastFreezeRiskWindowEvaluated())
}

func TestFreezeSignalRejectsMalformedTaskFailureWithoutWrites(t *testing.T) {
	f := newFreezeRuntimeFixture(t, 20, 20, 40, 3, nil)
	feed := malformedFreezeFailureFeed{}
	f.fixture.keeper = f.fixture.keeper.WithFreezeRuntimeDependencies(feed, f.snapshots)
	f.msg = keeper.NewMsgServerImpl(f.fixture.keeper, types.MsgServerDependencies{FreezeTaskValidator: feed, ValidatorSnapshotProvider: f.snapshots})
	ctx := f.contextAt(101)
	before := len(sdk.UnwrapSDKContext(ctx).EventManager().Events())

	_, err := f.msg.SubmitFreezeSignal(ctx, &types.MsgSubmitFreezeSignal{
		ModelId: "model-a", ProfileVersion: 1, SubmitterAddress: f.submitter,
	})
	require.ErrorIs(t, err, types.ErrInvariantBroken)
	require.Len(t, sdk.UnwrapSDKContext(ctx).EventManager().Events(), before)
	_, err = f.fixture.keeper.FreezeSignalBuildCursor.Get(ctx, types.NewFreezeSignalBuildCursorKey("model-a", 1))
	require.ErrorIs(t, err, collections.ErrNotFound)
	_, err = f.fixture.keeper.FreezeSignalByWindow.Get(ctx, types.NewFreezeSignalByWindowKey("model-a", 1, 0))
	require.ErrorIs(t, err, collections.ErrNotFound)
}

func TestFreezeGenesisRejectsTallyAndBindingAttacks(t *testing.T) {
	validator := hubAddress(t, 237)
	f := newFreezeRuntimeFixture(t, 20, 20, 40, 10, map[string]types.ValidatorSnapshotMember{
		validator: {SignerAddress: validator, ConsensusAddress: bytes.Repeat([]byte{0x15}, 20), VotingPower: 1},
	})
	opened := f.submit(t, 101)
	_, err := f.msg.EmergencyFreezeVote(f.contextAt(101), &types.MsgEmergencyFreezeVote{
		FreezeSignalId: opened.GetFreezeSignalId(), Vote: types.EmergencyFreezeVote_EMERGENCY_FREEZE_VOTE_ACCEPT, ValidatorAddress: validator,
	})
	require.NoError(t, err)
	exported, err := f.fixture.keeper.ExportGenesis(f.contextAt(101))
	require.NoError(t, err)

	tallyAttack := *exported
	tallyAttack.FreezeSignals = append([]types.FreezeSignalState(nil), exported.FreezeSignals...)
	tallyAttack.FreezeSignals[0].AcceptedVotingPower++
	require.ErrorContains(t, tallyAttack.Validate(), "vote rows do not match tally")

	bindingAttack := *exported
	bindingAttack.FreezeSignalWindowBindings = nil
	require.ErrorContains(t, bindingAttack.Validate(), "no matching window binding")
}

// EmergencyFreezeVotes is the one page token in this batch whose primary key
// changed shape AND whose old form still decodes: freeze_signal_id moved from a
// length-prefixed non-terminal BytesKey to a fixed-width Hash32, so the codec
// reads the old leading 0x20 plus the first 31 id bytes as a structurally valid
// component and the canonicality re-encode reproduces the original bytes exactly.
//
// What rejects the token is the scope clause in decodeEmergencyFreezeVotePageToken:
// the mis-split K1 cannot equal the freeze_signal_id the request names. This mints
// a genuine pre-Hash32 token -- correctly bound to chain id, RPC, selector and
// height -- so the only thing left to reject it is that clause.
func TestEmergencyFreezeVotesRejectPreHash32PageTokens(t *testing.T) {
	validator := hubAddress(t, 240)
	consAddr := bytes.Repeat([]byte{0x41}, 20)
	f := newFreezeRuntimeFixture(t, 100, 20, 40, 10, map[string]types.ValidatorSnapshotMember{
		validator: {SignerAddress: validator, ConsensusAddress: consAddr, VotingPower: 1},
	})
	signal := f.submit(t, 101)
	_, err := f.msg.EmergencyFreezeVote(f.contextAt(101), &types.MsgEmergencyFreezeVote{
		FreezeSignalId:   signal.GetFreezeSignalId(),
		Vote:             types.EmergencyFreezeVote_EMERGENCY_FREEZE_VOTE_ACCEPT,
		ValidatorAddress: validator,
	})
	require.NoError(t, err)

	ctx := f.contextAt(102)
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	signalID := signal.GetFreezeSignalId()

	// The pre-Hash32 encoding: BytesKey wrote the non-terminal component behind a
	// single length byte.
	legacyPrimaryKey := append([]byte{byte(len(signalID))}, signalID...)
	legacyPrimaryKey = append(legacyPrimaryKey, consAddr...)

	rpcDigest := shared.CanonicalHashBytes(
		shared.MustDomain(shared.DomainQueryRPCV1), []byte("/hub.v1.Query/EmergencyFreezeVotes"),
	)
	selectorDigest := shared.CanonicalHashBytes(
		shared.MustDomain(shared.DomainQuerySelectorV1),
		[]byte(sdkCtx.ChainID()), rpcDigest, signalID,
	)
	// External anchors. Both digests above are rebuilt here the same way the query
	// server builds them, so the pair would agree with itself after any reordering
	// of the preimage. The frozen hex is the part that cannot be rebuilt, and it is
	// what a page token minted by an earlier binary actually carries.
	require.Equal(t, "e2be1d98d6619599d0458fc0169bba65a625970128e6aabad802c1711b91956f",
		hex.EncodeToString(rpcDigest),
		"TRUEOPEN_QUERY_RPC_V1 over /hub.v1.Query/EmergencyFreezeVotes is a frozen consensus preimage; moving this constant is a consensus change and must be re-checked against the §1.4 domain registry")
	require.Equal(t, "943d7e617f589749a979274af7d1938e9a3f2d9a3a62c8c1eb1f70d0c7f85ef2",
		hex.EncodeToString(selectorDigest),
		"TRUEOPEN_QUERY_SELECTOR_V1 is a frozen consensus preimage; moving this constant is a consensus change and must be re-checked against the §1.4 domain registry")
	// The selector above binds signal_id, so pinning the selector without pinning
	// the id it commits to would leave half the preimage unanchored.
	require.Equal(t, "fa5a1586446ee3f55b8e8ebf2f5db570dcb237fa7f74b5d51a7679fbfb60204a",
		hex.EncodeToString(signalID),
		"TRUEOPEN_FREEZE_SIGNAL_V1 is a frozen consensus preimage; moving this constant is a consensus change and must be re-checked against the §1.4 domain registry")
	legacyToken, err := shared.EncodePageTokenV1(rpcDigest, selectorDigest, legacyPrimaryKey, uint64(sdkCtx.BlockHeight()))
	require.NoError(t, err)

	query := keeper.NewQueryServerImpl(f.fixture.keeper)
	_, err = query.EmergencyFreezeVotes(ctx, &types.QueryEmergencyFreezeVotesRequest{
		FreezeSignalId: signalID,
		Page:           shared.QueryPageRequestV1{Limit: 1, PageToken: legacyToken},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err),
		"a pre-Hash32 vote page token must be rejected by the scope clause")

	// The same request without the stale token still works, so the rejection above
	// is about the token and not about the fixture.
	page, err := query.EmergencyFreezeVotes(ctx, &types.QueryEmergencyFreezeVotesRequest{
		FreezeSignalId: signalID, Page: shared.QueryPageRequestV1{Limit: 1},
	})
	require.NoError(t, err)
	require.Len(t, page.Votes, 1)
}
