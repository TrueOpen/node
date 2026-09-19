package types

import (
	"bytes"
	"encoding/hex"
	"testing"

	dcrsecp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	dcrecdsa "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

const (
	accountSigningVectorUser      = "trueopen1rfjz7r3u8t65teavh5utquj3kwvsj983p3jclz"
	accountSigningVectorTaskHash  = "58c2b8fdfa6e5dac474ff4d4fd2089bac2cea26756daf0b40973bff15bb221b7"
	accountSigningVectorSignature = "a992fe7b0fd6447bde4f21467064cfbd11f9012bd694d6106ac20e10c0f272d4216dceaabcd04f3c6da1fe3fc740c77dcf8ec9c7baf540926ae5f848cd04a85f1b"
)

type testEIP712AccountPublicKey struct {
	typeName string
	key      []byte
}

func (k testEIP712AccountPublicKey) Type() string  { return k.typeName }
func (k testEIP712AccountPublicKey) Bytes() []byte { return append([]byte(nil), k.key...) }

func TestTaskOrderEIP712PublicVector(t *testing.T) {
	order := accountSigningVectorOrder(t)
	taskHash := mustDecodeEIP712Hex(t, accountSigningVectorTaskHash)
	digest, err := BuildTaskOrderEIP712Digest(424242, "uusdc", order, taskHash)
	require.NoError(t, err)
	require.Equal(t, "54e6898d361f89f6e2a5a865d7d329459be265231a65729798452d2dcab0bb6d", hex.EncodeToString(digest.DomainSeparator[:]))
	require.Equal(t, "fb12173fe8ccf6d610ae2164bb83470beaec83922d51105a60fadfed935b35f3", hex.EncodeToString(digest.HashStruct[:]))
	require.Equal(t, "00ea0894077c1a005422d5f00332fe520aa1714443aa6a46249fec5a7d05f09d", hex.EncodeToString(digest.SigningDigest[:]))

	recovered, err := RecoverEIP712Signer(digest.SigningDigest[:], mustDecodeEIP712Hex(t, accountSigningVectorSignature))
	require.NoError(t, err)
	require.Equal(t, "031b84c5567b126440995d3ed5aaba0565d71e1834604819ff9c17f5e9d5dd078f", hex.EncodeToString(recovered.PublicKey))
	require.Equal(t, "1a642f0e3c3af545e7acbd38b07251b3990914f1", hex.EncodeToString(recovered.Address))
}

func TestVerifySignedOrderV2EIP712WithAccountOrAddress(t *testing.T) {
	privateKey := dcrsecp256k1.PrivKeyFromBytes(bytes.Repeat([]byte{0x01}, 32))
	order := accountSigningVectorOrder(t)
	taskHash, err := TaskOrderHash(order)
	require.NoError(t, err)
	digest, err := BuildTaskOrderEIP712Digest(424242, "uusdc", order, taskHash[:])
	require.NoError(t, err)
	compact := dcrecdsa.SignCompact(privateKey, digest.SigningDigest[:], false)
	require.Contains(t, []byte{27, 28}, compact[0])
	signature := append(append([]byte(nil), compact[1:]...), compact[0])
	signedOrder := SignedOrderV2{
		Order: order, SignatureScheme: SignatureSchemeEIP712, UserSignature: signature,
	}
	accountKey := testEIP712AccountPublicKey{
		typeName: EthSecp256k1PublicKeyType,
		key:      privateKey.PubKey().SerializeCompressed(),
	}
	expectedAddress, err := canonicalTaskOrderEIP712User(order.UserAddress)
	require.NoError(t, err)
	require.NoError(t, VerifySignedOrderV2EIP712WithAccountPublicKey(424242, "uusdc", signedOrder, taskHash[:], accountKey))
	require.NoError(t, VerifySignedOrderV2EIP712WithAddress(424242, "uusdc", signedOrder, taskHash[:], expectedAddress))

	t.Run("wrong task hash", func(t *testing.T) {
		wrong := append([]byte(nil), taskHash[:]...)
		wrong[0] ^= 1
		require.ErrorContains(t, VerifySignedOrderV2EIP712WithAccountPublicKey(424242, "uusdc", signedOrder, wrong, accountKey), "task_hash")
	})
	t.Run("wrong domain chain", func(t *testing.T) {
		require.ErrorContains(t, VerifySignedOrderV2EIP712WithAccountPublicKey(424243, "uusdc", signedOrder, taskHash[:], accountKey), "recovered signer")
	})
	t.Run("wrong business denom", func(t *testing.T) {
		require.ErrorContains(t, VerifySignedOrderV2EIP712WithAccountPublicKey(424242, "utrueopen", signedOrder, taskHash[:], accountKey), "recovered signer")
	})
	t.Run("wrong scheme", func(t *testing.T) {
		changed := signedOrder
		changed.SignatureScheme = "secp256k1"
		require.ErrorContains(t, VerifySignedOrderV2EIP712WithAccountPublicKey(424242, "uusdc", changed, taskHash[:], accountKey), "signature_scheme")
	})
	t.Run("wrong public key type", func(t *testing.T) {
		changed := accountKey
		changed.typeName = "secp256k1"
		require.ErrorContains(t, VerifySignedOrderV2EIP712WithAccountPublicKey(424242, "uusdc", signedOrder, taskHash[:], changed), "public key type")
	})
	t.Run("wrong expected address", func(t *testing.T) {
		wrong := append([]byte(nil), expectedAddress...)
		wrong[0] ^= 1
		require.ErrorContains(t, VerifySignedOrderV2EIP712WithAddress(424242, "uusdc", signedOrder, taskHash[:], wrong), "expected user address")
	})
}

func TestCanonicalEIP712SignatureRejectsAlternateForms(t *testing.T) {
	valid := mustDecodeEIP712Hex(t, accountSigningVectorSignature)
	tests := map[string][]byte{
		"raw64":         append([]byte(nil), valid[:64]...),
		"recovery zero": append(append([]byte(nil), valid[:64]...), 0),
		"recovery one":  append(append([]byte(nil), valid[:64]...), 1),
		"zero R":        append(make([]byte, 32), valid[32:]...),
	}

	// n-S is the malleable high-S counterpart of the public vector.
	highS := append([]byte(nil), valid...)
	orderN := mustDecodeEIP712Hex(t, "fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141")
	subtractBigEndian(highS[32:64], orderN, valid[32:64])
	tests["high S"] = highS

	for name, signature := range tests {
		t.Run(name, func(t *testing.T) {
			require.Error(t, RequireCanonicalEIP712Signature(signature))
		})
	}
}

func accountSigningVectorOrder(t *testing.T) TaskOrderV2 {
	t.Helper()
	return TaskOrderV2{
		SchemaVersion: 2, ChainId: "trueopen-golden-1", UserAddress: accountSigningVectorUser,
		SessionId:     mustDecodeEIP712Hex(t, "77625100ba4faa1306ae6eaf5a872a661443aa94f87c5530c4b178614e3d62f7"),
		OrderSequence: 7, ModelId: "trueopen/golden-model", ProfileVersion: 1,
		TaskType:  shared.TaskType_TASK_TYPE_TEXT_GENERATION,
		InputHash: bytes.Repeat([]byte{0x42}, Hash32Len), InputSizeBytes: 1, InputBucket: 1, OutputBudgetBucket: 1,
		GenerationParams: GenerationParamsV1{
			GenerationParamsSchemaVersion: GenerationParamsSchemaVersionV1,
			MaxOutputTokens:               1, MaxOutputDuration: 1,
			DecodingParams: DecodingParamsV1{TopPPpm: TopPPMMaxV1, RepetitionPenaltyPpm: 1_000_000},
		},
		PriceBid: shared.NewAmount(1), MaxFee: shared.NewAmount(1_000_000),
		AssignmentPriorityFee: shared.NewAmount(0), TxFeeReserve: shared.NewAmount(0),
		EarliestSubmitHeight: 1000, OrderExpireHeight: 2000,
		DeadlinePolicy:       DeadlinePolicyV1{LatencyClass: DeadlineLatencyClass_DEADLINE_LATENCY_CLASS_STANDARD},
		TimeoutBucketVersion: 1, SessionAnchorHeight: 1,
		SessionAnchorBlockHash: bytes.Repeat([]byte{0x43}, Hash32Len),
		BuilderSetId:           "builder-set-golden", BuilderSetHash: bytes.Repeat([]byte{0x44}, Hash32Len),
	}
}

func mustDecodeEIP712Hex(t *testing.T, value string) []byte {
	t.Helper()
	raw, err := hex.DecodeString(value)
	require.NoError(t, err)
	return raw
}

func subtractBigEndian(output, left, right []byte) {
	borrow := 0
	for index := len(left) - 1; index >= 0; index-- {
		value := int(left[index]) - int(right[index]) - borrow
		if value < 0 {
			value += 256
			borrow = 1
		} else {
			borrow = 0
		}
		output[index] = byte(value)
	}
}
