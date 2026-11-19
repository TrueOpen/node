package keeper

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	"github.com/TrueOpen/node/x/task/types"
)

// With the content track off (the default) the bond guard does not apply: a
// Worker whose bond is below the minimum multiple of the order value is audited
// at the compute-track rate, not on every task.
func TestAnchorBondGuardIsOffWhileContentTrackIsOff(t *testing.T) {
	// R = 1000 (fixture order value), D = 30,000 < 40 R.
	f := newAnchorFixture(t, 30_000, anchorGoodTokenSize)
	f.setParams(t, func(v *types.VerificationParamsV1) { v.AnchorAuditMinRateBps = 1 })
	v := f.params.Verification
	require.Zero(t, v.AnchorContentVerdictWindowBlocks)
	require.Equal(t, types.DefaultAnchorMinBondMultiple, v.AnchorMinBondMultiple)
	state := f.sample(t)
	require.Equal(t, uint32(4_000), state.AuditRateBps, "s = kR/(D*b + kR) = 1000/(1500+1000)")
	require.Zero(t, state.ContentVerdictDeadlineHeight)
	require.Contains(t, state.AuditBasis, "(below false)")

	// The same task with the multiple set to 0 gets the same rate and the same coin.
	g := newAnchorFixture(t, 30_000, anchorGoodTokenSize)
	g.setParams(t, func(v *types.VerificationParamsV1) { v.AnchorAuditMinRateBps, v.AnchorMinBondMultiple = 1, 0 })
	other := g.sample(t)
	require.Equal(t, state.AuditRateBps, other.AuditRateBps)
	require.Equal(t, state.Selected, other.Selected)

	// With the track on the guard applies.
	h := newContentFixture(t, 30_000, anchorGoodTokenSize)
	h.setParams(t, func(v *types.VerificationParamsV1) { v.AnchorAuditMinRateBps = 1 })
	require.Equal(t, uint32(types.BasisPointsMaximum), h.sample(t).AuditRateBps)
}

// anchorOutcome is what a task's audit comes to: how it was sampled, the
// verdict settlement reads and the fault settlement would apply.
type anchorOutcome struct {
	Rate                               uint32
	Selected                           bool
	Positions                          []uint64
	OpenDeadline, ProofDeadline        uint64
	VerdictDeadline, ContentDeadline   uint64
	Held                               bool
	Verdict                            types.AnchorVerdictV1
	Source                             types.AnchorResultSourceV1
	FaultType                          string
	SlashBps                           uint32
	SlashAmount                        uint64
	SettlementDeadline, CleanupHoldFor uint64
}

// runAnchorScenario drives one Worker behaviour through sampling, the messages
// and the settlement view, and reports the outcome.
func runAnchorScenario(t *testing.T, f *anchorFixture, behaviour string) anchorOutcome {
	t.Helper()
	state := f.sample(t)
	out := anchorOutcome{Rate: state.AuditRateBps, Selected: state.Selected, Positions: state.Positions,
		OpenDeadline: state.OpeningDeadlineHeight, ProofDeadline: state.ProofDeadlineHeight,
		VerdictDeadline: state.VerdictDeadlineHeight, ContentDeadline: state.ContentVerdictDeadlineHeight}
	if state.Selected {
		switch behaviour {
		case "never opens":
		case "opens, never proves":
			f.open(t, state, f.sampleAt+1)
		case "proves, validators silent", "proves, validators pass", "proves, validators reject":
			f.open(t, state, f.sampleAt+1)
			_, err := f.server.SubmitAnchorProof(f.at(int64(f.sampleAt+2)), f.proofMsg(0))
			require.NoError(t, err)
			verdict := types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS
			if behaviour == "proves, validators reject" {
				verdict = types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT
			}
			if behaviour != "proves, validators silent" {
				for i := 0; i < 2; i++ {
					_, err := f.vote(i, f.valAcct[i], verdict, f.sampleAt+3)
					require.NoError(t, err)
				}
			}
		default:
			t.Fatalf("unknown behaviour %q", behaviour)
		}
	}
	pass := types.TaskVerdict_TASK_VERDICT_PASS
	_, err := f.keeper.anchorSettlementView(f.ctx, f.taskKey, pass, f.sampleAt+4)
	out.Held = err != nil
	view, err := f.keeper.anchorSettlementView(f.ctx, f.taskKey, pass, state.VerdictDeadlineHeight)
	require.NoError(t, err)
	if view != nil {
		out.Verdict, out.Source = view.TaskVerdict, view.ResultSource
		if view.TaskVerdict == types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT {
			out.FaultType, out.SlashBps = anchorWorkerFaultForVerdict(*view, f.params.Verification)
			out.SlashAmount = anchorSlashAmount(view.SlashableBond, out.SlashBps)
		}
	}
	summary := f.passRound(t, 150)
	out.SettlementDeadline, err = f.keeper.effectiveSettlementDeadline(f.ctx, f.taskKey, summary, f.params)
	require.NoError(t, err)
	out.CleanupHoldFor, err = f.keeper.anchorContentCleanupHold(f.ctx, f.taskKey, f.sampleAt+4)
	require.NoError(t, err)
	return out
}

