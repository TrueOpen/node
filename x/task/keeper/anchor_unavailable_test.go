package keeper

import (
	"bytes"
	"context"
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

// daStub adds a published data-availability endpoint to the anchor-only Hub stub.
type daStub struct {
	anchorOnlyHubStub
	endpoint string
}

func (s daStub) GetAnchorDAEndpoint(context.Context, string) string { return s.endpoint }

func (f *anchorOnlyFixture) publishDA(endpoint string) {
	base, ok := f.keeper.hubKeeper.(anchorOnlyHubStub)
	if !ok {
		base = f.keeper.hubKeeper.(daStub).anchorOnlyHubStub
	}
	f.keeper.hubKeeper = daStub{anchorOnlyHubStub: base, endpoint: endpoint}
	f.server = msgServer{k: f.keeper}
}

// setPowers gives the three fixture validators the listed voting powers.
func (f *anchorFixture) setPowers(powers ...int64) {
	for i, power := range powers {
		f.staking.powers[string(bytes.Repeat([]byte{0xA1 + byte(i)}, 20))] = power
	}
}

// sampledAndProved stages a selected audit with an opening and a proof on
// record and returns it with the first height at which UNAVAILABLE is accepted.
func sampledAndProved(t *testing.T, bond uint64) (*anchorOnlyFixture, types.AnchorAuditState, uint64) {
	t.Helper()
	f := newAnchorOnlyFixture(t, bond, true)
	f.submitReceipt(t)
	state := f.sampleRound(t)
	require.True(t, state.Selected)
	f.openAndProveSampled(t, state)
	late := anchorUnavailableVoteOpenHeight(state)
	require.Equal(t, state.ProofDeadlineHeight+(state.VerdictDeadlineHeight-state.ProofDeadlineHeight)/2, late)
	require.Less(t, late, state.VerdictDeadlineHeight)
	return f, state, late
}

func (f *anchorOnlyFixture) requireMaterialUnavailable(t *testing.T, bond uint64) types.AnchorAuditState {
	t.Helper()
	settlement := f.settleAtDeadline(t)
	f.requireWorkerFaulted(t, settlement, hubtypes.FaultTypeAnchorTimeout, f.params.Verification.AnchorTimeoutSlashBps, bond)
	audit, err := f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.Equal(t, anchorReject, audit.TaskVerdict)
	require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_MATERIAL_UNAVAILABLE, audit.ResultSource)
	require.NotZero(t, audit.ResultHeight)
	return audit
}

func (f *anchorOnlyFixture) requireNoVerdictPaid(t *testing.T) {
	t.Helper()
	settlement := f.settleAtDeadline(t)
	f.requirePaidInFull(t, settlement)
	require.Empty(t, *f.faults)
	audit, err := f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.Equal(t, anchorNoVerdict, audit.TaskVerdict)
	require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT, audit.ResultSource)
}

// The Worker records an opening and a proof in time and then keeps them out of
// reach. Validators holding two thirds of the power say so; at the verdict
// deadline the audit ends like a missed deadline: the task is not paid and the
// Worker takes the timeout slash, without a jail. Before this vote existed the
// same Worker ended NO_VERDICT, was paid in full and lost nothing.
func TestAnchorUnavailableAtTwoThirdsTakesTheTimeoutFault(t *testing.T) {
	const bond = uint64(4_000)
	f, state, late := sampledAndProved(t, bond)
	resp, err := f.vote(0, f.valAcct[0], anchorUnavailable, late)
	require.NoError(t, err)
	require.False(t, resp.Finalized)
	resp, err = f.vote(1, f.valAcct[1], anchorUnavailable, late)
	require.NoError(t, err)
	require.False(t, resp.Finalized, "UNAVAILABLE decides nothing before the deadline")
	require.Equal(t, uint64(2), resp.Tally.UnavailablePower)
	_, err = f.settleNow(t, state.VerdictDeadlineHeight-1)
	require.ErrorContains(t, err, "pending until")

	audit := f.requireMaterialUnavailable(t, bond)
	require.Equal(t, uint64(2), audit.Tally.UnavailablePower)
	require.Equal(t, uint64(3), audit.Tally.TotalPower)
}

