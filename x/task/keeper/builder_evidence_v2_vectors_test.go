package keeper

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/wire/bus"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// The two envelopes are the protocol's linked V2 vectors, byte for
// byte. Their OPEN_VERIFY payload explicitly projects initial verify_round=1,
// while zero is the invalid/unspecified proto3 value. The current public V1
// surface opens no challenge round, so this is the only round with Task
// authority here.
//
// The protocol publishes an evidence_id and a fault_id for the tag 2 pair, which are
// reachable only if both envelopes clear that authority - the identities are
// derived from the canonical digest, and the canonical digest is only produced
// once the equivocation entry point has accepted both actions. So the fixture
// only holds together if tag 2 succeeds, and the assertions below are what
// proves it end to end, from published bytes to published fault_id.
//
// The tag 3 branch is the same envelope read against a Task that has no accepted
// InferReceipt: the Builder announced the verification round before there was an
// inference result to verify. That is the "premature OPEN_VERIFY" the contract
// names,
// and it is what WRONG_STAGE is for. The two branches therefore describe two
// different Task states, and the test walks them in that order - tag 3 first,
// then the receipt, then tag 2 - because the same envelope must stop being a
// WRONG_STAGE fault the moment the prerequisite exists.

func TestBuilderEvidenceV2LinkedWireVectors(t *testing.T) {
	fixture := loadBuilderEvidenceRC2Fixture(t)
	envelopeVectorA := fixture.envelope(t, "envelope_a")
	envelopeVectorB := fixture.envelope(t, "envelope_b")
	equivocationVector := fixture.linkedCase(t, "proposal equivocation")
	invalidStageVector := fixture.linkedCase(t, "invalid stage submission")
	f := newVerificationFixture(t)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithChainID("c"))
	taskID := bytes.Repeat([]byte{0x11}, types.Hash32Len)
	taskHash := bytes.Repeat([]byte{0x22}, types.Hash32Len)
	taskKey := types.NewTaskKey(taskID)
	builder := "trueopen1wltmkp6cpvulh9ya7z0hhw0cpgwsvsdccd5man"
	worker := "trueopen1j7r6u8nwvw93l2tc0wd75v07vu89lxyfqf8fut"
	proofKey, err := hex.DecodeString("03a37e2aa46416e86bfa50a931e2278c541ee1da298dcd79f63693089ffb26c765")
	require.NoError(t, err)
	canonicalBuilder, err := f.keeper.canonicalBusOperator(builder)
	require.NoError(t, err)
	canonicalWorker, err := f.keeper.canonicalBusOperator(worker)
	require.NoError(t, err)
	f.hub.builderProofOperator = canonicalBuilder
	f.hub.builderProofKey = proofKey
	f.taskID, f.taskKey = taskID, taskKey
	f.setTaskBuilders(t, canonicalBuilder)
	require.NoError(t, f.keeper.TaskCore.Set(f.ctx, taskKey, types.TaskCoreState{
		TaskId: taskID, AcceptedTaskHash: taskHash, ModelId: bytes.Repeat([]byte{0x55}, types.Hash32Len),
		TaskPhase: types.TaskPhase_TASK_PHASE_WORKER_ASSIGNED,
	}))
	require.NoError(t, f.keeper.WriteTaskAssignment(f.ctx, taskKey, types.TaskAssignmentState{
		TaskId: taskID, WinnerWorker: canonicalWorker,
	}))

	envelopeA := mustDecodeBuilderEvidenceHex(t, envelopeVectorA.EnvelopeBytes)
	envelopeB := mustDecodeBuilderEvidenceHex(t, envelopeVectorB.EnvelopeBytes)

	// The linkage being proved is that this keeper's own envelope verification
	// reproduces the published bus_signing_digest, because every canonical
	// evidence digest below is derived from it and from nothing else.
	verifiedA, err := f.keeper.verifyBuilderEvidenceEnvelope(f.ctx, "c", envelopeA)
	require.NoError(t, err)
	require.Equal(t, envelopeVectorA.BusSigningDigest, hex.EncodeToString(verifiedA.SigningDigest[:]))
	verifiedB, err := f.keeper.verifyBuilderEvidenceEnvelope(f.ctx, "c", envelopeB)
	require.NoError(t, err)
	require.Equal(t, envelopeVectorB.BusSigningDigest, hex.EncodeToString(verifiedB.SigningDigest[:]))

	// tag 3, against a Task with no accepted InferReceipt: the missing receipt is
	// the missing stage prerequisite, so the envelope carries the asserted
	// WRONG_STAGE violation. The equivocation branch is unreachable in this same
	// state, which is why the protocol gives the two branches different Task states.
	invalidFact, err := f.keeper.canonicalBuilderProtocolFault(f.ctx, "c", bus.SignedEnvelopeProtocolFaultV2{
		Envelope:  envelopeA,
		Violation: int32(types.BuilderProtocolViolation_BUILDER_PROTOCOL_VIOLATION_WRONG_STAGE),
	})
	require.NoError(t, err)
	require.Equal(t, shared.BuilderEvidenceKind_BUILDER_EVIDENCE_KIND_INVALID_STAGE_SUBMISSION, invalidFact.EvidenceKind)
	require.Equal(t, invalidStageVector.CanonicalEvidenceDigest, hex.EncodeToString(invalidFact.CanonicalEvidenceDigest))
	requireBuilderEvidenceIdentities(t, invalidFact,
		invalidStageVector.EvidenceID, invalidStageVector.FaultID)

	_, err = f.keeper.canonicalBuilderEquivocation(f.ctx, "c", bus.SignedEnvelopeEquivocationV2{
		EnvelopeA: envelopeA, EnvelopeB: envelopeB,
	})
	require.ErrorIs(t, err, errBuilderEvidenceWrongStage)

	// The accepted InferReceipt is the prerequisite the round announced. With it
	// in place both envelopes hold Task authority and tag 2 is reachable.
	require.NoError(t, f.keeper.WriteInferReceipt(f.ctx, taskKey, types.InferReceiptState{
		TaskId: taskID, WinnerWorker: canonicalWorker,
	}))
	equivocationDigest, err := bus.EquivocationDigest(bus.EquivocationContent{
		DigestA: verifiedA.SigningDigest[:], DigestB: verifiedB.SigningDigest[:],
	})
	require.NoError(t, err)
	require.Equal(t, equivocationVector.CanonicalEvidenceDigest, hex.EncodeToString(equivocationDigest[:]))
	swappedDigest, err := bus.EquivocationDigest(bus.EquivocationContent{
		DigestA: verifiedB.SigningDigest[:], DigestB: verifiedA.SigningDigest[:],
	})
	require.NoError(t, err)
	require.Equal(t, equivocationDigest, swappedDigest)

	equivocationFact, err := f.keeper.canonicalBuilderEquivocation(f.ctx, "c", bus.SignedEnvelopeEquivocationV2{
		EnvelopeA: envelopeA, EnvelopeB: envelopeB,
	})
	require.NoError(t, err)
	require.Equal(t, shared.BuilderEvidenceKind_BUILDER_EVIDENCE_KIND_PROPOSAL_EQUIVOCATION, equivocationFact.EvidenceKind)
	require.Equal(t, canonicalBuilder, equivocationFact.BuilderOperator)
	require.Equal(t, taskID, equivocationFact.ScopeId)
	require.Equal(t, equivocationVector.CanonicalEvidenceDigest, hex.EncodeToString(equivocationFact.CanonicalEvidenceDigest))
	requireBuilderEvidenceIdentities(t, equivocationFact,
		equivocationVector.EvidenceID, equivocationVector.FaultID)

	// The published pair is order-free at the entry point, not only at the digest
	// helper, so the submitter cannot mint a second fault by swapping arguments.
	swappedFact, err := f.keeper.canonicalBuilderEquivocation(f.ctx, "c", bus.SignedEnvelopeEquivocationV2{
		EnvelopeA: envelopeB, EnvelopeB: envelopeA,
	})
	require.NoError(t, err)
	require.Equal(t, equivocationFact, swappedFact)

	// Same envelope, same asserted violation, prerequisite now present: the
	// WRONG_STAGE claim is no longer true of it. This is what keeps the tag 3
	// vector meaning "premature", rather than "OPEN_VERIFY is always a fault".
	_, err = f.keeper.canonicalBuilderProtocolFault(f.ctx, "c", bus.SignedEnvelopeProtocolFaultV2{
		Envelope:  envelopeA,
		Violation: int32(types.BuilderProtocolViolation_BUILDER_PROTOCOL_VIOLATION_WRONG_STAGE),
	})
	require.Error(t, err)
	require.False(t, errors.Is(err, errBuilderEvidenceWrongStage))
}

