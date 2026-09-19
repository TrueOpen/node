package types

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/cosmos/cosmos-sdk/types/bech32"
	dcrsecp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"golang.org/x/crypto/sha3"

	shared "github.com/TrueOpen/node/x/shared/types"
)

const (
	SignatureSchemeEIP712        = "eip712"
	TaskOrderEIP712DomainName    = "TrueOpen Task Order"
	TaskOrderEIP712DomainVersion = "2"
	EthSecp256k1PublicKeyType    = "eth_secp256k1"

	taskOrderEIP712DomainType  = "EIP712Domain(string name,string version,uint256 chainId)"
	taskOrderEIP712MessageType = "TaskOrder(string chainId,string user,bytes32 sessionId,uint64 orderSequence,string modelId,uint32 profileVersion,string maxFee,string feeDenom,uint64 earliestSubmitHeight,uint64 orderExpireHeight,bytes32 taskHash)"
	taskOrderAccountHRP        = "trueopen"
)

// TaskOrderEIP712Digest contains the three public §6.5/§6.6 conformance
// checkpoints. HashStruct is the EIP-712 TaskOrder message hash.
type TaskOrderEIP712Digest struct {
	DomainSeparator [32]byte
	HashStruct      [32]byte
	SigningDigest   [32]byte
}

// TaskOrderEIP712AccountPublicKey is the narrow auth-account key projection
// needed by the pure verifier. The caller must pass the account's already
// decoded public key; Type keeps the eth_secp256k1 type check inside this
// consensus helper instead of relying on a handler convention.
type TaskOrderEIP712AccountPublicKey interface {
	Bytes() []byte
	Type() string
}

// EIP712RecoveredSigner is the canonical recovered identity. PublicKey is the
// 33-byte compressed SEC1 point and Address is the 20-byte EVM/TrueOpen address.
type EIP712RecoveredSigner struct {
	PublicKey []byte
	Address   []byte
}

// BuildTaskOrderEIP712Digest implements the exact SignedOrderV2 order domain.
// taskHash is explicit because the public EIP-712 conformance vector treats it
// as an opaque bytes32. VerifySignedOrderV2EIP712* additionally recomputes it
// from the complete TaskOrderV2 before accepting a signature.
func BuildTaskOrderEIP712Digest(
	evmChainID uint64,
	businessDenom string,
	order TaskOrderV2,
	taskHash []byte,
) (TaskOrderEIP712Digest, error) {
	if evmChainID == 0 || evmChainID > uint64(math.MaxInt64) {
		return TaskOrderEIP712Digest{}, fmt.Errorf("evm_chain_id must be in 1..%d", int64(math.MaxInt64))
	}
	if businessDenom == "" || businessDenom != strings.TrimSpace(businessDenom) || !utf8.ValidString(businessDenom) {
		return TaskOrderEIP712Digest{}, fmt.Errorf("business_denom must be non-empty canonical UTF-8")
	}
	// This is the existing single full TaskOrderV2 schema/canonical validator.
	// Its digest is deliberately not substituted for taskHash here: callers of
	// this low-level builder may be checking the published opaque EIP-712 vector.
	if _, err := TaskOrderHash(order); err != nil {
		return TaskOrderEIP712Digest{}, fmt.Errorf("task order: %w", err)
	}
	if _, err := canonicalTaskOrderEIP712User(order.UserAddress); err != nil {
		return TaskOrderEIP712Digest{}, err
	}
	if len(taskHash) != Hash32Len {
		return TaskOrderEIP712Digest{}, fmt.Errorf("task_hash must be 32 bytes")
	}

	domainTypeHash := eip712Keccak256([]byte(taskOrderEIP712DomainType))
	domainSeparator := eip712Keccak256(
		domainTypeHash[:],
		eip712StringWord(TaskOrderEIP712DomainName),
		eip712StringWord(TaskOrderEIP712DomainVersion),
		eip712Uint64Word(evmChainID),
	)

	messageTypeHash := eip712Keccak256([]byte(taskOrderEIP712MessageType))
	hashStruct := eip712Keccak256(
		messageTypeHash[:],
		eip712StringWord(order.ChainId),
		eip712StringWord(order.UserAddress),
		order.SessionId,
		eip712Uint64Word(order.OrderSequence),
		eip712StringWord(order.ModelId),
		eip712Uint32Word(order.ProfileVersion),
		eip712StringWord(order.MaxFee.AtomicUnits),
		eip712StringWord(businessDenom),
		eip712Uint64Word(order.EarliestSubmitHeight),
		eip712Uint64Word(order.OrderExpireHeight),
		taskHash,
	)
	signingDigest := eip712Keccak256([]byte{0x19, 0x01}, domainSeparator[:], hashStruct[:])
	return TaskOrderEIP712Digest{
		DomainSeparator: domainSeparator,
		HashStruct:      hashStruct,
		SigningDigest:   signingDigest,
	}, nil
}