// A Worker serves bad material to some validators and nothing to the rest, so
// that neither REJECT nor UNAVAILABLE holds two thirds on its own. Every
// validator found fault; together they hold the quorum and the audit ends as the
// timeout fault. Counted separately this ended NO_VERDICT, paid in full.
func TestAnchorRejectAndUnavailableTogetherTakeTheTimeoutFault(t *testing.T) {
	const bond = uint64(4_000)
	f, state, late := sampledAndProved(t, bond)
	f.setPowers(3, 3, 4)
	_, err := f.vote(0, f.valAcct[0], anchorReject, state.SampleHeight+3)
	require.NoError(t, err)
	resp, err := f.vote(1, f.valAcct[1], anchorReject, state.SampleHeight+3)
	require.NoError(t, err)
	require.False(t, resp.Finalized, "60% REJECT is not a verdict")
	resp, err = f.vote(2, f.valAcct[2], anchorUnavailable, late)
	require.NoError(t, err)
	require.False(t, resp.Finalized, "every validator voted, but an UNAVAILABLE vote keeps the audit open")

	audit := f.requireMaterialUnavailable(t, bond)
	require.Equal(t, uint64(6), audit.Tally.RejectPower)
	require.Equal(t, uint64(4), audit.Tally.UnavailablePower)
}

// More than one third of the power obtained the material and verified it, so it
// was obtainable and sound: whatever kept the others from fetching it is not
// the Worker's to pay for.
func TestAnchorPassAboveOneThirdBlocksTheUnavailableOutcome(t *testing.T) {
	f, state, late := sampledAndProved(t, 4_000)
	f.setPowers(3, 3, 4)
	_, err := f.vote(2, f.valAcct[2], anchorPass, state.SampleHeight+3)
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err := f.vote(i, f.valAcct[i], anchorUnavailable, late)
		require.NoError(t, err)
	}
	f.requireNoVerdictPaid(t)
}

// Exactly one third PASS is not "more than one third".
func TestAnchorPassAtExactlyOneThirdDoesNotBlock(t *testing.T) {
	const bond = uint64(4_000)
	f, state, late := sampledAndProved(t, bond)
	_, err := f.vote(2, f.valAcct[2], anchorPass, state.SampleHeight+3)
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err := f.vote(i, f.valAcct[i], anchorUnavailable, late)
		require.NoError(t, err)
	}
	f.requireMaterialUnavailable(t, bond)
}

// One validator's trouble reaching the Worker's store is not the Worker's
// fault: below two thirds the audit stays NO_VERDICT, paid and unslashed.
func TestAnchorUnavailableBelowTwoThirdsStaysNoVerdict(t *testing.T) {
	f, _, late := sampledAndProved(t, 4_000)
	_, err := f.vote(0, f.valAcct[0], anchorUnavailable, late)
	require.NoError(t, err)
	f.requireNoVerdictPaid(t)
}

// Every validator voted and no rule applies: NO_VERDICT, but only at the
// deadline, because an UNAVAILABLE vote may still be replaced.
func TestAnchorMixedVotesWithoutTwoThirdsStayNoVerdict(t *testing.T) {
	f, state, late := sampledAndProved(t, 4_000)
	_, err := f.vote(1, f.valAcct[1], anchorPass, state.SampleHeight+3)
	require.NoError(t, err)
	_, err = f.vote(2, f.valAcct[2], anchorNoVerdict, state.SampleHeight+3)
	require.NoError(t, err)
	resp, err := f.vote(0, f.valAcct[0], anchorUnavailable, late)
	require.NoError(t, err)
	require.False(t, resp.Finalized, "an open UNAVAILABLE vote keeps the audit open until the deadline")
	_, err = f.settleNow(t, late+1)
	require.ErrorContains(t, err, "pending until")
	f.requireNoVerdictPaid(t)
}