// requireBuilderEvidenceIdentities derives the published evidence_id and
// fault_id from a canonical fact exactly as the Hub fault kernel does. The
// derivation is a pure function of the fact, so asserting it here proves the
// Task-side fact is the one the published identities were computed from without
// standing up a Hub fixture.
func requireBuilderEvidenceIdentities(t *testing.T, fact shared.BuilderObjectiveEvidenceFactV2, evidenceIDHex, faultIDHex string) {
	t.Helper()
	var content [32]byte
	require.Len(t, fact.CanonicalEvidenceDigest, 32)
	copy(content[:], fact.CanonicalEvidenceDigest)
	evidenceID, err := bus.EvidenceID("c", fact.BuilderOperator, int32(fact.EvidenceKind), fact.ScopeId, content)
	require.NoError(t, err)
	require.Equal(t, evidenceIDHex, hex.EncodeToString(evidenceID[:]))
	faultID, err := bus.FaultID("c", fact.BuilderOperator, int32(fact.EvidenceKind), fact.ScopeId, content)
	require.NoError(t, err)
	require.Equal(t, faultIDHex, hex.EncodeToString(faultID[:]))
}

func mustDecodeBuilderEvidenceHex(t *testing.T, value string) []byte {
	t.Helper()
	raw, err := hex.DecodeString(value)
	require.NoError(t, err)
	return raw
}

