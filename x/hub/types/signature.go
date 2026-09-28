package types

import (
	"bytes"
	"fmt"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// Every consensus domain used below resolves through the shared registry so an
// unregistered domain cannot reach a digest.
// Ordered field encodings stay in the helper that owns each digest; the registry
// records framing, field order, producer, consumers, Store, Event and Query.
var (
	DomainSupportModelsV1 = shared.MustDomain(shared.DomainSupportModelsV1)

	DomainDailySupportConfirmationV1 = shared.MustDomain(shared.DomainDailySupportConfirmationV1)
)

// CanonicalSupportedModelsHash commits to strictly ascending, unique raw model IDs.
func CanonicalSupportedModelsHash(models [][]byte) []byte {
	digest, err := CanonicalSupportedModelsHashV1(models)
	if err != nil {
		return nil
	}
	return digest
}

func CanonicalSupportedModelsHashV1(models [][]byte) ([]byte, error) {
	repeatedModels, err := canonicalSupportedModelsFrame(models)
	if err != nil {
		return nil, err
	}
	return shared.NewCanonicalHashBuilderV1(DomainSupportModelsV1).Raw(
		shared.Uint32BE(uint32(len(models))),
	).Nested(repeatedModels).Sum()
}

// CanonicalDailySupportConfirmationSigningBytes is the legacy no-error wrapper.
// Deprecated: node production uses CanonicalDailySupportConfirmationSigningBytesV1.
func CanonicalDailySupportConfirmationSigningBytes(
	chainID string,
	operatorAddress []byte,
	epochIndex, serviceAuthorizationNonce, expiryHeight uint64,
	models [][]byte,
) []byte {
	digest, err := CanonicalDailySupportConfirmationSigningBytesV1(
		chainID, operatorAddress, epochIndex, serviceAuthorizationNonce, expiryHeight, models,
	)
	if err != nil {
		return nil
	}
	return digest
}

func CanonicalDailySupportConfirmationSigningBytesV1(
	chainID string,
	operatorAddress []byte,
	epochIndex, serviceAuthorizationNonce, expiryHeight uint64,
	models [][]byte,
) ([]byte, error) {
	repeatedModels, err := canonicalSupportedModelsFrame(models)
	if err != nil {
		return nil, err
	}
	return shared.NewCanonicalHashBuilderV1(DomainDailySupportConfirmationV1).Raw(
		[]byte(chainID), operatorAddress, shared.Uint64BE(epochIndex),
		shared.Uint64BE(serviceAuthorizationNonce), shared.Uint64BE(expiryHeight),
		shared.Uint32BE(uint32(len(models))),
	).Nested(repeatedModels).Sum()
}

func canonicalSupportedModelsFrame(models [][]byte) (shared.CanonicalFrameV1, error) {
	fields := make([]shared.CanonicalFieldV1, len(models))
	for index, modelID := range models {
		if len(modelID) != 32 {
			return shared.CanonicalFrameV1{}, fmt.Errorf("model ID at index %d must be 32 bytes", index)
		}
		if index > 0 && bytes.Compare(models[index-1], modelID) >= 0 {
			return shared.CanonicalFrameV1{}, fmt.Errorf("model IDs must be strictly ascending and unique")
		}
		fields[index] = shared.RawCanonicalFieldV1(modelID)
	}
	repeated := shared.CanonicalRepeatedFieldsV1(fields)
	return repeated, repeated.Err()
}

// VerifyStrictSecp256k1Digest verifies a detached business signature directly
// over its already-derived 32-byte signing digest.
func VerifyStrictSecp256k1Digest(pubKey cryptotypes.PubKey, digest, signature []byte) error {
	return shared.VerifyStrictSecp256k1Digest(pubKey, digest, signature)
}

func SignSecp256k1DigestForTest(priv *secp256k1.PrivKey, digest []byte) ([]byte, error) {
	return shared.SignSecp256k1DigestForTest(priv, digest)
}
