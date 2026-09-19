package keeper_test

import (
	"bytes"
	"math"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/keeper"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestBuildDataUnavailableAggregateFactsUsesSelectedVerifierSlots(t *testing.T) {
	input := dataUnavailableAggregateInput()
	aggregates, err := keeper.BuildDataUnavailableAggregateFacts(input)
	require.NoError(t, err)
	require.Len(t, aggregates, 2)

	first := aggregates[0]
	require.Equal(t, dataUnavailableBuilders[0], first.BuilderOperatorAddress)
	require.Equal(t, uint32(3), first.RequiredReportCount, "ceil(2*4/3)")
	require.Equal(t, uint32(3), first.ValidReportCount)
	require.True(t, first.ThresholdReached)
	require.Len(t, first.AggregateHash, tasktypes.Hash32Len)
	require.Equal(t, dataUnavailableDigest(0x31), first.ReportDigestsByVerifierSlot[0])
	require.Empty(t, first.ReportDigestsByVerifierSlot[1], "a later accepted commit invalidates the report")
	require.Equal(t, dataUnavailableDigest(0x33), first.ReportDigestsByVerifierSlot[2])
	require.Equal(t, dataUnavailableDigest(0x34), first.ReportDigestsByVerifierSlot[3])

	second := aggregates[1]
	require.Equal(t, dataUnavailableBuilders[1], second.BuilderOperatorAddress)
	require.Equal(t, uint32(1), second.ValidReportCount)
	require.False(t, second.ThresholdReached)
	require.Len(t, second.AggregateHash, tasktypes.Hash32Len)
	require.NotEqual(t, first.AggregateHash, second.AggregateHash)
	require.Empty(t, second.ReportDigestsByVerifierSlot[0])
	require.Empty(t, second.ReportDigestsByVerifierSlot[1])
	require.Empty(t, second.ReportDigestsByVerifierSlot[2])
	require.Equal(t, dataUnavailableDigest(0x34), second.ReportDigestsByVerifierSlot[3])

	replayed, err := keeper.BuildDataUnavailableAggregateFacts(input)
	require.NoError(t, err)
	require.Equal(t, aggregates, replayed)
}

func TestBuildDataUnavailableAggregateFactsRejectsScopeDeadlineAndShapeDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*keeper.DataUnavailableAggregateInput)
		match  string
	}{
		{
			name: "selected verifier scope",
			mutate: func(input *keeper.DataUnavailableAggregateInput) {
				input.VerifierSlots[0].Report.VerifierOperatorAddress = input.SelectedVerifierOperators[1]
			},
			match: "does not belong to the frozen selected verifier",
		},
		{
			name: "deadline",
			mutate: func(input *keeper.DataUnavailableAggregateInput) {
				input.VerifierSlots[0].Report.ReportHeight = input.CommitDeadlineHeight + 1
			},
			match: "outside the commit deadline",
		},
		{
			name: "selected vector",
			mutate: func(input *keeper.DataUnavailableAggregateInput) {
				input.VerifierSlots = input.VerifierSlots[:3]
			},
			match: "must match the frozen selected verifier vector",
		},
		{
			name: "builder vector",
			mutate: func(input *keeper.DataUnavailableAggregateInput) {
				input.VerifierSlots[0].Report.UnavailableBuilderSlots = []bool{true}
			},
			match: "frozen Task Builder slot count",
		},
		{
			name: "empty report",
			mutate: func(input *keeper.DataUnavailableAggregateInput) {
				input.VerifierSlots[0].Report.UnavailableBuilderSlots = []bool{false, false}
			},
			match: "at least one unavailable Task Builder",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := dataUnavailableAggregateInput()
			tc.mutate(&input)
			_, err := keeper.BuildDataUnavailableAggregateFacts(input)
			require.ErrorContains(t, err, tc.match)
		})
	}
}

func TestDataUnavailableThresholdUsesCheckedWidenedArithmetic(t *testing.T) {
	threshold, err := keeper.DataUnavailableReportThreshold(math.MaxUint32)
	require.NoError(t, err)
	require.Equal(t, uint32(2863311530), threshold)

	_, err = keeper.DataUnavailableReportThreshold(0)
	require.ErrorContains(t, err, "must be positive")
}