type builderEvidenceRC2Fixture struct {
	BuilderEvidence struct {
		Envelopes []struct {
			Name             string `json:"name"`
			EnvelopeBytes    string `json:"envelope_bytes"`
			BusSigningDigest string `json:"bus_signing_digest"`
		} `json:"envelopes"`
		LinkedCases []struct {
			Name                    string `json:"name"`
			CanonicalEvidenceDigest string `json:"canonical_evidence_digest"`
			EvidenceID              string `json:"evidence_id"`
			FaultID                 string `json:"fault_id"`
		} `json:"linked_cases"`
	} `json:"builder_evidence"`
}

func loadBuilderEvidenceRC2Fixture(t *testing.T) builderEvidenceRC2Fixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/bus_envelope_v2_vectors.json")
	require.NoError(t, err)
	var fixture builderEvidenceRC2Fixture
	require.NoError(t, json.Unmarshal(raw, &fixture))
	return fixture
}

func (f builderEvidenceRC2Fixture) envelope(t *testing.T, name string) struct {
	Name             string `json:"name"`
	EnvelopeBytes    string `json:"envelope_bytes"`
	BusSigningDigest string `json:"bus_signing_digest"`
} {
	t.Helper()
	for _, envelope := range f.BuilderEvidence.Envelopes {
		if envelope.Name == name {
			return envelope
		}
	}
	t.Fatalf("missing RC2 Bus envelope %q", name)
	return struct {
		Name             string `json:"name"`
		EnvelopeBytes    string `json:"envelope_bytes"`
		BusSigningDigest string `json:"bus_signing_digest"`
	}{}
}

func (f builderEvidenceRC2Fixture) linkedCase(t *testing.T, name string) struct {
	Name                    string `json:"name"`
	CanonicalEvidenceDigest string `json:"canonical_evidence_digest"`
	EvidenceID              string `json:"evidence_id"`
	FaultID                 string `json:"fault_id"`
} {
	t.Helper()
	for _, linked := range f.BuilderEvidence.LinkedCases {
		if linked.Name == name {
			return linked
		}
	}
	t.Fatalf("missing RC2 Bus linked case %q", name)
	return struct {
		Name                    string `json:"name"`
		CanonicalEvidenceDigest string `json:"canonical_evidence_digest"`
		EvidenceID              string `json:"evidence_id"`
		FaultID                 string `json:"fault_id"`
	}{}
}