// RequireCanonicalEIP712Signature enforces the sole SignedOrderV2 transport
// form: R||S||V, V 27/28, with non-zero in-range R/S and low-S. The order domain
// and the bridge signer PoP share that form, so the rule lives in shared
// and this is only its named boundary.
func RequireCanonicalEIP712Signature(signature []byte) error {
	if err := shared.RequireRecoverableSecp256k1Signature(signature); err != nil {
		return fmt.Errorf("EIP-712 signature: %w", err)
	}
	return nil
}

// RecoverEIP712Signer recovers directly from the already-derived EIP-712
// signing digest. It never applies a second hash or an EIP-191 prefix.
func RecoverEIP712Signer(signingDigest, signature []byte) (EIP712RecoveredSigner, error) {
	recovered, err := shared.RecoverSecp256k1Signer(signingDigest, signature)
	if err != nil {
		return EIP712RecoveredSigner{}, fmt.Errorf("EIP-712: %w", err)
	}
	return EIP712RecoveredSigner{PublicKey: recovered.PublicKey, Address: recovered.Address}, nil
}

// VerifySignedOrderV2EIP712WithAccountPublicKey verifies a SignedOrderV2
// against the immutable public key already stored on the user's auth account.
func VerifySignedOrderV2EIP712WithAccountPublicKey(
	evmChainID uint64,
	businessDenom string,
	signedOrder SignedOrderV2,
	taskHash []byte,
	accountPublicKey TaskOrderEIP712AccountPublicKey,
) error {
	if accountPublicKey == nil {
		return fmt.Errorf("user account public key is required")
	}
	if accountPublicKey.Type() != EthSecp256k1PublicKeyType {
		return fmt.Errorf("user account public key type must be %s", EthSecp256k1PublicKeyType)
	}
	publicKey, err := canonicalEthSecp256k1PublicKey(accountPublicKey.Bytes())
	if err != nil {
		return fmt.Errorf("user account public key: %w", err)
	}
	return verifySignedOrderV2EIP712(
		evmChainID, businessDenom, signedOrder, taskHash,
		publicKey.SerializeCompressed(), nil,
	)
}

// VerifySignedOrderV2EIP712WithAddress verifies a SignedOrderV2 against an
// explicit raw 20-byte expected address. Handlers with an auth account should
// prefer the public-key variant so both §4.3 comparisons are enforced.
func VerifySignedOrderV2EIP712WithAddress(
	evmChainID uint64,
	businessDenom string,
	signedOrder SignedOrderV2,
	taskHash, expectedAddress []byte,
) error {
	if len(expectedAddress) != 20 {
		return fmt.Errorf("expected user address must be 20 bytes")
	}
	return verifySignedOrderV2EIP712(
		evmChainID, businessDenom, signedOrder, taskHash,
		nil, append([]byte(nil), expectedAddress...),
	)
}