func TestResolveDataUnavailableCurrentServiceSubmitterUsesSelectedScope(t *testing.T) {
	services := []string{
		sdk.AccAddress(bytes.Repeat([]byte{0xa1}, 20)).String(),
		sdk.AccAddress(bytes.Repeat([]byte{0xa2}, 20)).String(),
		sdk.AccAddress(bytes.Repeat([]byte{0xa3}, 20)).String(),
		sdk.AccAddress(bytes.Repeat([]byte{0xa4}, 20)).String(),
	}
	bindings := make([]keeper.DataUnavailableSelectedServiceBinding, len(dataUnavailableVerifiers))
	for i := range bindings {
		bindings[i] = keeper.DataUnavailableSelectedServiceBinding{
			VerifierOperatorAddress:   dataUnavailableVerifiers[i],
			CurrentServiceAddress:     services[i],
			ServiceAuthorizationNonce: uint64(i + 10),
		}
	}

	operator, nonce, err := keeper.ResolveDataUnavailableCurrentServiceSubmitter(bindings, services[2])
	require.NoError(t, err)
	require.Equal(t, dataUnavailableVerifiers[2], operator)
	require.Equal(t, uint64(12), nonce)

	_, _, err = keeper.ResolveDataUnavailableCurrentServiceSubmitter(bindings, dataUnavailableVerifiers[2])
	require.ErrorContains(t, err, "not a current service address")

	bindings[3].CurrentServiceAddress = bindings[2].CurrentServiceAddress
	_, _, err = keeper.ResolveDataUnavailableCurrentServiceSubmitter(bindings, services[2])
	require.ErrorContains(t, err, "is duplicated")
}

func TestClassifyDataUnavailableReportReplayIsExact(t *testing.T) {
	attempt := keeper.DataUnavailableReportAttempt{
		TaskID:                            dataUnavailableTaskID(),
		VerifyRound:                       tasktypes.VerifyRoundV1,
		VerifierOperatorAddress:           dataUnavailableVerifiers[0],
		UnavailableTaskBuilderBitmap:      []byte{0x03},
		ServiceAuthorizationNonceSnapshot: 9,
	}
	decision, digest, err := keeper.ClassifyDataUnavailableReportReplay(nil, attempt)
	require.NoError(t, err)
	require.Equal(t, keeper.DataUnavailableReplayDecisionNew, decision)
	require.Nil(t, digest)

	existing := &tasktypes.DataUnavailableReportState{
		TaskId:                            bytes.Clone(attempt.TaskID),
		VerifyRound:                       attempt.VerifyRound,
		VerifierOperatorAddress:           attempt.VerifierOperatorAddress,
		UnavailableTaskBuilderBitmap:      bytes.Clone(attempt.UnavailableTaskBuilderBitmap),
		ServiceAuthorizationNonceSnapshot: attempt.ServiceAuthorizationNonceSnapshot,
		ReportHeight:                      99,
		ReportDigest:                      dataUnavailableDigest(0x41),
	}
	decision, digest, err = keeper.ClassifyDataUnavailableReportReplay(existing, attempt)
	require.NoError(t, err)
	require.Equal(t, keeper.DataUnavailableReplayDecisionNoop, decision)
	require.Equal(t, existing.ReportDigest, digest)

	conflict := attempt
	conflict.UnavailableTaskBuilderBitmap = []byte{0x01}
	_, _, err = keeper.ClassifyDataUnavailableReportReplay(existing, conflict)
	require.ErrorContains(t, err, "conflicts with the immutable accepted report")
}

func TestDataUnavailableRegisteredHashesBindScopeAndSlotOrder(t *testing.T) {
	require.Equal(t, uint32(1), tasktypes.VerifyRoundV1)
	attempt := keeper.DataUnavailableReportAttempt{
		TaskID: dataUnavailableTaskID(), VerifyRound: tasktypes.VerifyRoundV1,
		VerifierOperatorAddress:      dataUnavailableVerifiers[0],
		UnavailableTaskBuilderBitmap: []byte{0x03}, ServiceAuthorizationNonceSnapshot: 9,
	}
	reportDigest, err := keeper.DataUnavailableReportDigest("trueopen-test", attempt)
	require.NoError(t, err)
	require.Len(t, reportDigest, tasktypes.Hash32Len)
	zeroRound := attempt
	zeroRound.VerifyRound = 0
	_, err = keeper.DataUnavailableReportDigest("trueopen-test", zeroRound)
	require.ErrorContains(t, err, "unsupported verify round")
	unsupported := attempt
	unsupported.VerifyRound = 2
	roundTwoDigest, err := keeper.DataUnavailableReportDigest("trueopen-test", unsupported)
	require.NoError(t, err)
	require.NotEqual(t, reportDigest, roundTwoDigest)

	changed := attempt
	changed.UnavailableTaskBuilderBitmap = []byte{0x01}
	changedDigest, err := keeper.DataUnavailableReportDigest("trueopen-test", changed)
	require.NoError(t, err)
	require.NotEqual(t, reportDigest, changedDigest)

	bitmapHash, err := keeper.DataUnavailableBitmapHash("trueopen-test", attempt.TaskID, attempt.VerifyRound, 2, attempt.UnavailableTaskBuilderBitmap)
	require.NoError(t, err)
	require.Len(t, bitmapHash, tasktypes.Hash32Len)
	_, err = keeper.DataUnavailableBitmapHash("trueopen-test", attempt.TaskID, attempt.VerifyRound, 9, attempt.UnavailableTaskBuilderBitmap)
	require.ErrorContains(t, err, "length")

	input := dataUnavailableAggregateInput()
	aggregates, err := keeper.BuildDataUnavailableAggregateFacts(input)
	require.NoError(t, err)
	reordered := aggregates[0]
	reordered.ReportDigestsByVerifierSlot = append([][]byte(nil), reordered.ReportDigestsByVerifierSlot...)
	reordered.ReportDigestsByVerifierSlot[0], reordered.ReportDigestsByVerifierSlot[2] = reordered.ReportDigestsByVerifierSlot[2], reordered.ReportDigestsByVerifierSlot[0]
	reorderedHash, err := keeper.DataUnavailableAggregateHash(input.ChainID, input.TaskID, input.VerifyRound, reordered)
	require.NoError(t, err)
	require.NotEqual(t, aggregates[0].AggregateHash, reorderedHash)
}

