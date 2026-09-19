package keeper

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/wire/bus"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// The two envelopes are keeper_api_contract.md §5.5's linked V2 vectors, byte for
// byte. Their OPEN_VERIFY payload explicitly projects initial verify_round=1,
// while zero is the invalid/unspecified proto3 value. The current public V1
// surface opens no challenge round, so this is the only round with Task
// authority here.
//
// §5.5 publishes an evidence_id and a fault_id for the tag 2 pair, which are
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
const (
	builderEvidenceEnvelopeAHex = "08011201631a55747275656f70656e2e7665726966792e6f70656e2e3131313131313131313131313131313131313131313131313131313131313131313131313131313131313131313131313131313131313131313131313131313120052802322f747275656f70656e31776c746d6b7036637076756c68397961377a30686877306370677773767364636364356d616e380142016d4a2000000000000000000000000000000000000000000000000000000000000000005001580260056ac0010a201111111111111111111111111111111111111111111111111111111111111111122022222222222222222222222222222222222222222222222222222222222222221a016d20012a203333333333333333333333333333333333333333333333333333333333333333322044444444444444444444444444444444444444444444444444444444444444443a2f747275656f70656e316a37723675386e77767739336c3274633077643735763037767538396c78796671663866757440017220f480d0ceb9f4ea4c4e71782c68f658bc52bb3a688ef9195354ce50d29f86b4887a402500b47afe78c9988560fd2631eed80f32250a15ceff653b454e158a3a5348981deec6c1825a4ee97cfde880c442f2872e8fb2a6aea7ec7725252f3f1bad47ae"
	builderEvidenceEnvelopeBHex = "08011201631a55747275656f70656e2e7665726966792e6f70656e2e3131313131313131313131313131313131313131313131313131313131313131313131313131313131313131313131313131313131313131313131313131313120052802322f747275656f70656e31776c746d6b7036637076756c68397961377a30686877306370677773767364636364356d616e380142016d4a2000000000000000000000000000000000000000000000000000000000000000005001580260056ac0010a201111111111111111111111111111111111111111111111111111111111111111122022222222222222222222222222222222222222222222222222222222222222221a016d20012a203333333333333333333333333333333333333333333333333333333333333333322055555555555555555555555555555555555555555555555555555555555555553a2f747275656f70656e316a37723675386e77767739336c3274633077643735763037767538396c787966716638667574400172206d8b6166cc7e2c86868bccd393f38aec56e2b94547f6f093d5c2c6cdce5461f07a40fca7cf7fcdc19030c7f624fe5a72d644dbd06c537dd4b83295bc1d307298cecc7d3c919eafb151792af66174edbce6e1c0fbb869678f02813061dae571d81234"

	builderEvidenceSigningDigestAHex = "b2af1c1eedbac2e4f485d0d6ecb30ab38fa60514fb9c418444ab56249fb42308"
	builderEvidenceSigningDigestBHex = "c1a710f675c939831419a4c0bdb5a553cc3072c3de3bd1645c60a10a53863b5a"

	builderEvidenceEquivocationDigestHex     = "97334840b80ec03254bfbc036f41863584b815bf92b13778e90d75275061bdf2"
	builderEvidenceEquivocationEvidenceIDHex = "0ac34327349a5e2cb6bca4715f71d6b6f9e8a31b7ddc496e6d62c12105064829"
	builderEvidenceEquivocationFaultIDHex    = "e3221cd50bce83b35f2de8175d6fae77a7866071af537f82d39b74e677a2db43"

	builderEvidenceInvalidStageDigestHex     = "4ba385dbd23479fc4d3520a5022dc76eb534abfcff8728943058fd13691c68b3"
	builderEvidenceInvalidStageEvidenceIDHex = "e48a1f0bc87a84e5555bd5b203a60ebd56d48a72e5f2d9e3badb632f70c6415e"
	builderEvidenceInvalidStageFaultIDHex    = "5ad4c58981863765a16ff9f4dcfeecc5d6c18ca9c444fc448ad612a7a64a22e3"
)

