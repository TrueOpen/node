package types

import (
	errorsmod "cosmossdk.io/errors"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// VerifyStrictSecp256k1Digest performs the real cryptographic check for a raw
// secp256k1 signature against an already-derived signing digest.
//
// All production task handlers route detached signatures through this helper
// after resolving the signer public key from AuthKeeper or the registered
// role identity. It remains in types so SDK clients can verify the same
// canonical bytes offline without depending on Keeper code.
//
// Signature format: 64 raw bytes (r || s). Cosmos secp256k1 VerifySignature
// accepts exactly the compact 64-byte form. Recoverable 65-byte signatures are
// rejected so every accepted signature has one wire representation.
//
// Returns nil on success, ErrInvalidSignature otherwise. Empty, malformed and
// wrong-key signatures are always rejected.
func VerifyStrictSecp256k1Digest(pubKey cryptotypes.PubKey, digest, signature []byte) error {
	if err := shared.VerifyStrictSecp256k1Digest(pubKey, digest, signature); err != nil {
		return errorsmod.Wrap(ErrInvalidSignature, err.Error())
	}
	return nil
}

func SignSecp256k1DigestForTest(priv *secp256k1.PrivKey, digest []byte) ([]byte, error) {
	return shared.SignSecp256k1DigestForTest(priv, digest)
}