// UNAVAILABLE is accepted in the second half of the verdict window only. An
// earlier one is refused, not stored and not counted.
func TestAnchorUnavailableIsRefusedBeforeTheSecondHalfOfTheVerdictWindow(t *testing.T) {
	f, state, late := sampledAndProved(t, 4_000)
	for _, early := range []uint64{state.SampleHeight + 3, state.ProofDeadlineHeight, late - 1} {
		_, err := f.vote(0, f.valAcct[0], anchorUnavailable, early)
		require.ErrorContains(t, err, "second half of the verdict window", "height %d", early)
	}
	votes, err := (&queryServer{k: f.keeper}).AnchorVotes(f.ctx, &types.QueryAnchorVotesRequest{TaskId: f.taskID})
	require.NoError(t, err)
	require.Empty(t, votes.Votes)
	for i := 0; i < 2; i++ { // a refused vote does not stop the validator voting later
		_, err = f.vote(i, f.valAcct[i], anchorUnavailable, late+uint64(i))
		require.NoError(t, err)
	}
	_, err = f.vote(2, f.valAcct[2], anchorUnavailable, state.VerdictDeadlineHeight)
	require.ErrorContains(t, err, "deadline", "no vote of any kind at the deadline")
}

// A validator that could not fetch earlier and can now replaces UNAVAILABLE
// with its judgement; a judgement is never withdrawn. Replacements that bring
// PASS to two thirds end the audit PASS.
func TestAnchorUnavailableVoteIsReplacedByAJudgementOnly(t *testing.T) {
	f, _, late := sampledAndProved(t, 4_000)
	for i := 0; i < 2; i++ {
		_, err := f.vote(i, f.valAcct[i], anchorUnavailable, late)
		require.NoError(t, err)
	}
	_, err := f.vote(0, f.valAcct[0], anchorNoVerdict, late+1)
	require.ErrorContains(t, err, "already voted", "UNAVAILABLE is replaced by PASS or REJECT only")

	resp, err := f.vote(0, f.valAcct[0], anchorPass, late+1)
	require.NoError(t, err)
	require.False(t, resp.Finalized)
	require.Equal(t, uint64(1), resp.Tally.PassPower)
	require.Equal(t, uint64(1), resp.Tally.UnavailablePower)
	require.Equal(t, uint32(2), resp.Tally.VoteCount, "a replaced vote is still one vote")
	_, err = f.vote(0, f.valAcct[0], anchorUnavailable, late+2)
	require.ErrorContains(t, err, "already voted", "a judgement is not withdrawn")
	_, err = f.vote(0, f.valAcct[0], anchorReject, late+2)
	require.ErrorContains(t, err, "already voted")

	resp, err = f.vote(1, f.valAcct[1], anchorPass, late+2)
	require.NoError(t, err)
	require.True(t, resp.Finalized)
	require.Equal(t, anchorPass, resp.TaskVerdict)
	settlement, err := f.settleNow(t, late+3)
	require.NoError(t, err)
	f.requirePaidInFull(t, settlement)
	require.Empty(t, *f.faults)
}

// The deadline rule weighs the stored votes at the validators' power at the
// deadline, not at the time they voted.
func TestAnchorUnavailableIsWeighedAtCurrentPower(t *testing.T) {
	t.Run("power moves away from the voters", func(t *testing.T) {
		f, _, late := sampledAndProved(t, 4_000)
		for i := 0; i < 2; i++ {
			_, err := f.vote(i, f.valAcct[i], anchorUnavailable, late)
			require.NoError(t, err)
		}
		f.setPowers(1, 1, 4) // two thirds when cast, one third at the deadline
		f.requireNoVerdictPaid(t)
	})
	t.Run("power moves to the voter", func(t *testing.T) {
		const bond = uint64(4_000)
		f, _, late := sampledAndProved(t, bond)
		_, err := f.vote(0, f.valAcct[0], anchorUnavailable, late)
		require.NoError(t, err)
		f.setPowers(4, 1, 1) // one third when cast, two thirds at the deadline
		audit := f.requireMaterialUnavailable(t, bond)
		require.Equal(t, uint64(4), audit.Tally.UnavailablePower)
		require.Equal(t, uint64(6), audit.Tally.TotalPower)
	})
	t.Run("an unbonded voter no longer counts", func(t *testing.T) {
		f, _, late := sampledAndProved(t, 4_000)
		for i := 0; i < 2; i++ {
			_, err := f.vote(i, f.valAcct[i], anchorUnavailable, late)
			require.NoError(t, err)
		}
		delete(f.staking.powers, string(bytes.Repeat([]byte{0xA1}, 20)))
		f.setPowers(0, 1, 1) // one voter left the set: one half is not two thirds
		delete(f.staking.powers, string(bytes.Repeat([]byte{0xA1}, 20)))
		f.requireNoVerdictPaid(t)
	})
}

