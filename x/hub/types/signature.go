package types

import (
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// Every consensus domain used below resolves through the shared registry so an
// unregistered domain cannot reach a digest (the API contract rule 1).
// Ordered field encodings stay in the helper that owns each digest; the registry
// records framing, field order, producer, consumers, Store, Event and Query.
var (
	DomainSupportProfilesV1 = shared.MustDomain(shared.DomainSupportProfilesV1)

	DomainDailySupportConfirmationV1 = shared.MustDomain(shared.DomainDailySupportConfirmationV1)
)

// CanonicalSupportedProfilesHash commits to an already-canonical ordered list.
// Callers must reject unsorted or duplicate entries before using this digest.
//
// Deprecated: node production uses CanonicalSupportedProfilesHashV1 so typed
// nested errors are explicit. This compatibility wrapper delegates to that sole
// implementation and returns nil when its legacy signature cannot report a
// canonical framing error.
func CanonicalSupportedProfilesHash(profiles []ProfileKeyV1) []byte {
	digest, err := CanonicalSupportedProfilesHashV1(profiles)
	if err != nil {
		return nil
	}
	return digest
}

func CanonicalSupportedProfilesHashV1(profiles []ProfileKeyV1) ([]byte, error) {
	profileFrames := make([]shared.CanonicalFrameV1, len(profiles))
	for index, profile := range profiles {
		profileFrames[index] = canonicalProfileKeyFrame(profile)
	}
	repeatedProfiles := shared.CanonicalRepeatedFramesV1(profileFrames)
	if err := repeatedProfiles.Err(); err != nil {
		return nil, err
	}
	return shared.NewCanonicalHashBuilderV1(DomainSupportProfilesV1).Raw(
		shared.Uint32BE(uint32(len(profiles))),
	).Nested(repeatedProfiles).Sum()
}

// CanonicalDailySupportConfirmationSigningBytes is the legacy no-error wrapper.
// Deprecated: node production uses CanonicalDailySupportConfirmationSigningBytesV1.
func CanonicalDailySupportConfirmationSigningBytes(
	chainID string,
	operatorAddress []byte,
	epochIndex, serviceAuthorizationNonce, expiryHeight uint64,
	profiles []ProfileKeyV1,
) []byte {
	digest, err := CanonicalDailySupportConfirmationSigningBytesV1(
		chainID, operatorAddress, epochIndex, serviceAuthorizationNonce, expiryHeight, profiles,
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
	profiles []ProfileKeyV1,
) ([]byte, error) {
	profileFrames := make([]shared.CanonicalFrameV1, len(profiles))
	for index, profile := range profiles {
		profileFrames[index] = canonicalProfileKeyFrame(profile)
	}
	repeatedProfiles := shared.CanonicalRepeatedFramesV1(profileFrames)
	if err := repeatedProfiles.Err(); err != nil {
		return nil, err
	}
	return shared.NewCanonicalHashBuilderV1(DomainDailySupportConfirmationV1).Raw(
		[]byte(chainID), operatorAddress, shared.Uint64BE(epochIndex),
		shared.Uint64BE(serviceAuthorizationNonce), shared.Uint64BE(expiryHeight),
		shared.Uint32BE(uint32(len(profiles))),
	).Nested(repeatedProfiles).Sum()
}

func canonicalProfileKeyFrame(profile ProfileKeyV1) shared.CanonicalFrameV1 {
	return shared.FlatCanonicalFrameV1([]byte(profile.ModelId), shared.Uint32BE(profile.ProfileVersion))
}

// VerifyStrictSecp256k1Digest verifies a detached business signature directly
// over its already-derived 32-byte signing digest.
func VerifyStrictSecp256k1Digest(pubKey cryptotypes.PubKey, digest, signature []byte) error {
	return shared.VerifyStrictSecp256k1Digest(pubKey, digest, signature)
}

func SignSecp256k1DigestForTest(priv *secp256k1.PrivKey, digest []byte) ([]byte, error) {
	return shared.SignSecp256k1DigestForTest(priv, digest)
}