// With the content verdict window at 0 the chain audits, judges and slashes
// exactly as the compute-only chain does. Every scenario runs under two params
// bodies - one recorded before the content-track fields existed (all four
// zero), one carrying their defaults with the track off - and under a third
// that also changes the bond multiple and the content slash; all three must
// agree with each other and with the compute-only rules stated here.
func TestAnchorContentTrackOffMatchesComputeOnly(t *testing.T) {
	bodies := map[string]func(v *types.VerificationParamsV1){
		"before the content-track fields": func(v *types.VerificationParamsV1) {
			v.AnchorPositionsPerTask, v.AnchorMinBondMultiple, v.AnchorContentVerdictWindowBlocks, v.AnchorContentRejectSlashBps = 0, 0, 0, 0
		},
		"content-track defaults, track off": func(v *types.VerificationParamsV1) {},
		"other multiple and content slash, track off": func(v *types.VerificationParamsV1) {
			v.AnchorMinBondMultiple, v.AnchorContentRejectSlashBps = 1_000, 9_999
		},
	}
	behaviours := []string{"never opens", "opens, never proves", "proves, validators silent", "proves, validators pass", "proves, validators reject"}
	// R = 1000: no bond (always audited), below 40 R, exactly 40 R, far above.
	for _, bond := range []uint64{0, 30_000, 40_000, 1_000_000_000} {
		for _, behaviour := range behaviours {
			name := fmt.Sprintf("bond %d, Worker %s", bond, behaviour)
			var reference *anchorOutcome
			for body, mutate := range bodies {
				f := newAnchorFixture(t, bond, anchorGoodTokenSize)
				mutate(&f.params.Verification)
				require.Zero(t, f.params.Verification.AnchorContentVerdictWindowBlocks)
				require.NoError(t, f.keeper.Params.Set(f.ctx, f.params))
				got := runAnchorScenario(t, f, behaviour)

				// The compute-only rules, computed independently of the draw.
				v := f.params.Verification
				wantRate := anchorAuditRateBps(1_000, bond, anchorDeterrentSlashBps(v), v.AnchorAuditRiskMarginBps, v.AnchorAuditMinRateBps)
				require.Equal(t, wantRate, got.Rate, "%s / %s: rate", name, body)
				coin := anchorAuditCoin(sdkChainID(f.ctx), f.taskKey, f.beacon)
				require.Equal(t, anchorCoinSelects(coin, wantRate), got.Selected, "%s / %s: selection", name, body)
				require.Equal(t, f.sampleAt+v.AnchorOpenWindowBlocks, got.OpenDeadline)
				require.Equal(t, got.OpenDeadline+v.AnchorProofWindowBlocks, got.ProofDeadline)
				require.Equal(t, got.ProofDeadline+v.AnchorVerdictWindowBlocks, got.VerdictDeadline)
				require.Zero(t, got.ContentDeadline, "%s / %s: no content track", name, body)
				require.Zero(t, got.CleanupHoldFor, "%s / %s: no content cleanup hold", name, body)
				if got.Selected {
					require.Len(t, got.Positions, anchorSamplePositionsV0, "%s / %s: the fixed two positions", name, body)
					wantFault, wantBps := "", uint32(0)
					switch behaviour {
					case "never opens", "opens, never proves":
						wantFault, wantBps = hubtypes.FaultTypeAnchorTimeout, v.AnchorTimeoutSlashBps
					case "proves, validators reject":
						wantFault, wantBps = hubtypes.FaultTypeAnchorReject, v.AnchorRejectSlashBps
					}
					require.Equal(t, wantFault, got.FaultType, "%s / %s: fault type", name, body)
					require.Equal(t, wantBps, got.SlashBps, "%s / %s: slash share", name, body)
					require.Equal(t, anchorSlashAmount(bond, wantBps), got.SlashAmount, "%s / %s: slash amount", name, body)
				} else {
					require.Empty(t, got.Positions)
					require.False(t, got.Held)
					require.Equal(t, anchorOutcome{Rate: got.Rate, OpenDeadline: got.OpenDeadline, ProofDeadline: got.ProofDeadline,
						VerdictDeadline: got.VerdictDeadline, SettlementDeadline: got.SettlementDeadline}, got, "%s / %s: an unselected task has no audit outcome", name, body)
				}
				if reference == nil {
					reference = &got
					continue
				}
				require.Equal(t, *reference, got, "%s: %s differs from the other params bodies", name, body)
			}
		}
	}
}

// A Worker may seal an epoch while the content track is off; the seal then only
// orders its own proof after x and changes neither the verdict nor the fault.
func TestAnchorSealWithContentTrackOffChangesNoOutcome(t *testing.T) {
	f := newAnchorFixture(t, 0, anchorGoodTokenSize)
	state := f.sample(t)
	require.Zero(t, state.ContentVerdictDeadlineHeight)
	f.open(t, state, f.sampleAt+1)
	resp, err := f.seal(f.sampleAt+2, f.worker)
	require.NoError(t, err)
	f.beacons[int64(resp.XBeaconHeight)] = bytes.Repeat([]byte{0x3c}, 32)
	sealed := f.audit(t)
	require.False(t, anchorOpenedNeverProved(sealed))
	view, err := f.keeper.anchorSettlementView(f.ctx, f.taskKey, types.TaskVerdict_TASK_VERDICT_PASS, state.VerdictDeadlineHeight)
	require.NoError(t, err)
	fault, bps := anchorWorkerFaultForVerdict(*view, f.params.Verification)
	require.Equal(t, hubtypes.FaultTypeAnchorTimeout, fault, "opened, sealed, never proved: still anchor_timeout off the content track")
	require.Equal(t, f.params.Verification.AnchorTimeoutSlashBps, bps)
	require.False(t, anchorContentOpen(sealed))
	_, err = f.contentVote(0, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_REJECT_CONTENT, nil, f.sampleAt+5)
	require.Error(t, err, "no content vote without a content track")
}