func TestReportDataUnavailableRejectsMissingTaskWithoutWriting(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *tasktypes.DefaultGenesis()))
	before, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)

	server := keeper.NewMsgServerImpl(f.keeper)
	_, err = server.ReportDataUnavailable(f.ctx, &tasktypes.MsgReportDataUnavailable{
		TaskId:                       dataUnavailableTaskID(),
		VerifyRound:                  tasktypes.VerifyRoundV1,
		UnavailableTaskBuilderBitmap: []byte{0x01},
		SubmitterAddress:             dataUnavailableVerifiers[0],
	})
	require.ErrorContains(t, err, "task core unavailable")

	after, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

var (
	dataUnavailableVerifiers = []string{
		sdk.AccAddress(bytes.Repeat([]byte{0xe1}, 20)).String(),
		sdk.AccAddress(bytes.Repeat([]byte{0xe2}, 20)).String(),
		sdk.AccAddress(bytes.Repeat([]byte{0xe3}, 20)).String(),
		sdk.AccAddress(bytes.Repeat([]byte{0xe4}, 20)).String(),
	}
	dataUnavailableBuilders = []string{
		sdk.AccAddress(bytes.Repeat([]byte{0xf1}, 20)).String(),
		sdk.AccAddress(bytes.Repeat([]byte{0xf2}, 20)).String(),
	}
)

func dataUnavailableAggregateInput() keeper.DataUnavailableAggregateInput {
	return keeper.DataUnavailableAggregateInput{
		ChainID:                   "trueopen-test",
		TaskID:                    dataUnavailableTaskID(),
		VerifyRound:               tasktypes.VerifyRoundV1,
		CommitDeadlineHeight:      100,
		CloseHeight:               100,
		SelectedVerifierOperators: append([]string(nil), dataUnavailableVerifiers...),
		BuilderOperators:          append([]string(nil), dataUnavailableBuilders...),
		VerifierSlots: []keeper.DataUnavailableVerifierSlot{
			{Report: dataUnavailableReport(0, []bool{true, false}, 0x31)},
			{Report: dataUnavailableReport(1, []bool{true, true}, 0x32), HasAcceptedCommit: true},
			{Report: dataUnavailableReport(2, []bool{true, false}, 0x33)},
			{Report: dataUnavailableReport(3, []bool{true, true}, 0x34)},
		},
	}
}

func dataUnavailableReport(verifierSlot int, builders []bool, digestByte byte) *keeper.DataUnavailableStableReport {
	return &keeper.DataUnavailableStableReport{
		TaskID:                  dataUnavailableTaskID(),
		VerifyRound:             tasktypes.VerifyRoundV1,
		VerifierOperatorAddress: dataUnavailableVerifiers[verifierSlot],
		UnavailableBuilderSlots: builders,
		ReportHeight:            99,
		ReportDigest:            dataUnavailableDigest(digestByte),
	}
}

func dataUnavailableTaskID() []byte {
	return bytes.Repeat([]byte{0x21}, tasktypes.Hash32Len)
}

func dataUnavailableDigest(value byte) []byte {
	return bytes.Repeat([]byte{value}, tasktypes.Hash32Len)
}

func dataUnavailableWireSlots(digests [][]byte) []tasktypes.BuilderDataUnavailableSlotV1 {
	slots := make([]tasktypes.BuilderDataUnavailableSlotV1, len(digests))
	for i, digest := range digests {
		if len(digest) == 0 {
			continue
		}
		slots[i].XReportDigest = &tasktypes.BuilderDataUnavailableSlotV1_ReportDigest{
			ReportDigest: bytes.Clone(digest),
		}
	}
	return slots
}
