package cmd

import (
	"fmt"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	"github.com/cosmos/evm/crypto/ethsecp256k1"
)

// canonicalizeDirectTxSignatures runs after every SDK transaction signer and
// before encoding or broadcast. Unlike a keyring wrapper, this hook survives
// GetClientTxContext rebuilding the keyring from command flags (including
// genesis gentx). Web3 signatures use another sign mode and remain 65 bytes.
func canonicalizeDirectTxSignatures(_ string, _ keyring.KeyType, builder client.TxBuilder) error {
	signatures, err := builder.GetTx().GetSignaturesV2()
	if err != nil {
		return err
	}
	changed := false
	for index := range signatures {
		single, ok := signatures[index].Data.(*signing.SingleSignatureData)
		if !ok {
			continue
		}
		normalized, _, err := canonicalDirectSignature(single.Signature, signatures[index].PubKey, single.SignMode)
		if err != nil {
			return err
		}
		if len(normalized) == len(single.Signature) {
			continue
		}
		copySingle := *single
		copySingle.Signature = normalized
		signatures[index].Data = &copySingle
		changed = true
	}
	if !changed {
		return nil
	}
	return builder.SetSignatures(signatures...)
}

func canonicalDirectSignature(
	sig []byte,
	pubKey cryptotypes.PubKey,
	signMode signing.SignMode,
) ([]byte, cryptotypes.PubKey, error) {
	if signMode != signing.SignMode_SIGN_MODE_DIRECT {
		return sig, pubKey, nil
	}
	if _, ok := pubKey.(*ethsecp256k1.PubKey); !ok {
		return sig, pubKey, nil
	}
	switch len(sig) {
	case 64:
		return sig, pubKey, nil
	case 65:
		if sig[64] > 1 {
			return nil, nil, fmt.Errorf("eth_secp256k1 DIRECT signature has invalid recovery id %d", sig[64])
		}
		return append([]byte(nil), sig[:64]...), pubKey, nil
	default:
		return nil, nil, fmt.Errorf("eth_secp256k1 DIRECT signature must be 64 or 65 bytes, got %d", len(sig))
	}
}
