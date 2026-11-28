package keeper

import (
	"bytes"
	"context"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// panelHubState is the mutable Hub view of the panel tests.
type panelHubState struct {
	// slots maps a candidate-pool slot to its operator.
	slots map[uint32]string
	// notAudit, stale, lowBond, waiting, tombstoned switch one eligibility leg
	// off for an operator.
	notAudit, stale, lowBond, waiting, tombstoned map[string]bool
	// score overrides an operator's performance score (ppm).
	score map[string]uint64
	// duties counts pending stage duties per operator.
	duties map[string]int
	// beaconRefs counts beacon references per height.
	beaconRefs map[uint64]int
	// noBeacon withholds the beacon of a height.
	noBeacon   map[int64]bool
	separation bool
	badSig     map[string]bool
	// paused holds the dispatch pause reasons per operator.
	paused map[string]uint32
}

// panelHubStub adds a populated candidate pool and audit-capable operators to
// the anchor-only Hub view.
type panelHubStub struct {
	anchorOnlyHubStub
	state *panelHubState
}

func (s panelHubStub) GetBeaconForDomain(ctx sdk.Context, domain string, height int64) (hubtypes.BeaconSnapshot, bool) {
	if s.state.noBeacon[height] {
		return hubtypes.BeaconSnapshot{}, false
	}
	return s.anchorOnlyHubStub.GetBeaconForDomain(ctx, domain, height)
}

func (s panelHubStub) AcquireBeaconConsumerRef(_ context.Context, height uint64, _ hubtypes.BeaconConsumerKind, _ string) error {
	s.state.beaconRefs[height]++
	return nil
}

func (s panelHubStub) ReleaseBeaconConsumerRef(_ context.Context, height uint64, _ hubtypes.BeaconConsumerKind, _ string) error {
	s.state.beaconRefs[height]--
	return nil
}

func (s panelHubStub) SetWorkerDispatchPause(_ context.Context, operator string, reason uint32, paused bool) (uint32, bool, error) {
	before := s.state.paused[operator]
	after := before &^ reason
	if paused {
		after = before | reason
	}
	s.state.paused[operator] = after
	return after, after != before, nil
}

func (s panelHubStub) AcquirePendingStageDuty(_ context.Context, operator string) error {
	s.state.duties[operator]++
	return nil
}

func (s panelHubStub) ReleasePendingStageDuty(_ context.Context, operator string) error {
	s.state.duties[operator]--
	return nil
}

func (s panelHubStub) GetCandidatePoolLayout(context.Context, []byte) (uint32, uint32, uint32, bool) {
	return s.pool.SlotCapacity, uint32(len(s.poolBitmap)), 1, true
}

func (s panelHubStub) ResolveCandidatePoolMember(_ context.Context, _ []byte, slot uint32) (hubtypes.CandidatePoolMemberState, hubtypes.CandidateSlotBindingState, error) {
	operator, ok := s.state.slots[slot]
	if !ok {
		return hubtypes.CandidatePoolMemberState{}, hubtypes.CandidateSlotBindingState{}, context.Canceled
	}
	binding := bytes.Repeat([]byte{byte(0x80 + slot)}, types.Hash32Len)
	return hubtypes.CandidatePoolMemberState{Slot: slot, SlotVersion: 1, OperatorAddress: operator, BindingHash: binding},
		hubtypes.CandidateSlotBindingState{Slot: slot, SlotVersion: 1, OperatorAddress: operator, BindingHash: binding}, nil
}

func (panelHubStub) GetModelStatus(sdk.Context, []byte) hubtypes.ModelStatus {
	return hubtypes.ModelStatusActive
}
func (panelHubStub) IsProfileFrozen(sdk.Context, []byte, uint32) bool { return false }
func (s panelHubStub) GetNodeTombstone(_ sdk.Context, address sdk.AccAddress) bool {
	return s.state.tombstoned[address.String()]
}
func (panelHubStub) GetNodeJailStatus(sdk.Context, sdk.AccAddress, string) hubtypes.JailStatus {
	return hubtypes.JailStatusNone
}

func (s panelHubStub) GetModelCapability(_ sdk.Context, address sdk.AccAddress, modelID []byte) (hubtypes.ModelCapabilitySnapshot, bool) {
	operator := address.String()
	snapshot := hubtypes.ModelCapabilitySnapshot{
		OperatorAddress: operator, ModelID: modelID, CapabilityVersion: 1, AuditCapability: !s.state.notAudit[operator],
		// An operator that is not audit capable is a plain Verifier.
		VerificationCapability: s.state.notAudit[operator],
	}
	if s.state.waiting[operator] {
		snapshot.AuditEffectiveFromEpoch = 1 << 40
	}
	return snapshot, true
}

func (s panelHubStub) GetModelSupport(_ sdk.Context, address sdk.AccAddress, modelID []byte) (hubtypes.ModelSupportSnapshot, bool) {
	operator := address.String()
	fresh := uint64(1 << 40)
	if s.state.stale[operator] {
		fresh = 0
	}
	return hubtypes.ModelSupportSnapshot{
		OperatorAddress: operator, ModelID: modelID, DeclaredSupport: true, SupportFreshUntilEpoch: fresh, SupportVersion: 1,
	}, true
}

func (s panelHubStub) GetServiceBond(_ sdk.Context, address sdk.AccAddress, _ uint64) (hubtypes.ServiceBondSnapshot, bool) {
	bond := uint64(1_000_000)
	if s.state.lowBond[address.String()] {
		bond = 0
	}
	return hubtypes.ServiceBondSnapshot{
		OperatorAddress: address.String(), Status: hubtypes.ServiceBondStatusActive,
		ActiveBond: bond, EffectiveActiveBond: bond, AvailableBond: bond, BondVersion: 1,
	}, true
}

func (s panelHubStub) GetRoleScoringSnapshot(_ sdk.Context, address sdk.AccAddress, _ string) (hubtypes.RoleScoringSnapshot, error) {
	score := uint64(hubtypes.PerformanceScoreDefaultPpm)
	if override, ok := s.state.score[address.String()]; ok {
		score = override
	}
	return hubtypes.RoleScoringSnapshot{PerformanceScorePpm: score, PerformanceVersion: hubtypes.PerformanceScoreMethodVersionV1}, nil
}

func (s panelHubStub) VerifyCurrentCortexServiceDigest(_ context.Context, operator string, _, _ []byte, _ uint64) error {
	if s.state.badSig[operator] {
		return context.Canceled
	}
	return nil
}

func (s panelHubStub) GetHubParams(ctx sdk.Context) hubtypes.HubParamsSnapshot {
	params := s.anchorOnlyHubStub.GetHubParams(ctx)
	params.MinValidatorSeparation = s.state.separation
	return params
}

type anchorPanelFixture struct {
	*anchorOnlyFixture
	hubState   *panelHubState
	candidates []string // audit-capable operators, by ascending slot
	audit      types.AnchorAuditState
}

const (
	panelVoteWindow  = uint64(20)
	panelPublication = uint64(50)
)

// newAnchorPanelFixture stages an anchor-only task under the panel audit mode
// with `candidates` audit-capable operators in the task's candidate pool next
// to the Worker, and takes it to the recorded proof: the first panel round is
// open.
func newAnchorPanelFixture(t *testing.T, bond uint64, candidates int) *anchorPanelFixture {
	t.Helper()
	f := newAnchorPanelFixtureBeforeProof(t, bond, candidates)
	f.openAndProveSampled(t, f.audit)
	f.reload(t)
	return f
}

func newAnchorPanelFixtureBeforeProof(t *testing.T, bond uint64, candidates int) *anchorPanelFixture {
	t.Helper()
	return newTunedAnchorPanelFixture(t, bond, candidates, func(*types.TaskParamsV1) {})
}

// newTunedAnchorPanelFixture is newAnchorPanelFixtureBeforeProof with the
// task's parameters adjusted by tune before the task freezes them.
func newTunedAnchorPanelFixture(t *testing.T, bond uint64, candidates int, tune func(*types.TaskParamsV1)) *anchorPanelFixture {
	t.Helper()
	base := newAnchorOnlyFixture(t, bond, true)
	state := &panelHubState{
		slots: map[uint32]string{0: base.worker}, notAudit: map[string]bool{}, stale: map[string]bool{}, lowBond: map[string]bool{},
		waiting: map[string]bool{}, tombstoned: map[string]bool{}, score: map[string]uint64{}, duties: map[string]int{},
		beaconRefs: map[uint64]int{}, noBeacon: map[int64]bool{}, badSig: map[string]bool{}, paused: map[string]uint32{},
	}
	f := &anchorPanelFixture{anchorOnlyFixture: base, hubState: state}
	base.hub.pool.SlotCapacity = 16
	base.hub.poolBitmap = make([]byte, 2)
	base.hub.poolBitmap[0] |= 1
	for i := 0; i < candidates; i++ {
		slot := uint32(i + 1)
		operator := sdk.AccAddress(bytes.Repeat([]byte{byte(0x40 + i)}, 20)).String()
		state.slots[slot] = operator
		f.candidates = append(f.candidates, operator)
		base.hub.poolBitmap[slot/8] |= 1 << (slot % 8)
	}
	base.hub.pool.ActiveCount = uint32(candidates + 1)
	base.keeper.hubKeeper = panelHubStub{anchorOnlyHubStub: base.keeper.hubKeeper.(anchorOnlyHubStub), state: state}
	base.server = msgServer{k: base.keeper}

	base.params.Verification.AnchorAuditMode = types.AnchorAuditModePanel
	base.params.Verification.AnchorPanelSize = 3
	base.params.Verification.AnchorPanelThreshold = 2
	base.params.Verification.AnchorPanelVoteWindowBlocks = panelVoteWindow
	base.params.Verification.AnchorPublicationWindowBlocks = panelPublication
	tune(&base.params)
	require.NoError(t, base.params.Validate())
	require.NoError(t, base.keeper.Params.Set(base.ctx, base.params))

	base.submitReceipt(t)
	f.audit = base.sampleRound(t)
	// The task's own verification round holds beacon references of its own
	// until cleanup; only the panel's are counted from here.
	for height := range state.beaconRefs {
		delete(state.beaconRefs, height)
	}
	require.True(t, f.audit.Selected)
	require.Equal(t, types.AnchorAuditModePanel, f.audit.AuditMode)
	return f
}

func (f *anchorPanelFixture) reload(t *testing.T) types.AnchorAuditState {
	t.Helper()
	audit, err := f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	f.audit = audit
	return audit
}

func (f *anchorPanelFixture) round(t *testing.T) types.AnchorPanelRoundState {
	t.Helper()
	audit := f.reload(t)
	require.NotEmpty(t, audit.PanelRounds)
	return audit.PanelRounds[len(audit.PanelRounds)-1]
}

// sweep runs the panel queue at height and returns how many audits it visited.
func (f *anchorPanelFixture) sweep(t *testing.T, height uint64) uint64 {
	t.Helper()
	visited, err := f.keeper.processAnchorPanelDue(f.at(int64(height)), height, 10)
	require.NoError(t, err)
	return visited
}

func (f *anchorPanelFixture) selection(t *testing.T) anchorPanelSelection {
	t.Helper()
	selection, found, err := f.keeper.loadAnchorPanelSelection(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.True(t, found, "the open round must have a working selection row")
	return selection
}

func (f *anchorPanelFixture) dueHeight(t *testing.T) uint64 {
	t.Helper()
	height, err := f.keeper.AnchorPanelDueHeight.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	return height
}

// handraises builds the signed hand-raises of the given window members.
func (f *anchorPanelFixture) handraises(t *testing.T, selection anchorPanelSelection, operators ...string) []types.VerifierHandraiseV1 {
	t.Helper()
	receipt, err := f.keeper.ReadInferReceipt(f.ctx, f.taskKey)
	require.NoError(t, err)
	core, err := f.keeper.TaskCore.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	wanted := map[string]bool{}
	for _, operator := range operators {
		wanted[operator] = true
	}
	var out []types.VerifierHandraiseV1
	members := append([]types.VerifierCandidateWindowMemberState(nil), selection.Members...)
	for slot := uint32(0); slot < 16; slot++ {
		for _, member := range members {
			if member.Slot != slot || (len(operators) != 0 && !wanted[member.OperatorAddress]) {
				continue
			}
			out = append(out, types.VerifierHandraiseV1{
				SchemaVersion: types.VerifierHandraiseSchemaVersionV1, ChainId: sdkChainID(f.ctx), TaskId: f.taskID,
				VerifyRound: selection.PanelRound, InferReceiptHash: receipt.InferReceiptHash, OutputHash: receipt.OutputHash,
				ModelId: core.ModelId, ProfileVersion: core.ProfileVersion,
				Member: types.CandidateMemberRefV1{
					CandidatePoolSnapshotId: selection.Window.CandidatePoolSnapshotId, Slot: member.Slot,
					SlotVersion: member.SlotVersion, OperatorAddress: member.OperatorAddress,
				},
				Duty: shared.Duty_DUTY_VERIFIER, ServiceAuthorizationNonce: f.hub.nonce,
				ExpiryHeight: selection.Window.HandraiseCloseHeight, ServiceSignature: make([]byte, 64),
			})
		}
	}
	return out
}

// assign takes the open round from its opening to its drawn panel: the window
// is materialized, every window member raises its hand through a Builder, and
// the panel is drawn. It returns the round with its members.
func (f *anchorPanelFixture) assign(t *testing.T) types.AnchorPanelRoundState {
	t.Helper()
	selection := f.selection(t)
	require.Equal(t, uint64(1), f.sweep(t, selection.Window.WindowRandomnessHeight))
	selection = f.selection(t)
	require.Equal(t, types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY, selection.Window.Status)
	_, err := f.server.SubmitVerifierHandraises(f.at(int64(selection.Window.HandraiseCloseHeight)), &types.MsgSubmitVerifierHandraises{
		TaskId: f.taskID, Handraises: f.handraises(t, selection), SubmitterAddress: f.builders[0],
	})
	require.NoError(t, err)
	require.Equal(t, uint64(1), f.sweep(t, selection.Window.SelectionRandomnessHeight))
	round := f.round(t)
	require.Len(t, round.Members, 3)
	require.Equal(t, selection.Window.SelectionRandomnessHeight, round.AssignmentHeight)
	require.Equal(t, round.AssignmentHeight+panelVoteWindow, round.VoteDeadlineHeight)
	return round
}

func (f *anchorPanelFixture) voteMsg(round types.AnchorPanelRoundState, member string, verdict types.AnchorVerdictV1) *types.MsgSubmitAnchorPanelVote {
	msg := &types.MsgSubmitAnchorPanelVote{
		TaskId: f.taskID, PanelRound: round.PanelRound, OpeningHash: f.audit.OpeningHash, ProofHash: f.audit.WorkerProofHash,
		Verdict: verdict, VerifierOperatorAddress: member, SubmitterAddress: member,
	}
	if verdict != types.AnchorVerdictV1_ANCHOR_VERDICT_V1_UNAVAILABLE {
		for i, position := range f.audit.Positions {
			positionVerdict := types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS
			if verdict == types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT && i == 0 {
				positionVerdict = types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT
			}
			msg.Verdicts = append(msg.Verdicts, types.AnchorPositionVerdictV1{Position: position, Verdict: positionVerdict})
		}
	}
	return msg
}

// vote casts the listed verdicts, one per member in member order, at height.
func (f *anchorPanelFixture) castVotes(t *testing.T, round types.AnchorPanelRoundState, height uint64, verdicts ...types.AnchorVerdictV1) *types.MsgSubmitAnchorPanelVoteResponse {
	t.Helper()
	var last *types.MsgSubmitAnchorPanelVoteResponse
	for i, verdict := range verdicts {
		response, err := f.server.SubmitAnchorPanelVote(f.at(int64(height)), f.voteMsg(round, round.Members[i], verdict))
		require.NoError(t, err)
		last = response
	}
	f.reload(t)
	return last
}

// requireNoPanelWork asserts that nothing of the panel's working state is left.
func (f *anchorPanelFixture) requireNoPanelWork(t *testing.T) {
	t.Helper()
	audit := f.reload(t)
	has, err := f.keeper.anchorPanelHasWork(f.ctx, f.taskKey, audit)
	require.NoError(t, err)
	require.False(t, has, "a final audit keeps no panel working row or index")
	for operator, count := range f.hubState.duties {
		require.Zerof(t, count, "pending stage duty of %s must be released", operator)
	}
	for height, count := range f.hubState.beaconRefs {
		require.Zerof(t, count, "beacon reference at %d must be released", height)
	}
}

var (
	votePass        = types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS
	voteReject      = types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT
	voteUnavailable = types.AnchorVerdictV1_ANCHOR_VERDICT_V1_UNAVAILABLE
	noVerdict       = types.AnchorVerdictV1_ANCHOR_VERDICT_V1_NO_VERDICT
)