// The rule is the same on the Verifier path (a kind-5 task whose receipt was
// accepted before anchor-only mode, still in flight across the upgrade): its
// audit row carries no data-availability prefix and takes the new vote.
func TestAnchorUnavailableOnAnAuditOfTheVerifierPath(t *testing.T) {
	f := newAnchorFixture(t, 4_000, anchorGoodTokenSize)
	state := f.openAndProve(t)
	require.Empty(t, state.DaUriPrefix)
	require.NotEmpty(t, state.SelectedVerifiers, "a Verifier-path audit")
	late := anchorUnavailableVoteOpenHeight(state)
	_, err := f.vote(0, f.valAcct[0], anchorUnavailable, late-1)
	require.ErrorContains(t, err, "second half")
	for i := 0; i < 2; i++ {
		_, err := f.vote(i, f.valAcct[i], anchorUnavailable, late)
		require.NoError(t, err)
	}
	pass := types.TaskVerdict_TASK_VERDICT_PASS
	_, err = f.keeper.anchorSettlementView(f.at(int64(state.VerdictDeadlineHeight-1)), f.taskKey, pass, state.VerdictDeadlineHeight-1)
	require.ErrorContains(t, err, "pending until")
	view, err := f.keeper.anchorSettlementView(f.at(int64(state.VerdictDeadlineHeight)), f.taskKey, pass, state.VerdictDeadlineHeight)
	require.NoError(t, err)
	require.Equal(t, anchorReject, view.TaskVerdict)
	require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_MATERIAL_UNAVAILABLE, view.ResultSource)
}

// An audit recorded before validator votes had deadlines has no verdict window:
// it never takes an UNAVAILABLE vote.
func TestAnchorUnavailableNeedsAVerdictWindow(t *testing.T) {
	f := newAnchorFixture(t, 4_000, anchorGoodTokenSize)
	state := f.openAndProve(t)
	state.ProofDeadlineHeight, state.VerdictDeadlineHeight = 0, 0
	require.NoError(t, f.keeper.AnchorAudit.Set(f.ctx, f.taskKey, state))
	require.Zero(t, anchorUnavailableVoteOpenHeight(state))
	_, err := f.vote(0, f.valAcct[0], anchorUnavailable, f.sampleAt+10)
	require.ErrorContains(t, err, "second half")
}

