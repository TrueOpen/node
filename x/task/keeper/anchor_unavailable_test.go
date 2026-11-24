package keeper

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	"github.com/TrueOpen/node/x/task/types"
)

const (
	anchorPass        = types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS
	anchorReject      = types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT
	anchorNoVerdict   = types.AnchorVerdictV1_ANCHOR_VERDICT_V1_NO_VERDICT
	anchorUnavailable = types.AnchorVerdictV1_ANCHOR_VERDICT_V1_UNAVAILABLE
)

func (f *anchorOnlyFixture) settleAtDeadline(t *testing.T) types.TaskSettlementState {
	t.Helper()
	deadline := f.settlementDeadline(t)
	visited, err := f.keeper.processExpiredSettlementDeadlines(f.at(int64(deadline)), deadline, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	settlement, err := f.keeper.ReadTaskSettlement(f.ctx, f.taskKey)
	require.NoError(t, err)
	return settlement
}

// The Worker records an opening and a proof in time and then keeps them out of
// reach. Validators holding two thirds of the power say so; at the verdict
// deadline the audit ends like a missed deadline: the task is not paid and the
// Worker takes the timeout slash, without a jail. Before this vote existed the
// same Worker ended NO_VERDICT, was paid in full and lost nothing.
func TestAnchorUnavailableAtTwoThirdsTakesTheTimeoutFault(t *testing.T) {
	const bond = uint64(4_000)
	f := newAnchorOnlyFixture(t, bond, true)
	f.submitReceipt(t)
	state := f.sampleRound(t)
	require.True(t, state.Selected)
	f.openAndProveSampled(t, state)

	voteAt := state.SampleHeight + 3
	resp, err := f.vote(0, f.valAcct[0], anchorUnavailable, voteAt)
	require.NoError(t, err)
	require.False(t, resp.Finalized)
	resp, err = f.vote(1, f.valAcct[1], anchorUnavailable, voteAt)
	require.NoError(t, err)
	require.False(t, resp.Finalized, "UNAVAILABLE decides nothing before the deadline")
	require.Equal(t, uint64(2), resp.Tally.UnavailablePower)
	_, err = f.settleNow(t, state.VerdictDeadlineHeight-1)
	require.ErrorContains(t, err, "pending until")

	settlement := f.settleAtDeadline(t)
	f.requireWorkerFaulted(t, settlement, hubtypes.FaultTypeAnchorTimeout, f.params.Verification.AnchorTimeoutSlashBps, bond)
	audit, err := f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.Equal(t, anchorReject, audit.TaskVerdict)
	require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_MATERIAL_UNAVAILABLE, audit.ResultSource)
	require.Equal(t, uint64(2), audit.Tally.UnavailablePower)
	require.Equal(t, uint64(3), audit.Tally.TotalPower)
	require.NotZero(t, audit.ResultHeight)
}

// One validator's trouble reaching the Worker's store is not the Worker's
// fault: below two thirds the audit stays NO_VERDICT, paid and unslashed.
func TestAnchorUnavailableBelowTwoThirdsStaysNoVerdict(t *testing.T) {
	f := newAnchorOnlyFixture(t, 4_000, true)
	f.submitReceipt(t)
	state := f.sampleRound(t)
	f.openAndProveSampled(t, state)
	_, err := f.vote(0, f.valAcct[0], anchorUnavailable, state.SampleHeight+3)
	require.NoError(t, err)

	settlement := f.settleAtDeadline(t)
	f.requirePaidInFull(t, settlement)
	require.Empty(t, *f.faults)
	audit, err := f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.Equal(t, anchorNoVerdict, audit.TaskVerdict)
	require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT, audit.ResultSource)
}

