package contracttest

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/TrueOpen/node/internal/testutil/domainfixture"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestInferReceiptSigningDigestMatchesWireRC2(t *testing.T) {
	decode := func(value string) []byte {
		decoded, err := hex.DecodeString(value)
		require.NoError(t, err)
		return decoded
	}
	receipt := tasktypes.InferReceiptV3{
		SchemaVersion: 3, ChainId: "trueopen-golden-1",
		TaskId:                    bytes.Repeat([]byte{0x11}, 32),
		TaskHash:                  bytes.Repeat([]byte{0x22}, 32),
		WorkerOperatorAddress:     "trueopen1rfjz7r3u8t65teavh5utquj3kwvsj983p3jclz",
		ServiceAuthorizationNonce: 7,
		GenerationParamsDigest:    decode("b9cc1d8c612df0db96bd365b24e2aa9db3f418775452b2106156922f8816a314"),
		OutputHash:                decode("1d07690eb524833c073fe74787e52a25e426ed09013500d3dd43f7f363f65ec0"),
		OutputSizeBytes:           6,
		RequiredEvidenceCommitments: []tasktypes.EvidenceCommitmentV1{
			{
				EvidenceKind:       shared.EvidenceKind_EVIDENCE_KIND_WORKER_VALUE_OPENING,
				EvidenceHashOrRoot: decode("17b4b66711beba88ab8ea07f28d21576daa206c78583ec4f3023aaaa1fd7f44d"),
				EncodedSizeBytes:   817,
			},
			{
				EvidenceKind:       shared.EvidenceKind_EVIDENCE_KIND_WORKER_TOKEN_OPENING,
				EvidenceHashOrRoot: decode("f9470df7daf275da0f23215b67fd2d6e6cadfb139e9da89fc70bed77cc7b9571"),
				EncodedSizeBytes:   28,
			},
		},
		ExpiryHeight: 2000, GeneratedTokenCount: 3, OutputLeafCount: 3,
		OutputKeyCommitment:      make([]byte, 32),
		WorkerTokenKeyCommitment: make([]byte, 32),
		WorkerValueKeyCommitment: make([]byte, 32),
		CiphertextOutputRoot:     make([]byte, 32),
	}
	for _, vector := range domainfixture.Load(t, "testdata/infer_receipt_v3.json").Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			receipt.GeneratedTokenCount = vector.Uint(t, 11, "generated_token_count")
			receipt.OutputLeafCount = vector.Uint(t, 12, "output_leaf_count")
			receipt.DispatchTicketHash = vector.Bytes(t, 17, "dispatch_ticket_hash")
			digest, err := tasktypes.InferReceiptSigningDigest(receipt)
			require.NoError(t, err)
			domainfixture.RequireDigest(t, vector, digest[:])
			receipt.DispatchTicketHash = bytes.Repeat([]byte{0x99}, 32)
			changed, err := tasktypes.InferReceiptSigningDigest(receipt)
			require.NoError(t, err)
			require.NotEqual(t, digest, changed)
		})
	}
}