func TestBuilderEvidenceV2LinkedWireVectors(t *testing.T) {
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
		TaskId: taskID, AcceptedTaskHash: taskHash, ModelId: "m",
		TaskPhase: types.TaskPhase_TASK_PHASE_WORKER_ASSIGNED,
	}))
	require.NoError(t, f.keeper.TaskAssignment.Set(f.ctx, taskKey, types.TaskAssignmentState{
		TaskId: taskID, WinnerWorker: canonicalWorker,
	}))

	envelopeA := mustDecodeBuilderEvidenceHex(t, builderEvidenceEnvelopeAHex)
	envelopeB := mustDecodeBuilderEvidenceHex(t, builderEvidenceEnvelopeBHex)

	// The linkage being proved is that this keeper's own envelope verification
	// reproduces the published bus_signing_digest, because every canonical
	// evidence digest below is derived from it and from nothing else.
	verifiedA, err := f.keeper.verifyBuilderEvidenceEnvelope(f.ctx, "c", envelopeA)
	require.NoError(t, err)
	require.Equal(t, builderEvidenceSigningDigestAHex, hex.EncodeToString(verifiedA.SigningDigest[:]))
	verifiedB, err := f.keeper.verifyBuilderEvidenceEnvelope(f.ctx, "c", envelopeB)
	require.NoError(t, err)
	require.Equal(t, builderEvidenceSigningDigestBHex, hex.EncodeToString(verifiedB.SigningDigest[:]))

	// tag 3, against a Task with no accepted InferReceipt: the missing receipt is
	// the missing stage prerequisite, so the envelope carries the asserted
	// WRONG_STAGE violation. The equivocation branch is unreachable in this same
	// state, which is why §5.5 gives the two branches different Task states.
	invalidFact, err := f.keeper.canonicalBuilderProtocolFault(f.ctx, "c", bus.SignedEnvelopeProtocolFaultV2{
		Envelope:  envelopeA,
		Violation: int32(types.BuilderProtocolViolation_BUILDER_PROTOCOL_VIOLATION_WRONG_STAGE),
	})
	require.NoError(t, err)
	require.Equal(t, shared.BuilderEvidenceKind_BUILDER_EVIDENCE_KIND_INVALID_STAGE_SUBMISSION, invalidFact.EvidenceKind)
	require.Equal(t, builderEvidenceInvalidStageDigestHex, hex.EncodeToString(invalidFact.CanonicalEvidenceDigest))
	requireBuilderEvidenceIdentities(t, invalidFact,
		builderEvidenceInvalidStageEvidenceIDHex, builderEvidenceInvalidStageFaultIDHex)

	_, err = f.keeper.canonicalBuilderEquivocation(f.ctx, "c", bus.SignedEnvelopeEquivocationV2{
		EnvelopeA: envelopeA, EnvelopeB: envelopeB,
	})
	require.ErrorIs(t, err, errBuilderEvidenceWrongStage)

	// The accepted InferReceipt is the prerequisite the round announced. With it
	// in place both envelopes hold Task authority and tag 2 is reachable.
	require.NoError(t, f.keeper.InferReceipt.Set(f.ctx, taskKey, types.InferReceiptState{
		TaskId: taskID, WinnerWorker: canonicalWorker,
	}))
	equivocationDigest, err := bus.EquivocationDigest(bus.EquivocationContent{
		DigestA: verifiedA.SigningDigest[:], DigestB: verifiedB.SigningDigest[:],
	})
	require.NoError(t, err)
	require.Equal(t, builderEvidenceEquivocationDigestHex, hex.EncodeToString(equivocationDigest[:]))
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
	require.Equal(t, builderEvidenceEquivocationDigestHex, hex.EncodeToString(equivocationFact.CanonicalEvidenceDigest))
	requireBuilderEvidenceIdentities(t, equivocationFact,
		builderEvidenceEquivocationEvidenceIDHex, builderEvidenceEquivocationFaultIDHex)

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

// requireBuilderEvidenceIdentities derives §5.5's published evidence_id and
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
