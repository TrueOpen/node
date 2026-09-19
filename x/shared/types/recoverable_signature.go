package types

import (
	"fmt"

	dcrsecp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	dcrecdsa "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

// RecoverableSecp256k1SignatureBytes is the R||S||V transport length shared by
// the SignedOrderV2 order domain (account_and_signature_protocol.md §10.1a) and the
// bridge signer PoP (cross_chain_asset_bridge_protocol.md §4.1). Both are
// recoverable signatures over an already
// derived 32-byte digest; neither applies a second hash or an EIP-191 prefix.
const RecoverableSecp256k1SignatureBytes = 65

// EVMAddressBytes is the raw width of an EVM-style 20-byte address. Bridge
// signer identities are EVM addresses owned by upstream Hyperlane fields, not
// TrueOpen accounts, so they never cross the bech32 boundary.
const EVMAddressBytes = 20

// RequireRecoverableSecp256k1Signature enforces the single recoverable wire
// form: exactly 65 bytes R||S||V with V in {27,28} and a canonical low-S R||S
// prefix. Keeping the V check here and delegating the rest means a recoverable
// signature and a compact one can never disagree about what "canonical" means.
func RequireRecoverableSecp256k1Signature(signature []byte) error {
	if len(signature) != RecoverableSecp256k1SignatureBytes {
		return fmt.Errorf("recoverable secp256k1 signature must be exactly %d bytes in R||S||V form, got %d",
			RecoverableSecp256k1SignatureBytes, len(signature))
	}
	if signature[64] != 27 && signature[64] != 28 {
		return fmt.Errorf("recoverable secp256k1 signature V must be 27 or 28")
	}
	return RequireCanonicalSecp256k1Signature(signature[:CompactSecp256k1SignatureBytes])
}

// RecoveredSecp256k1Signer is the canonical recovered identity: the 33-byte
// compressed SEC1 point and the 20-byte EVM-style address derived from it.
type RecoveredSecp256k1Signer struct {
	PublicKey []byte
	Address   []byte
}

// RecoverSecp256k1Signer recovers the signer of an already-derived 32-byte
// digest. It is the only recovery implementation in the repository: the order
// domain and the bridge PoP differ in which digest they derive, never in how a
// signature over that digest is interpreted.
func RecoverSecp256k1Signer(digest, signature []byte) (RecoveredSecp256k1Signer, error) {
	if len(digest) != Hash32KeySize {
		return RecoveredSecp256k1Signer{}, fmt.Errorf("recoverable signing digest must be %d bytes", Hash32KeySize)
	}
	if err := RequireRecoverableSecp256k1Signature(signature); err != nil {
		return RecoveredSecp256k1Signer{}, err
	}
	// dcrec expects V first; the wire form carries it last.
	compact := make([]byte, RecoverableSecp256k1SignatureBytes)
	compact[0] = signature[64]
	copy(compact[1:], signature[:CompactSecp256k1SignatureBytes])
	publicKey, _, err := dcrecdsa.RecoverCompact(compact, digest)
	if err != nil {
		return RecoveredSecp256k1Signer{}, fmt.Errorf("recover secp256k1 signer: %w", err)
	}
	address := EVMAddressFromSecp256k1PublicKey(publicKey)
	return RecoveredSecp256k1Signer{
		PublicKey: append([]byte(nil), publicKey.SerializeCompressed()...),
		Address:   append([]byte(nil), address[:]...),
	}, nil
}

// EVMAddressFromSecp256k1PublicKey is keccak256(uncompressed[1:])[12:].
func EVMAddressFromSecp256k1PublicKey(publicKey *dcrsecp256k1.PublicKey) [EVMAddressBytes]byte {
	uncompressed := publicKey.SerializeUncompressed()
	hash := sha3.NewLegacyKeccak256()
	_, _ = hash.Write(uncompressed[1:])
	var digest [32]byte
	hash.Sum(digest[:0])
	var address [EVMAddressBytes]byte
	copy(address[:], digest[12:])
	return address
}
