package types_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cosmos/cosmos-sdk/types/bech32"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

// wireVectorFile is the Wire v0.3 `trueopen.task.result_receipt.v2` fixture vendored
// under testdata. It is the single authority for the V2 result-chain preimages;
// Node reproduces it rather than pinning locally computed digests.
const wireVectorFile = "testdata/result_receipt_v2.json"

type wireVectorSet struct {
	Vectors []struct {
		Name        string `json:"name"`
		Domain      string `json:"domain"`
		PreimageHex string `json:"preimage_hex"`
		DigestHex   string `json:"digest_hex"`
	} `json:"vectors"`
}

func loadWireVector(t *testing.T, name string) (preimage []byte, digest []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(wireVectorFile))
	require.NoError(t, err)
	var set wireVectorSet
	require.NoError(t, json.Unmarshal(raw, &set))
	for _, vector := range set.Vectors {
		if vector.Name != name {
			continue
		}
		preimage, err = hex.DecodeString(vector.PreimageHex)
		require.NoError(t, err)
		digest, err = hex.DecodeString(vector.DigestHex)
		require.NoError(t, err)
		return preimage, digest
	}
	t.Fatalf("wire vector %q is missing from %s", name, wireVectorFile)
	return nil, nil
}

// The MetricSummaryV1 producer is bound to the published Wire preimage *and*
// digest, so a framing change fails on the preimage instead of being masked by a
// recomputed hash.
func TestMetricSummaryHashMatchesWireVector(t *testing.T) {
	preimage, digest := loadWireVector(t, "metric_summary_v1")

	summary := tasktypes.MetricSummaryV1{
		FiniteCount:                3,
		MissingComparedCount:       0,
		MeanAbsLogprobDiffFp_1E6:   10_000,
		AbsLogprobDiffP95Fp_1E6:    20_000,
		AbsLogprobDiffP99Fp_1E6:    30_000,
		RankDeltaNonzeroRateFp_1E6: 40_000,
		XTopkJaccardMeanFp_1E6: &tasktypes.MetricSummaryV1_TopkJaccardMeanFp_1E6{
			TopkJaccardMeanFp_1E6: 900_000,
		},
		XUnionJsP99Fp_1E6: &tasktypes.MetricSummaryV1_UnionJsP99Fp_1E6{
			UnionJsP99Fp_1E6: 50_000,
		},
		ComparedTopkCount: 3,
		ComparedRankCount: 3,
	}

	// The published preimage is the whole H_FIELDS_V1 root — the domain frame
	// followed by the nested MetricSummaryV1 body — not the body on its own.
	frame, err := tasktypes.CanonicalMetricSummaryFrameV1(summary)
	require.NoError(t, err)
	rooted := shared.CanonicalFrameBytes([]byte(shared.MustDomain(shared.DomainMetricSummaryV1)), frame)
	require.Equal(t, hex.EncodeToString(preimage), hex.EncodeToString(rooted))

	got, err := tasktypes.MetricSummaryHash(summary)
	require.NoError(t, err)
	require.Equal(t, hex.EncodeToString(digest), hex.EncodeToString(got[:]))
}

func TestResultReceiptSigningDigestReadsEveryField(t *testing.T) {
	base := verificationReceiptFixture(t)
	baseDigest, err := tasktypes.ResultReceiptSigningDigest(base)
	require.NoError(t, err)

	mutations := map[string]func(*tasktypes.ResultReceiptV2){
		"schema_version":              func(v *tasktypes.ResultReceiptV2) { v.SchemaVersion++ },
		"chain_id":                    func(v *tasktypes.ResultReceiptV2) { v.ChainId += "-other" },
		"task_id":                     func(v *tasktypes.ResultReceiptV2) { v.TaskId[0] ^= 0xff },
		"verify_round":                func(v *tasktypes.ResultReceiptV2) { v.VerifyRound++ },
		"verifier_operator_address":   func(v *tasktypes.ResultReceiptV2) { v.VerifierOperatorAddress = verificationAddress(t, 0x41) },
		"service_authorization_nonce": func(v *tasktypes.ResultReceiptV2) { v.ServiceAuthorizationNonce++ },
		"generation_params_digest":    func(v *tasktypes.ResultReceiptV2) { v.GenerationParamsDigest[0] ^= 0xff },
		"metric_root":                 func(v *tasktypes.ResultReceiptV2) { v.MetricRoot[0] ^= 0xff },
		"metric_summary":              func(v *tasktypes.ResultReceiptV2) { v.MetricSummary.FiniteCount++ },
		"aggregate_proof_hash":        func(v *tasktypes.ResultReceiptV2) { v.AggregateProofHash[0] ^= 0xff },
		"verifier_evidence_bundle_hash": func(v *tasktypes.ResultReceiptV2) {
			v.VerifierEvidenceBundleHash[0] ^= 0xff
		},
		"verifier_evidence_manifest_size_bytes": func(v *tasktypes.ResultReceiptV2) {
			v.VerifierEvidenceManifestSizeBytes++
		},
		"salt":          func(v *tasktypes.ResultReceiptV2) { v.Salt[0] ^= 0xff },
		"expiry_height": func(v *tasktypes.ResultReceiptV2) { v.ExpiryHeight++ },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			candidate := verificationReceiptFixture(t)
			mutate(&candidate)
			digest, err := tasktypes.ResultReceiptSigningDigest(candidate)
			require.NoError(t, err)
			require.NotEqual(t, baseDigest, digest)
		})
	}

	base.ServiceSignature[0] ^= 0xff
	digest, err := tasktypes.ResultReceiptSigningDigest(base)
	require.NoError(t, err)
	require.Equal(t, baseDigest, digest, "service_signature is outside its own signing digest")
}

