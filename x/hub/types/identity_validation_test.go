package types_test

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestHubParseSecp256k1PubKeyHexValidatesCurvePoint(t *testing.T) {
	valid := hex.EncodeToString(secp256k1.GenPrivKey().PubKey().Bytes())
	pub, err := types.ParseSecp256k1PubKeyHex("service_pubkey", valid)
	require.NoError(t, err)
	require.Equal(t, valid, hex.EncodeToString(pub.Bytes()))

	invalidCurvePoint := "02" + strings.Repeat("ff", 32)
	_, err = types.ParseSecp256k1PubKeyHex("service_pubkey", invalidCurvePoint)
	require.Error(t, err)
	require.Contains(t, err.Error(), "valid compressed secp256k1 pubkey")
}

func TestServiceKeyBindingUsesEthereumAddress(t *testing.T) {
	privateKey := secp256k1.GenPrivKey()
	publicKey := privateKey.PubKey().(*secp256k1.PubKey)
	ethereumAddress := sdk.AccAddress(types.ServicePubKeyAddress(publicKey)).String()
	require.NoError(t, types.ValidateCurrentServiceKeyBinding("builder", ethereumAddress, ethereumAddress, publicKey.Bytes()))

	cosmosAddress := sdk.AccAddress(publicKey.Address()).String()
	require.NotEqual(t, ethereumAddress, cosmosAddress)
	require.ErrorContains(t,
		types.ValidateCurrentServiceKeyBinding("builder", ethereumAddress, cosmosAddress, publicKey.Bytes()),
		"does not derive",
	)
}

func TestServiceDescriptorAllowsHTTPAndRejectsUnknownSchemes(t *testing.T) {
	descriptor := types.ServiceDescriptorState{
		ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_CORTEX,
		OperatorAddress: "operator-1", DescriptorVersion: 1, EndpointCount: 1,
		Endpoints: []types.ServiceEndpointV1{{
			EndpointKind: types.ServiceEndpointKind_SERVICE_ENDPOINT_KIND_OBJECT_GATEWAY_HTTPS,
			Uri:          "http://127.0.0.1:8080/.well-known/trueopen-cortex.json", ProtocolVersion: "v1",
		}},
		DescriptorHash: bytes.Repeat([]byte{1}, 32), UpdatedHeight: 1,
	}
	require.NoError(t, descriptor.Validate())

	descriptor.Endpoints[0].Uri = "ftp://127.0.0.1/descriptor.json"
	require.ErrorContains(t, descriptor.Validate(), "unsupported uri scheme")
}

func TestHubValidateSHA256HexRequiresCanonicalLowercase(t *testing.T) {
	valid := strings.Repeat("ab", 32)
	canonical, err := types.ValidateSHA256Hex("metadata_hash", valid)
	require.NoError(t, err)
	require.Equal(t, valid, canonical)

	_, err = types.ValidateSHA256Hex("metadata_hash", strings.ToUpper(valid))
	require.Error(t, err)
	require.Contains(t, err.Error(), "lowercase")

	_, err = types.ValidateSHA256Hex("metadata_hash", "not-a-sha256")
	require.Error(t, err)
	require.Contains(t, err.Error(), "64")
}

func TestServiceBondRejectsUnspecifiedStatus(t *testing.T) {
	bond := types.ServiceBondState{
		OperatorAddress:     "operator",
		ActiveBond:          1,
		EffectiveActiveBond: 1,
		BondVersion:         1,
		Status:              types.ServiceBondStatus_SERVICE_BOND_STATUS_UNSPECIFIED,
	}
	require.ErrorContains(t, bond.Validate(), "invalid service bond status")
}

func TestHubStrictSignatureVerifiesRegisteredPubKey(t *testing.T) {
	priv := secp256k1.GenPrivKey()
	msg := types.CanonicalDailySupportConfirmationSigningBytes(
		"chain-test", bytes.Repeat([]byte{0xab}, 20), 1, 1, 100,
		[]types.ProfileKeyV1{{ModelId: "model", ProfileVersion: 1}},
	)
	sig, err := types.SignSecp256k1DigestForTest(priv, msg)
	require.NoError(t, err)
	require.NoError(t, types.VerifyStrictSecp256k1Digest(priv.PubKey(), msg, sig))
	_, err = shared.DecodeCanonicalSecp256k1SignatureHex(" " + hex.EncodeToString(sig))
	require.Error(t, err)
	require.ErrorContains(t, types.VerifyStrictSecp256k1Digest(priv.PubKey(), msg, append(sig, 0)), "64 bytes")

	curveOrder, ok := new(big.Int).SetString("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141", 16)
	require.True(t, ok)
	highS := new(big.Int).Sub(curveOrder, new(big.Int).SetBytes(sig[32:]))
	highSignature := append([]byte(nil), sig...)
	highSBytes := highS.FillBytes(make([]byte, 32))
	copy(highSignature[32:], highSBytes)
	require.ErrorContains(t, types.VerifyStrictSecp256k1Digest(priv.PubKey(), msg, highSignature), "low-S")

	other := secp256k1.GenPrivKey()
	require.Error(t, types.VerifyStrictSecp256k1Digest(other.PubKey(), msg, sig))
}

// The three identity digests each have their own registered domain
// (the API contract) and are built with H_FIELDS_V1, so moving a
// separator across a field boundary can never produce the same digest and no
// two actions can share one preimage.
func TestHubServiceAuthorizationBytesAreDomainSeparatedAndLengthFramed(t *testing.T) {
	cortex := shared.EnumBE(uint32(shared.ParticipantType_PARTICIPANT_TYPE_CORTEX))
	pubkey := bytes.Repeat([]byte{0xab}, 33)

	// TRUEOPEN_SERVICE_KEY_ROTATION_V1, §10.0c1.
	rotation := func(chainID, operator string) []byte {
		return shared.CanonicalHashBytes(
			shared.MustDomain(shared.DomainServiceKeyRotationV1),
			[]byte(chainID), cortex, []byte(operator), pubkey,
			shared.Uint64BE(1), shared.Uint64BE(2),
		)
	}
	rotationA := rotation("chain|segment", "operator")
	rotationB := rotation("chain", "segment|operator")

	// TRUEOPEN_SERVICE_REGISTRATION_V1, §10.0c step 1. Node binds participant_type
	// after chain_id so Cortex and Builder registration stop sharing one preimage.
	registration := shared.CanonicalHashBytes(
		shared.MustDomain(shared.DomainServiceRegistrationV1),
		[]byte("chain|segment"), cortex, []byte("operator"), pubkey, shared.Uint64BE(1),
	)

	// TRUEOPEN_SERVICE_DESCRIPTOR_V1, §9.6b. This preimage carries no chain_id and
	// frames an absent tls_pubkey_hash as an empty field.
	descriptor := shared.CanonicalHashBytes(
		shared.MustDomain(shared.DomainServiceDescriptorV1),
		cortex, []byte("operator"), shared.Uint64BE(1), shared.Uint32BE(1),
		shared.EnumBE(1), []byte("https://node.invalid"), []byte("v1.0"), nil,
	)

	for _, digest := range [][]byte{rotationA, rotationB, registration, descriptor} {
		require.Len(t, digest, 32)
	}
	require.NotEqual(t, rotationA, rotationB, "length framing must keep field boundaries")
	require.NotEqual(t, rotationA, registration, "register and rotate use different domains")
	require.NotEqual(t, rotationA, descriptor)
	require.NotEqual(t, registration, descriptor)

	require.Equal(t, rotationA, rotation("chain|segment", "operator"), "the digest must be deterministic")
}
