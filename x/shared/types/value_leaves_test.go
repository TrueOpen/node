package types

import (
	"bytes"
	"testing"

	"github.com/TrueOpen/wire/dispatch"
	"github.com/stretchr/testify/require"
)

func TestValuePositionCommitmentsAndAttribution(t *testing.T) {
	signed := func(v int64) []byte { return Uint64BE(uint64(v)) }
	hash := func(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }
	scope := ValueLeafScope{ChainID: "value-evidence-test", TaskID: hash(1), TaskHash: hash(2), Worker: bytes.Repeat([]byte{3}, 20), Verifier: bytes.Repeat([]byte{4}, 20), Round: 1, RequiredTopK: 2, ModelID: hash(5), ProfileVersion: 1, GenerationParamsDigest: hash(6), Profile: ProfileExecutionSnapshot{RequiredTopK: 2, TokenizerHash: hash(7), VerificationProfile: VerificationProfile{JudgmentFunctionVersion: "judgment-v1", CanonicalEncodingVersion: "canonical-v1", EvidenceSchemaHash: hash(8), MetricAggregateProofVersion: "aggregate-v1", Metrics: MetricSpec{CompareLogprobDiff: true, CompareRankDelta: true}}}}
	for _, test := range []struct {
		name                                                               string
		duplicate, badWorkerFlags, badMetric, badPath, badScope            bool
		wrongWorkerToken, wrongVerifierToken, unprovenTopK, mixedNonfinite bool
		worker, verifier                                                   bool
		wantError                                                          bool
	}{
		{name: "honest commitment"},
		{name: "committed duplicate token", duplicate: true, worker: true},
		{name: "committed invalid worker flags", badWorkerFlags: true, worker: true},
		{name: "committed inconsistent local difference", badMetric: true, verifier: true},
		{name: "worker fault precedes verifier fault", duplicate: true, badMetric: true, worker: true},
		{name: "uncommitted accusation", badPath: true, wantError: true},
		{name: "foreign task", badScope: true, wantError: true},
		{name: "Worker and metric token context differs", wrongWorkerToken: true, wantError: true},
		{name: "Verifier emitted token differs", wrongVerifierToken: true, verifier: true},
		{name: "top-k derived metrics are outside local attribution", unprovenTopK: true},
		{name: "individually valid nonfinite and missing leaves", mixedNonfinite: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			token2 := uint32(7)
			if test.duplicate {
				token2 = 2
			}
			topk, err := CanonicalRepeatedFramesV1([]CanonicalFrameV1{FlatCanonicalFrameV1(Uint32BE(2), signed(-1_250_000)), FlatCanonicalFrameV1(Uint32BE(token2), signed(-2_000_000))}).Bytes()
			require.NoError(t, err)
			missing := byte(0)
			if test.badWorkerFlags {
				missing = 1
			}
			workerLogprob, verifierLogprob := int64(-1_250_000), int64(-1_200_000)
			workerRank, verifierRank := uint32(1), uint32(1)
			workerFinite, verifierFinite, verifierMissing := byte(1), byte(1), byte(0)
			verifierToken, metricToken := uint32(2), uint32(2)
			if test.wrongWorkerToken {
				metricToken = 3
			}
			if test.wrongVerifierToken {
				verifierToken = 3
			}
			if test.mixedNonfinite {
				workerLogprob, verifierLogprob, workerRank, verifierRank = 0, 0, 0, 0
				workerFinite, verifierFinite, verifierMissing = 0, 0, 1
				topk, err = CanonicalRepeatedFramesV1(nil).Bytes()
				require.NoError(t, err)
			}
			worker := CanonicalFrameBytes([]byte(scope.ChainID), scope.TaskID, scope.TaskHash, scope.Worker, Uint32BE(0), Uint32BE(2), signed(workerLogprob), Uint32BE(workerRank), topk, []byte{missing}, []byte{workerFinite})
			verifier := CanonicalFrameBytes([]byte(scope.ChainID), scope.TaskID, scope.TaskHash, Uint32BE(1), scope.Verifier, Uint32BE(0), Uint32BE(verifierToken), signed(verifierLogprob), Uint32BE(verifierRank), hash(9), []byte{verifierMissing}, []byte{verifierFinite})
			root := func(domain, tree string, leaf []byte) [32]byte {
				digest := dispatch.HashFields(domain, Uint32BE(1), leaf)
				return [32]byte(dispatch.MerkleRootV1(tree, [][]byte{digest[:]}))
			}
			wr := root("TRUEOPEN_PREFILL_WORKER_VALUE_LEAF_V1", "TRUEOPEN_PREFILL_WORKER_VALUE_ROOT_V1", worker)
			vr := root("TRUEOPEN_PREFILL_VERIFIER_VALUE_LEAF_V1", "TRUEOPEN_PREFILL_VERIFIER_VALUE_ROOT_V1", verifier)
			diff := uint64(50_000)
			if test.mixedNonfinite {
				diff = 0
			}
			if test.badMetric {
				diff++
			}
			vp := scope.Profile.VerificationProfile
			metric := CanonicalFrameBytes([]byte(scope.ChainID), scope.TaskID, scope.TaskHash, Uint32BE(1), scope.ModelID, Uint32BE(1), []byte(vp.JudgmentFunctionVersion), []byte(vp.CanonicalEncodingVersion), vp.EvidenceSchemaHash, []byte(vp.MetricAggregateProofVersion), scope.Profile.TokenizerHash, scope.GenerationParamsDigest, Uint32BE(0), Uint32BE(metricToken), Uint32BE(2), signed(workerLogprob), signed(verifierLogprob), Uint64BE(diff), Uint32BE(workerRank), Uint32BE(verifierRank), signed(0), []byte{0}, []byte{0}, []byte{0}, []byte{workerFinite}, vr[:])
			mr := root("TRUEOPEN_PREFILL_TOKEN_METRIC_LEAF_V3", "TRUEOPEN_PREFILL_TOKEN_METRIC_ROOT_V3", metric)
			opening := dispatch.ValuePositionOpening{WorkerLeaf: worker, VerifierLeaf: verifier, MetricLeaf: metric}
			if test.badPath {
				opening.WorkerPath = [][32]byte{{1}}
			}
			localScope := scope
			if test.unprovenTopK {
				localScope.Profile.VerificationProfile.Metrics.CompareTopkJaccard = true
				localScope.Profile.VerificationProfile.Metrics.CompareUnionJs = true
			}
			if test.badScope {
				localScope.TaskID = hash(99)
			}
			wi, vm, err := VerifyValuePosition(localScope, opening, 1, wr, vr, mr, false)
			if test.wantError {
				require.Error(t, err)
				require.False(t, wi)
				require.False(t, vm)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.worker, wi)
			require.Equal(t, test.verifier, vm)
		})
	}
}
