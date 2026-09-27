package contracttest

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/cosmos/cosmos-sdk/types/bech32"
	"github.com/stretchr/testify/require"

	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestResultReceiptSigningDigestMatchesWireRC2(t *testing.T) {
	decode := func(value string) []byte {
		decoded, err := hex.DecodeString(value)
		require.NoError(t, err)
		return decoded
	}
	verifierBytes := decode("2b753f1e4d4bf656f8bdce49c18362c4aa1a25f2")
	verifier, err := bech32.ConvertAndEncode("trueopen", verifierBytes)
	require.NoError(t, err)
	receipt := tasktypes.ResultReceiptV3{
		SchemaVersion: 3, ChainId: "trueopen-golden-1",
		TaskId: bytes.Repeat([]byte{0x11}, 32), VerifyRound: 1,
		VerifierOperatorAddress: verifier, ServiceAuthorizationNonce: 7,
		GenerationParamsDigest: decode("b9cc1d8c612df0db96bd365b24e2aa9db3f418775452b2106156922f8816a314"),
		MetricRoot:             decode("c261e32222bab88b3ea110c865bb97a483d15968cbfe42317fc7c118e1568a93"),
		MetricSummary: tasktypes.MetricSummaryV1{
			FiniteCount: 2, MissingComparedCount: 1,
			MeanAbsLogprobDiffFp_1E6: 50_000,
			AbsLogprobDiffP95Fp_1E6:  50_000,
			AbsLogprobDiffP99Fp_1E6:  50_000,
			ComparedRankCount:        2,
		},
		AggregateProofHash:                decode("6a54c75efb90d4fb60f16fa685633e634e3ce8a3c1d3ab68f5ff600eb1952db2"),
		VerifierEvidenceBundleHash:        decode("5b56779af1df4a720e944c89571cda6613e6749e4b4919eaa5fe6d526f3fe866"),
		VerifierEvidenceManifestSizeBytes: 603,
		Salt:                              bytes.Repeat([]byte{0xaa}, 32), ExpiryHeight: 2000,
		VerifierValueRoot: decode("58e2382fc950514f3b698f7be548880f9a40f2095848d4ed5884cbd13eb8b3ea"),
		MetricLeafCount:   3, VerifierEvidenceKeyCommitment: make([]byte, 32),
	}
	digest, err := tasktypes.ResultReceiptSigningDigest(receipt)
	require.NoError(t, err)
	require.Equal(t, "abaa595867935f9d8e3caf9da320eec93aa2a373947196a8d72c523d84497aff", hex.EncodeToString(digest[:]))
}
