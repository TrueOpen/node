package types

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/wire/dispatch"
	"github.com/cosmos/gogoproto/jsonpb"
	"github.com/stretchr/testify/require"
)

func TestDispatchParamsEnforceRotationSafety(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*TaskParamsV1)
	}{
		{"record exceeds clamp", func(p *TaskParamsV1) { p.Dispatch.RecordDeadlineBlocks = 61 }},
		{"claim exceeds clamp", func(p *TaskParamsV1) { p.Dispatch.DispatchCompensationClaimWindowBlocks = 121 }},
		{"rebuttal exceeds clamp", func(p *TaskParamsV1) { p.Dispatch.DispatchCompensationRebuttalBlocks = 61 }},
		{"challenge exceeds clamp", func(p *TaskParamsV1) { p.Dispatch.DispatchChallengeWindowBlocks = 121 }},
		{"response exceeds clamp", func(p *TaskParamsV1) { p.Dispatch.DispatchChallengeResponseBlocks = 61 }},
		{"withdraw races record", func(p *TaskParamsV1) { p.Session.SessionWithdrawDelayBlocks = p.Dispatch.RecordDeadlineBlocks }},
		{"key change races record", func(p *TaskParamsV1) { p.Session.SessionChangeDelayBlocks = p.Dispatch.RecordDeadlineBlocks }},
		{"stale admission", func(p *TaskParamsV1) { p.Dispatch.OrderFreshnessMarginBlocks = p.Dispatch.RecordDeadlineBlocks }},
		{"loses exclusion facts", func(p *TaskParamsV1) { p.Dispatch.DispatchRecordRetentionBlocks = 300 }},
		{"loses prerecord evidence", func(p *TaskParamsV1) { p.Dispatch.DispatchEvidenceRetentionBlocks = 239 }},
		{"missing penalty kind", func(p *TaskParamsV1) {
			p.Dispatch.DispatchFaultPenaltyByKind = p.Dispatch.DispatchFaultPenaltyByKind[:4]
		}},
		{"duplicate penalty kind", func(p *TaskParamsV1) {
			p.Dispatch.DispatchFaultPenaltyByKind[1].Kind = p.Dispatch.DispatchFaultPenaltyByKind[0].Kind
		}},
		{"missing gas cap", func(p *TaskParamsV1) { p.Dispatch.DispatchChallengeGasCapByKind[0] = nil }},
		{"invalid slash", func(p *TaskParamsV1) { p.Dispatch.WorkerDispatchContradictionSlashBps = 10001 }},
		{"value evidence disabled", func(p *TaskParamsV1) { p.Evidence.VerifierValueEvidenceWindowBlocks = 0 }},
		{"value evidence outlives retained facts", func(p *TaskParamsV1) {
			p.Evidence.VerifierValueEvidenceWindowBlocks = p.Evidence.MaxEvidenceRetentionBlocks + 1
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := DefaultTaskParams()
			require.NoError(t, p.Validate())
			tc.mutate(&p)
			require.Error(t, p.Validate())
		})
	}
	// Live windows within their clamps do not change the fixed activation.
	// All five maxima must remain valid together.
	p := DefaultTaskParams()
	limits := shared.DispatchRotationWindowHardLimits()
	p.Dispatch.RecordDeadlineBlocks = limits.RecordDeadlineBlocks
	p.Dispatch.DispatchCompensationClaimWindowBlocks = limits.DispatchCompensationClaimWindowBlocks
	p.Dispatch.DispatchCompensationRebuttalBlocks = limits.DispatchCompensationRebuttalBlocks
	p.Dispatch.DispatchChallengeWindowBlocks = limits.DispatchChallengeWindowBlocks
	p.Dispatch.DispatchChallengeResponseBlocks = limits.DispatchChallengeResponseBlocks
	require.NoError(t, p.Validate())
	height, err := dispatch.ServiceKeyRotationHeight(120, 120, 120)
	require.NoError(t, err)
	require.Equal(t, uint64(360), height)
	interval, err := dispatch.ServiceKeyRotationInterval(limits)
	require.NoError(t, err)
	require.Equal(t, uint64(241), interval)
	allowed, err := dispatch.CanRegisterServiceKeyRotation(601, 360, 1, limits)
	require.NoError(t, err)
	require.False(t, allowed)
	allowed, err = dispatch.CanRegisterServiceKeyRotation(601, 360, 0, limits)
	require.NoError(t, err)
	require.True(t, allowed)
}

func TestPhase1SeedParameterSafety(t *testing.T) {
	for _, name := range []string{"localnet_genesis_seed.json", "testnet_genesis_seed.json"} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", "..", "config", name))
			require.NoError(t, err)
			var root map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &root))
			var p TaskParamsV1
			require.NoError(t, jsonpb.Unmarshal(bytes.NewReader(root["task_params"]), &p))
			require.NoError(t, p.Validate())
			_, err = TaskParamsHashV1("trueopen-phase1-seed", 1, p)
			require.NoError(t, err)
			var hub hubtypes.HubParamsV2
			require.NoError(t, jsonpb.Unmarshal(bytes.NewReader(root["hub_params"]), &hub))
			require.NoError(t, hub.Validate())
			require.NoError(t, ValidateVerifyOpenClock(p.Deadlines, hub.Epoch.DeltaWBlocks, hub.Builder.OpenVerifyBuilderProposalWindowBlocks))
			require.NoError(t, ValidateSessionCloseHorizon(p, hubtypes.ProfileChallengeOpenWindowMaxBlocks))
			require.Equal(t, uint64(120), hub.Epoch.EpochLengthBlocks)
			require.Less(t, hub.CandidatePool.CandidatePoolBuildLeadBlocks, hub.Epoch.EpochLengthBlocks)
			require.Equal(t, uint64(30), p.Session.SessionWithdrawDelayBlocks)
			require.Equal(t, uint64(30), p.Session.SessionChangeDelayBlocks)
		})
	}
}

func TestPhase1ParameterCommitmentIncludesNewSections(t *testing.T) {
	base, err := TaskParamsHashV1("trueopen-phase1-params", 1, DefaultTaskParams())
	require.NoError(t, err)
	for _, tc := range []struct {
		name   string
		mutate func(*TaskParamsV1)
	}{
		{"prefund", func(p *TaskParamsV1) { p.Session.SessionMinPrefund = shared.NewAmount(2) }},
		{"session cap", func(p *TaskParamsV1) { p.Session.MaxActiveSessionsPerAccount++ }},
		{"record window", func(p *TaskParamsV1) { p.Dispatch.RecordDeadlineBlocks++ }},
		{"prefill amount", func(p *TaskParamsV1) { p.Dispatch.PrefillCompensationByModelTier[0].Amount = shared.NewAmount(1001) }},
		{"fault penalty", func(p *TaskParamsV1) { p.Dispatch.DispatchFaultPenaltyByKind[0].Amount = shared.NewAmount(10001) }},
		{"challenge gas", func(p *TaskParamsV1) { p.Dispatch.DispatchChallengeGasCapByKind[0].GasCap++ }},
		{"compensation floor", func(p *TaskParamsV1) { p.Dispatch.BuilderCompensationAccountMinBalance = shared.NewAmount(1_000_001) }},
		{"worker slash", func(p *TaskParamsV1) { p.Dispatch.WorkerDispatchContradictionSlashBps++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := DefaultTaskParams()
			tc.mutate(&p)
			digest, err := TaskParamsHashV1("trueopen-phase1-params", 1, p)
			require.NoError(t, err)
			require.NotEqual(t, base, digest)
		})
	}
}