func verifySignedOrderV2EIP712(
	evmChainID uint64,
	businessDenom string,
	signedOrder SignedOrderV2,
	taskHash, expectedPublicKey, expectedAddress []byte,
) error {
	if signedOrder.SignatureScheme != SignatureSchemeEIP712 {
		return fmt.Errorf("signature_scheme must be exactly %q", SignatureSchemeEIP712)
	}
	recomputedTaskHash, err := TaskOrderHash(signedOrder.Order)
	if err != nil {
		return fmt.Errorf("task order: %w", err)
	}
	if len(taskHash) != Hash32Len || !bytes.Equal(recomputedTaskHash[:], taskHash) {
		return fmt.Errorf("task_hash does not match TaskOrderV2")
	}
	digest, err := BuildTaskOrderEIP712Digest(evmChainID, businessDenom, signedOrder.Order, taskHash)
	if err != nil {
		return err
	}
	recovered, err := RecoverEIP712Signer(digest.SigningDigest[:], signedOrder.UserSignature)
	if err != nil {
		return err
	}
	userAddress, err := canonicalTaskOrderEIP712User(signedOrder.Order.UserAddress)
	if err != nil {
		return err
	}
	if !bytes.Equal(recovered.Address, userAddress) {
		return fmt.Errorf("recovered signer does not match TaskOrderV2 user_address")
	}
	if len(expectedPublicKey) != 0 && !bytes.Equal(recovered.PublicKey, expectedPublicKey) {
		return fmt.Errorf("recovered signer public key does not match the user account public key")
	}
	if len(expectedAddress) != 0 && !bytes.Equal(recovered.Address, expectedAddress) {
		return fmt.Errorf("recovered signer does not match the expected user address")
	}
	return nil
}

func canonicalTaskOrderEIP712User(value string) ([]byte, error) {
	if value == "" || value != strings.TrimSpace(value) {
		return nil, fmt.Errorf("TaskOrderV2 user_address must be canonical Bech32")
	}
	hrp, raw, err := bech32.DecodeAndConvert(value)
	if err != nil || hrp != taskOrderAccountHRP || len(raw) != 20 {
		return nil, fmt.Errorf("TaskOrderV2 user_address must be a 20-byte %s address", taskOrderAccountHRP)
	}
	reencoded, err := bech32.ConvertAndEncode(taskOrderAccountHRP, raw)
	if err != nil || reencoded != value {
		return nil, fmt.Errorf("TaskOrderV2 user_address must be canonical Bech32")
	}
	return append([]byte(nil), raw...), nil
}

func canonicalEthSecp256k1PublicKey(raw []byte) (*dcrsecp256k1.PublicKey, error) {
	if len(raw) != 33 || (raw[0] != 0x02 && raw[0] != 0x03) {
		return nil, fmt.Errorf("public key must be a 33-byte compressed SEC1 point")
	}
	publicKey, err := dcrsecp256k1.ParsePubKey(raw)
	if err != nil || !bytes.Equal(publicKey.SerializeCompressed(), raw) {
		return nil, fmt.Errorf("public key is not a canonical compressed secp256k1 point")
	}
	return publicKey, nil
}

func eip712StringWord(value string) []byte {
	hash := eip712Keccak256([]byte(value))
	return hash[:]
}

func eip712Uint32Word(value uint32) []byte {
	word := make([]byte, 32)
	binary.BigEndian.PutUint32(word[28:], value)
	return word
}

func eip712Uint64Word(value uint64) []byte {
	word := make([]byte, 32)
	binary.BigEndian.PutUint64(word[24:], value)
	return word
}

func eip712Keccak256(parts ...[]byte) [32]byte {
	hash := sha3.NewLegacyKeccak256()
	for _, part := range parts {
		_, _ = hash.Write(part)
	}
	var result [32]byte
	hash.Sum(result[:0])
	return result
}