// UNAVAILABLE is a vote value only.
func TestAnchorUnavailableIsNeverAPositionVerdict(t *testing.T) {
	f := newAnchorOnlyFixture(t, 4_000, true)
	f.submitReceipt(t)
	state := f.sampleRound(t)
	f.openAndProveSampled(t, state)
	vote := func(verdict types.AnchorVerdictV1, position types.AnchorVerdictV1) error {
		_, err := f.server.SubmitAnchorValidatorVote(f.at(int64(anchorUnavailableVoteOpenHeight(state))), &types.MsgSubmitAnchorValidatorVote{
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
		"port too large":  func(m *types.MsgSubmitAnchorOpening) { m.OpeningUri = "http://a:99999/x" },
		"port zero":       func(m *types.MsgSubmitAnchorOpening) { m.OpeningUri = "http://a:0/x" },
		"fragment":        func(m *types.MsgSubmitAnchorOpening) { m.AnchorCommitUri = "https://da.example/c#f" },
		"control char":    func(m *types.MsgSubmitAnchorOpening) { m.AnchorCommitUri = "https://da.example/c\n" },
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
		"proof port":        func(m *types.MsgSubmitAnchorProof) { m.ProofUri = "https://da.example:70000/p" },
	} {
		msg := proof()
		mutate(msg)
		_, err := f.server.SubmitAnchorProof(at, msg)
		require.Error(t, err, name)
	}
	_, err = f.server.SubmitAnchorProof(at, proof())
	require.NoError(t, err)
}

// A Worker that published a data-availability endpoint before the draw has it
// frozen into the audit; everything it records for that audit must lie under
// it. It cannot move its material elsewhere after learning it was sampled.
func TestAnchorRecordedLocationsMustLieUnderThePublishedEndpoint(t *testing.T) {
	f := newAnchorOnlyFixture(t, 4_000, true)
	f.publishDA("http://203.0.113.7:50132/da/")
	f.submitReceipt(t)
	state := f.sampleRound(t)
	require.True(t, state.Selected)
	require.Equal(t, "http://203.0.113.7:50132/da/", state.DaUriPrefix)

	// Changing the descriptor after the draw changes nothing for this audit.
	f.publishDA("https://elsewhere.example/")
	at := f.at(int64(state.SampleHeight + 1))
	opening := func(openingURI, commitURI string) error {
		msg := f.openingMsg(state, f.worker)
		msg.OpeningUri, msg.AnchorCommitUri = openingURI, commitURI
		_, err := f.server.SubmitAnchorOpening(at, msg)
		return err
	}
	for name, uris := range map[string][2]string{
		"other host":       {"https://da.example/o", "http://203.0.113.7:50132/da/c"},
		"new descriptor":   {"https://elsewhere.example/o", "https://elsewhere.example/c"},
		"other port":       {"http://203.0.113.7:50133/da/o", "http://203.0.113.7:50132/da/c"},
		"other scheme":     {"https://203.0.113.7:50132/da/o", "http://203.0.113.7:50132/da/c"},
		"sibling path":     {"http://203.0.113.7:50132/da2/o", "http://203.0.113.7:50132/da/c"},
		"prefix itself":    {"http://203.0.113.7:50132/da/", "http://203.0.113.7:50132/da/c"},
		"commit elsewhere": {"http://203.0.113.7:50132/da/o", "https://da.example/c"},
	} {
		require.ErrorContains(t, opening(uris[0], uris[1]), "published data-availability endpoint", name)
	}
	require.NoError(t, opening("http://203.0.113.7:50132/da/0c", "http://203.0.113.7:50132/da/cc"))

	proof := func(proofURI, manifestURI string) error {
		_, err := f.server.SubmitAnchorProof(at, &types.MsgSubmitAnchorProof{TaskId: f.taskID, OpeningHash: bytes.Repeat([]byte{0x0c}, 32),
			ProofHash: bytes.Repeat([]byte{0x0d}, 32), ProofSizeBytes: 5, ProofUri: proofURI,
			EpochManifestHash: bytes.Repeat([]byte{0x0e}, 32), EpochManifestUri: manifestURI, SubmitterAddress: f.worker})
		return err
	}
	require.ErrorContains(t, proof("https://da.example/p", "http://203.0.113.7:50132/da/m"), "published data-availability endpoint")
	require.ErrorContains(t, proof("http://203.0.113.7:50132/da/p", "https://da.example/m"), "published data-availability endpoint")
	require.NoError(t, proof("http://203.0.113.7:50132/da/p", "http://203.0.113.7:50132/da/m"))
}

// A Worker that has published no endpoint (every Worker serving before this
// rule existed) is not restricted, and neither is an audit already in flight.
func TestAnchorRecordedLocationsAreUnrestrictedWithoutAPublishedEndpoint(t *testing.T) {
	f := newAnchorOnlyFixture(t, 4_000, true)
	f.submitReceipt(t)
	state := f.sampleRound(t)
	require.True(t, state.Selected)
	require.Empty(t, state.DaUriPrefix)
	f.publishDA("http://203.0.113.7:50132/da/") // published after the draw: not applied to this audit
	f.openAndProveSampled(t, state)

	// An endpoint that is not a usable prefix is treated as none.
	for _, unusable := range []string{"http://a:99999/", "https://da.example/da", "file:///da/"} {
		g := newAnchorOnlyFixture(t, 4_000, true)
		g.publishDA(unusable)
		g.submitReceipt(t)
		require.Empty(t, g.sampleRound(t).DaUriPrefix, unusable)
	}
}

// The opening size is not refused at a bound an honest opening could reach.
func TestAnchorOpeningSizeHasOnlyASanityCeiling(t *testing.T) {
	f := newAnchorOnlyFixture(t, 4_000, true)
	f.submitReceipt(t)
	state := f.sampleRound(t)
	msg := f.openingMsg(state, f.worker)
	msg.OpeningSizeBytes = 64 << 30 // far above anything a validator caps at today
	_, err := f.server.SubmitAnchorOpening(f.at(int64(state.SampleHeight+1)), msg)
	require.NoError(t, err)
}