func TestMetricSummaryOptionalPresenceIsCanonical(t *testing.T) {
	base := verificationReceiptFixture(t).MetricSummary
	withoutTopK := base
	withoutTopK.XTopkJaccardMeanFp_1E6 = nil
	withZeroTopK := base
	withZeroTopK.XTopkJaccardMeanFp_1E6 = &tasktypes.MetricSummaryV1_TopkJaccardMeanFp_1E6{}

	absent, err := tasktypes.MetricSummaryHash(withoutTopK)
	require.NoError(t, err)
	presentZero, err := tasktypes.MetricSummaryHash(withZeroTopK)
	require.NoError(t, err)
	require.NotEqual(t, absent, presentZero)

	withNilPayload := base
	var nilTopK *tasktypes.MetricSummaryV1_TopkJaccardMeanFp_1E6
	withNilPayload.XTopkJaccardMeanFp_1E6 = nilTopK
	_, err = tasktypes.MetricSummaryHash(withNilPayload)
	require.Error(t, err)
}

func TestVerificationTypedFramesMatchLegacyBytes(t *testing.T) {
	receipt := verificationReceiptFixture(t)
	metricFrame, err := tasktypes.CanonicalMetricSummaryTypedFrameV1(receipt.MetricSummary)
	require.NoError(t, err)
	require.Equal(t, uint8(2), metricFrame.Depth())
	typedMetric, err := metricFrame.Bytes()
	require.NoError(t, err)
	legacyMetric, err := tasktypes.CanonicalMetricSummaryFrameV1(receipt.MetricSummary)
	require.NoError(t, err)
	require.Equal(t, legacyMetric, typedMetric)
}

func TestCommitmentHelpersRejectNonHashPlaceholders(t *testing.T) {
	receipt := verificationReceiptFixture(t)

	_, err := tasktypes.DeriveCommitKey(receipt.ChainId, receipt.TaskId[:31], receipt.VerifyRound, receipt.VerifierOperatorAddress)
	require.Error(t, err)

	_, err = tasktypes.ResultCommitmentHash(
		receipt.ChainId,
		receipt.TaskId,
		verificationBytes(0x77),
		receipt.VerifyRound,
		receipt.VerifierOperatorAddress,
		verificationBytes(0x88)[:31],
		receipt.Salt,
	)
	require.Error(t, err)
}

func verificationReceiptFixture(t *testing.T) tasktypes.ResultReceiptV2 {
	t.Helper()
	return tasktypes.ResultReceiptV2{
		SchemaVersion:             tasktypes.ResultReceiptSchemaVersionV2,
		ChainId:                   "trueopen-verification-1",
		TaskId:                    verificationBytes(0x11),
		VerifyRound:               7,
		VerifierOperatorAddress:   verificationAddress(t, 0x31),
		ServiceAuthorizationNonce: 19,
		GenerationParamsDigest:    verificationBytes(0x22),
		MetricRoot:                verificationBytes(0x33),
		MetricSummary: tasktypes.MetricSummaryV1{
			FiniteCount:                23,
			MissingComparedCount:       1,
			MeanAbsLogprobDiffFp_1E6:   1001,
			AbsLogprobDiffP95Fp_1E6:    2002,
			AbsLogprobDiffP99Fp_1E6:    3003,
			RankDeltaNonzeroRateFp_1E6: 4004,
			XTopkJaccardMeanFp_1E6: &tasktypes.MetricSummaryV1_TopkJaccardMeanFp_1E6{
				TopkJaccardMeanFp_1E6: 900_000,
			},
			XUnionJsP99Fp_1E6: &tasktypes.MetricSummaryV1_UnionJsP99Fp_1E6{
				UnionJsP99Fp_1E6: 50_000,
			},
			ComparedTopkCount: 23,
			ComparedRankCount: 22,
		},
		AggregateProofHash:                verificationBytes(0x44),
		VerifierEvidenceBundleHash:        verificationBytes(0x55),
		VerifierEvidenceManifestSizeBytes: 4_096,
		Salt:                              verificationBytes(0x66),
		ExpiryHeight:                      987_654,
		ServiceSignature:                  make([]byte, 64),
	}
}

func verificationAddress(t *testing.T, value byte) string {
	t.Helper()
	raw := make([]byte, 20)
	for i := range raw {
		raw[i] = value
	}
	encoded, err := bech32.ConvertAndEncode("trueopen", raw)
	require.NoError(t, err)
	return encoded
}

func verificationBytes(value byte) []byte {
	out := make([]byte, tasktypes.Hash32Len)
	for i := range out {
		out[i] = value
	}
	return out
}