// Every validator voted and no value holds two thirds: NO_VERDICT, but only at
// the deadline, because an UNAVAILABLE vote may still be replaced.
func TestAnchorMixedVotesWithoutTwoThirdsStayNoVerdict(t *testing.T) {
	f := newAnchorOnlyFixture(t, 4_000, true)
	f.submitReceipt(t)
	state := f.sampleRound(t)
	f.openAndProveSampled(t, state)
	voteAt := state.SampleHeight + 3
	_, err := f.vote(0, f.valAcct[0], anchorUnavailable, voteAt)
	require.NoError(t, err)
	_, err = f.vote(1, f.valAcct[1], anchorPass, voteAt)
	require.NoError(t, err)
	resp, err := f.vote(2, f.valAcct[2], anchorNoVerdict, voteAt)
	require.NoError(t, err)
	require.False(t, resp.Finalized, "an open UNAVAILABLE vote keeps the audit open until the deadline")
	_, err = f.settleNow(t, voteAt+1)
	require.ErrorContains(t, err, "pending until")

	settlement := f.settleAtDeadline(t)
	f.requirePaidInFull(t, settlement)
	require.Empty(t, *f.faults)
}

// A validator that could not fetch earlier and can now replaces UNAVAILABLE
// with its judgement; a judgement is never withdrawn.
func TestAnchorUnavailableVoteIsReplacedByAJudgementOnly(t *testing.T) {
	f := newAnchorOnlyFixture(t, 4_000, true)
	f.submitReceipt(t)
	state := f.sampleRound(t)
	f.openAndProveSampled(t, state)
	voteAt := state.SampleHeight + 3
	for i := 0; i < 2; i++ {
		_, err := f.vote(i, f.valAcct[i], anchorUnavailable, voteAt)
		require.NoError(t, err)
	}
	_, err := f.vote(0, f.valAcct[0], anchorNoVerdict, voteAt+1)
	require.ErrorContains(t, err, "already voted", "UNAVAILABLE is replaced by PASS or REJECT only")

	resp, err := f.vote(0, f.valAcct[0], anchorPass, voteAt+1)
	require.NoError(t, err)
	require.False(t, resp.Finalized)
	require.Equal(t, uint64(1), resp.Tally.PassPower)
	require.Equal(t, uint64(1), resp.Tally.UnavailablePower)
	require.Equal(t, uint32(2), resp.Tally.VoteCount, "a replaced vote is still one vote")
	_, err = f.vote(0, f.valAcct[0], anchorUnavailable, voteAt+2)
	require.ErrorContains(t, err, "already voted", "a judgement is not withdrawn")
	_, err = f.vote(0, f.valAcct[0], anchorReject, voteAt+2)
	require.ErrorContains(t, err, "already voted")

	resp, err = f.vote(1, f.valAcct[1], anchorPass, voteAt+2)
	require.NoError(t, err)
	require.True(t, resp.Finalized)
	require.Equal(t, anchorPass, resp.TaskVerdict)
	settlement, err := f.settleNow(t, voteAt+3)
	require.NoError(t, err)
	f.requirePaidInFull(t, settlement)
	require.Empty(t, *f.faults)
}

// UNAVAILABLE is a vote value only.
func TestAnchorUnavailableIsNeverAPositionVerdict(t *testing.T) {
	f := newAnchorOnlyFixture(t, 4_000, true)
	f.submitReceipt(t)
	state := f.sampleRound(t)
	f.openAndProveSampled(t, state)
	vote := func(verdict types.AnchorVerdictV1, position types.AnchorVerdictV1) error {
		_, err := f.server.SubmitAnchorValidatorVote(f.at(int64(state.SampleHeight+3)), &types.MsgSubmitAnchorValidatorVote{
			TaskId: f.taskID, OpeningHash: bytes.Repeat([]byte{0x0c}, 32), ProofHash: bytes.Repeat([]byte{0x0d}, 32),
			Verdict: verdict, ValidatorOperatorAddress: f.valoper[0], SubmitterAddress: f.valAcct[0],
			Verdicts: []types.AnchorPositionVerdictV1{{Position: state.Positions[0], Verdict: position}},
		})
		return err
	}
	require.ErrorContains(t, vote(anchorUnavailable, anchorPass), "no position verdicts")
	require.ErrorContains(t, vote(anchorPass, anchorUnavailable), "position verdict is invalid")
}

// What a Worker records must be something every validator can be expected to
// fetch: an http(s) location and a size within the common bound.
func TestAnchorRecordedMaterialIsBoundedAtSubmission(t *testing.T) {
	f := newAnchorOnlyFixture(t, 4_000, true)
	f.submitReceipt(t)
	state := f.sampleRound(t)
	at := f.at(int64(state.SampleHeight + 1))

	for name, mutate := range map[string]func(*types.MsgSubmitAnchorOpening){
		"file uri":        func(m *types.MsgSubmitAnchorOpening) { m.OpeningUri = "file:///tmp/o" },
		"no scheme":       func(m *types.MsgSubmitAnchorOpening) { m.OpeningUri = "da.example/o" },
		"no host":         func(m *types.MsgSubmitAnchorOpening) { m.OpeningUri = "https:///o" },
		"credentials":     func(m *types.MsgSubmitAnchorOpening) { m.OpeningUri = "https://u:p@da.example/o" },
		"other scheme":    func(m *types.MsgSubmitAnchorOpening) { m.AnchorCommitUri = "ftp://da.example/c" },
		"padded":          func(m *types.MsgSubmitAnchorOpening) { m.AnchorCommitUri = " https://da.example/c" },
		"over size bound": func(m *types.MsgSubmitAnchorOpening) { m.OpeningSizeBytes = maxAnchorOpeningSizeBytes + 1 },
	} {
		msg := f.openingMsg(state, f.worker)
		mutate(msg)
		_, err := f.server.SubmitAnchorOpening(at, msg)
		require.Error(t, err, name)
	}
	atBound := f.openingMsg(state, f.worker)
	atBound.OpeningSizeBytes = maxAnchorOpeningSizeBytes
	atBound.OpeningUri = "http://203.0.113.7:50132/da/0c"
	_, err := f.server.SubmitAnchorOpening(at, atBound)
	require.NoError(t, err, "plain http and the bound itself are accepted")

	proof := func() *types.MsgSubmitAnchorProof {
		return &types.MsgSubmitAnchorProof{TaskId: f.taskID, OpeningHash: bytes.Repeat([]byte{0x0c}, 32),
			ProofHash: bytes.Repeat([]byte{0x0d}, 32), ProofSizeBytes: 5, ProofUri: "https://da.example/p",
			EpochManifestHash: bytes.Repeat([]byte{0x0e}, 32), EpochManifestUri: "https://da.example/m", SubmitterAddress: f.worker}
	}
	for name, mutate := range map[string]func(*types.MsgSubmitAnchorProof){
		"file proof uri":   func(m *types.MsgSubmitAnchorProof) { m.ProofUri = "file:///tmp/p" },
		"bad manifest uri": func(m *types.MsgSubmitAnchorProof) { m.EpochManifestUri = "m" },
		"over proof bound": func(m *types.MsgSubmitAnchorProof) { m.ProofSizeBytes = maxAnchorProofSizeBytes + 1 },
		"empty proof uri":  func(m *types.MsgSubmitAnchorProof) { m.ProofUri = "" },
		"oversized uri": func(m *types.MsgSubmitAnchorProof) {
			m.ProofUri = "https://da.example/" + string(bytes.Repeat([]byte{'a'}, 600))
		},
		"manifest no host":  func(m *types.MsgSubmitAnchorProof) { m.EpochManifestUri = "http://" },
		"manifest userinfo": func(m *types.MsgSubmitAnchorProof) { m.EpochManifestUri = "http://x@da.example/m" },
	} {
		msg := proof()
		mutate(msg)
		_, err := f.server.SubmitAnchorProof(at, msg)
		require.Error(t, err, name)
	}
	_, err = f.server.SubmitAnchorProof(at, proof())
	require.NoError(t, err)
}
