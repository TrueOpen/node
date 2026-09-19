package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	storetypes "cosmossdk.io/store/types"
	txsigning "cosmossdk.io/x/tx/signing"

	errorsmod "cosmossdk.io/errors"
	"github.com/cosmos/evm/crypto/ethsecp256k1"
	cosmoseip712 "github.com/cosmos/evm/ethereum/eip712"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"golang.org/x/crypto/sha3"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	"github.com/cosmos/cosmos-sdk/x/auth/ante"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	hubkeeper "github.com/TrueOpen/node/x/hub/keeper"
	shared "github.com/TrueOpen/node/x/shared/types"
)

const web3ExtensionTypeURL = "/cosmos.evm.eip712.v1.ExtensionOptionsWeb3Tx"

type trueopenSignaturePath uint8

const (
	trueopenSignaturePathDirect trueopenSignaturePath = iota + 1
	trueopenSignaturePathWeb3
)

type signatureEnvelope struct {
	path trueopenSignaturePath
	web3 *cosmoseip712.ExtensionOptionsWeb3Tx
}

type trueopenSignaturePathDecorator struct {
	hub hubkeeper.Keeper
}

func newSignaturePathDecorator(hub hubkeeper.Keeper) trueopenSignaturePathDecorator {
	return trueopenSignaturePathDecorator{hub: hub}
}

func (d trueopenSignaturePathDecorator) AnteHandle(
	ctx sdk.Context,
	tx sdk.Tx,
	simulate bool,
	next sdk.AnteHandler,
) (sdk.Context, error) {
	if _, err := inspectSignatureEnvelope(tx, simulate, d.hub.GetHubParams(ctx).EVMChainID); err != nil {
		return ctx, err
	}
	return next(ctx, tx, simulate)
}

type trueopenSignatureVerificationDecorator struct {
	accounts         ante.AccountKeeper
	hub              hubkeeper.Keeper
	signModeHandler  *txsigning.HandlerMap
	amino            *codec.LegacyAmino
	stockStateChecks ante.SigVerificationDecorator
}

func newSignatureVerificationDecorator(
	accounts ante.AccountKeeper,
	hub hubkeeper.Keeper,
	signModeHandler *txsigning.HandlerMap,
	amino *codec.LegacyAmino,
	stockStateChecks ante.SigVerificationDecorator,
) trueopenSignatureVerificationDecorator {
	return trueopenSignatureVerificationDecorator{
		accounts: accounts, hub: hub, signModeHandler: signModeHandler, amino: amino, stockStateChecks: stockStateChecks,
	}
}

func (d trueopenSignatureVerificationDecorator) AnteHandle(
	ctx sdk.Context,
	tx sdk.Tx,
	simulate bool,
	next sdk.AnteHandler,
) (sdk.Context, error) {
	verifySignatures := ctx.IsSigverifyTx()
	// Preserve the SDK's sequence and unordered-nonce logic while disabling its
	// signature call. ethsecp256k1.PubKey.VerifySignature has a raw/EIP-712
	// fallback, so it cannot enforce TrueOpen's extension-only path boundary.
	return d.stockStateChecks.AnteHandle(ctx.WithIsSigverifyTx(false), tx, simulate,
		func(checkedCtx sdk.Context, checkedTx sdk.Tx, checkedSimulate bool) (sdk.Context, error) {
			checkedCtx = checkedCtx.WithIsSigverifyTx(verifySignatures)
			if checkedSimulate || checkedCtx.IsReCheckTx() || !verifySignatures {
				return next(checkedCtx, checkedTx, checkedSimulate)
			}
			if err := d.verify(checkedCtx, checkedTx); err != nil {
				return checkedCtx, err
			}
			return next(checkedCtx, checkedTx, checkedSimulate)
		})
}

func (d trueopenSignatureVerificationDecorator) verify(ctx sdk.Context, tx sdk.Tx) error {
	sigTx, ok := tx.(authsigning.SigVerifiableTx)
	if !ok {
		return errorsmod.Wrap(sdkerrors.ErrTxDecode, "transaction does not expose signatures")
	}
	signers, err := sigTx.GetSigners()
	if err != nil {
		return err
	}
	signatures, err := sigTx.GetSignaturesV2()
	if err != nil {
		return err
	}
	envelope, err := inspectSignatureEnvelope(tx, false, d.hub.GetHubParams(ctx).EVMChainID)
	if err != nil {
		return err
	}

	for i, signer := range signers {
		account, err := ante.GetSignerAcc(ctx, d.accounts, signer)
		if err != nil {
			return err
		}
		publicKey, err := requireEthSecp256k1PublicKey(account.GetPubKey())
		if err != nil {
			return errorsmod.Wrapf(sdkerrors.ErrInvalidPubKey, "signer %d: %v", i, err)
		}
		if supplied := signatures[i].PubKey; supplied != nil && !publicKey.Equals(supplied) {
			return errorsmod.Wrapf(sdkerrors.ErrInvalidPubKey, "signer %d public key differs from stored account key", i)
		}

		var accountNumber uint64
		if ctx.BlockHeight() != 0 {
			accountNumber = account.GetAccountNumber()
		}
		single := signatures[i].Data.(*signing.SingleSignatureData)
		switch envelope.path {
		case trueopenSignaturePathDirect:
			signerData := authsigning.SignerData{
				Address:       account.GetAddress().String(),
				ChainID:       ctx.ChainID(),
				AccountNumber: accountNumber,
				Sequence:      signatures[i].Sequence,
				PubKey:        publicKey,
			}
			signDocBytes, err := authsigning.GetSignBytesAdapter(
				ctx, d.signModeHandler, signing.SignMode_SIGN_MODE_DIRECT, signerData, tx,
			)
			if err != nil {
				return errorsmod.Wrap(sdkerrors.ErrUnauthorized, err.Error())
			}
			digest := keccak256(signDocBytes)
			if !ethcrypto.VerifySignature(publicKey.Bytes(), digest[:], single.Signature) {
				return errorsmod.Wrapf(sdkerrors.ErrUnauthorized, "signer %d DIRECT signature verification failed", i)
			}

		case trueopenSignaturePathWeb3:
			signDocBytes, err := canonicalAminoSignDoc(
				d.amino, ctx.ChainID(), accountNumber, signatures[i].Sequence, tx.(authsigning.Tx),
			)
			if err != nil {
				return errorsmod.Wrap(sdkerrors.ErrUnauthorized, err.Error())
			}
			typedData, err := buildWeb3TypedData(envelope.web3.TypedDataChainID, signDocBytes)
			if err != nil {
				return errorsmod.Wrap(sdkerrors.ErrUnauthorized, err.Error())
			}
			digest, _, err := apitypes.TypedDataAndHash(typedData)
			if err != nil {
				return errorsmod.Wrap(sdkerrors.ErrUnauthorized, err.Error())
			}
			recovered, err := shared.RecoverSecp256k1Signer(digest, single.Signature)
			if err != nil {
				return errorsmod.Wrap(sdkerrors.ErrUnauthorized, err.Error())
			}
			if !bytes.Equal(recovered.PublicKey, publicKey.Bytes()) || !bytes.Equal(recovered.Address, signer) {
				return errorsmod.Wrapf(sdkerrors.ErrUnauthorized, "signer %d Web3 signature does not match stored account key", i)
			}
		default:
			return errorsmod.Wrap(sdkerrors.ErrLogic, "unreachable signature path")
		}
	}
	return nil
}

func inspectSignatureEnvelope(
	tx sdk.Tx,
	simulate bool,
	expectedEVMChainID uint64,
) (signatureEnvelope, error) {
	extTx, ok := tx.(ante.HasExtensionOptionsTx)
	if !ok {
		return signatureEnvelope{}, errorsmod.Wrap(sdkerrors.ErrTxDecode, "transaction does not expose extension options")
	}
	if len(extTx.GetNonCriticalExtensionOptions()) != 0 {
		return signatureEnvelope{}, errorsmod.Wrap(sdkerrors.ErrUnknownExtensionOptions, "non-critical extension options are not supported")
	}
	options := extTx.GetExtensionOptions()

	sigTx, ok := tx.(authsigning.SigVerifiableTx)
	if !ok {
		return signatureEnvelope{}, errorsmod.Wrap(sdkerrors.ErrTxDecode, "transaction does not expose signatures")
	}
	signers, err := sigTx.GetSigners()
	if err != nil {
		return signatureEnvelope{}, err
	}
	signatures, err := sigTx.GetSignaturesV2()
	if err != nil {
		return signatureEnvelope{}, err
	}
	if len(signers) == 0 || len(signatures) != len(signers) {
		return signatureEnvelope{}, errorsmod.Wrapf(
			sdkerrors.ErrUnauthorized, "signature count %d does not match signer count %d", len(signatures), len(signers),
		)
	}

	envelope := signatureEnvelope{path: trueopenSignaturePathDirect}
	requiredMode := signing.SignMode_SIGN_MODE_DIRECT
	if len(options) != 0 {
		if len(options) != 1 || options[0] == nil || options[0].TypeUrl != web3ExtensionTypeURL {
			return signatureEnvelope{}, errorsmod.Wrap(sdkerrors.ErrUnknownExtensionOptions, "unsupported critical extension option shape")
		}
		web3 := new(cosmoseip712.ExtensionOptionsWeb3Tx)
		if err := web3.Unmarshal(options[0].Value); err != nil {
			return signatureEnvelope{}, errorsmod.Wrap(sdkerrors.ErrUnknownExtensionOptions, "invalid Web3 extension option")
		}
		if expectedEVMChainID != 0 && web3.TypedDataChainID != expectedEVMChainID {
			return signatureEnvelope{}, errorsmod.Wrapf(
				sdkerrors.ErrInvalidChainID, "typed_data_chain_id %d does not match committed evm_chain_id %d", web3.TypedDataChainID, expectedEVMChainID,
			)
		}
		if web3.TypedDataChainID == 0 || web3.TypedDataChainID > uint64(math.MaxInt64) {
			return signatureEnvelope{}, errorsmod.Wrap(sdkerrors.ErrInvalidChainID, "typed_data_chain_id is outside the supported range")
		}
		if web3.FeePayer != "" || len(web3.FeePayerSig) != 0 {
			return signatureEnvelope{}, errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "Web3 extension fee delegation is not supported")
		}
		envelope.path, envelope.web3 = trueopenSignaturePathWeb3, web3
		requiredMode = signing.SignMode_SIGN_MODE_LEGACY_AMINO_JSON

		authTx, ok := tx.(authsigning.Tx)
		if !ok {
			return signatureEnvelope{}, errorsmod.Wrap(sdkerrors.ErrTxDecode, "transaction does not expose auth fields")
		}
		adaptable, ok := tx.(authsigning.V2AdaptableTx)
		if !ok {
			return signatureEnvelope{}, errorsmod.Wrap(sdkerrors.ErrTxDecode, "transaction does not expose canonical TxRaw bytes")
		}
		txData := adaptable.GetSigningTxData()
		if len(authTx.GetMsgs()) != 1 || len(signers) != 1 || len(signatures) != 1 {
			return signatureEnvelope{}, errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "Web3 path requires exactly one message and one signer")
		}
		if authTx.GetTimeoutHeight() != 0 || authTx.GetUnordered() || txData.Body == nil || txData.Body.TimeoutTimestamp != nil {
			return signatureEnvelope{}, errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "Web3 path requires the default timeout and ordered transaction fields")
		}
		if txData.AuthInfo == nil || txData.AuthInfo.Fee == nil || txData.AuthInfo.Fee.Payer != "" || txData.AuthInfo.Fee.Granter != "" {
			return signatureEnvelope{}, errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "Web3 path fee payer and granter must be empty")
		}
	}

	for i, signature := range signatures {
		single, ok := signature.Data.(*signing.SingleSignatureData)
		if !ok || single.SignMode != requiredMode {
			return signatureEnvelope{}, errorsmod.Wrapf(sdkerrors.ErrUnauthorized, "signer %d uses an invalid signature mode", i)
		}
		if signature.PubKey != nil {
			if _, err := requireEthSecp256k1PublicKey(signature.PubKey); err != nil {
				return signatureEnvelope{}, errorsmod.Wrapf(sdkerrors.ErrInvalidPubKey, "signer %d: %v", i, err)
			}
		}
		if simulate && len(single.Signature) == 0 {
			continue
		}
		if envelope.path == trueopenSignaturePathDirect {
			if err := shared.RequireCanonicalSecp256k1Signature(single.Signature); err != nil {
				return signatureEnvelope{}, errorsmod.Wrapf(sdkerrors.ErrUnauthorized, "signer %d: %v", i, err)
			}
		} else if err := shared.RequireRecoverableSecp256k1Signature(single.Signature); err != nil {
			return signatureEnvelope{}, errorsmod.Wrapf(sdkerrors.ErrUnauthorized, "signer %d: %v", i, err)
		}
	}
	return envelope, nil
}

func requireEthSecp256k1PublicKey(publicKey cryptotypes.PubKey) (*ethsecp256k1.PubKey, error) {
	key, ok := publicKey.(*ethsecp256k1.PubKey)
	if !ok || key == nil {
		return nil, fmt.Errorf("public key must be eth_secp256k1")
	}
	if len(key.Key) != ethsecp256k1.PubKeySize || (key.Key[0] != 0x02 && key.Key[0] != 0x03) {
		return nil, fmt.Errorf("eth_secp256k1 public key must be a compressed 33-byte point")
	}
	parsed, err := ethcrypto.DecompressPubkey(key.Key)
	if err != nil || !bytes.Equal(ethcrypto.CompressPubkey(parsed), key.Key) {
		return nil, fmt.Errorf("eth_secp256k1 public key is not canonical")
	}
	return key, nil
}

type aminoCoinJSON struct {
	Amount string `json:"amount"`
	Denom  string `json:"denom"`
}

type aminoFeeJSON struct {
	Amount []aminoCoinJSON `json:"amount"`
	Gas    string          `json:"gas"`
}

type aminoSignDocJSON struct {
	AccountNumber string            `json:"account_number"`
	ChainID       string            `json:"chain_id"`
	Fee           aminoFeeJSON      `json:"fee"`
	Memo          string            `json:"memo"`
	Msgs          []json.RawMessage `json:"msgs"`
	Sequence      string            `json:"sequence"`
}

func canonicalAminoSignDoc(
	amino *codec.LegacyAmino,
	chainID string,
	accountNumber, sequence uint64,
	tx authsigning.Tx,
) ([]byte, error) {
	if amino == nil {
		return nil, fmt.Errorf("legacy amino codec is required")
	}
	msgs := tx.GetMsgs()
	msgJSON := make([]json.RawMessage, len(msgs))
	for i, msg := range msgs {
		encoded, err := amino.MarshalJSON(msg)
		if err != nil {
			return nil, fmt.Errorf("marshal message %d to canonical amino JSON: %w", i, err)
		}
		encoded, err = sdk.SortJSON(encoded)
		if err != nil {
			return nil, fmt.Errorf("sort message %d amino JSON: %w", i, err)
		}
		msgJSON[i] = encoded
	}
	coins := tx.GetFee()
	feeCoins := make([]aminoCoinJSON, len(coins))
	for i, coin := range coins {
		feeCoins[i] = aminoCoinJSON{Amount: coin.Amount.String(), Denom: coin.Denom}
	}
	docJSON, err := json.Marshal(aminoSignDocJSON{
		AccountNumber: strconv.FormatUint(accountNumber, 10),
		ChainID:       chainID,
		Fee:           aminoFeeJSON{Amount: feeCoins, Gas: strconv.FormatUint(tx.GetGas(), 10)},
		Memo:          tx.GetMemo(),
		Msgs:          msgJSON,
		Sequence:      strconv.FormatUint(sequence, 10),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal canonical amino sign doc: %w", err)
	}
	return sdk.SortJSON(docJSON)
}

// buildWeb3TypedData uses the pinned v0.6.3 parser and recursive type
// inference, then applies the frozen TrueOpen name for the first occurrence of a
// custom type. v0.6.3 internally appends index 0 even when no duplicate exists;
// the published account vector names the first occurrence without that suffix.
// Duplicate occurrences keep their 1+ suffix, so the upstream de-duplication
// behavior remains intact.
func buildWeb3TypedData(chainID uint64, signDoc []byte) (apitypes.TypedData, error) {
	typedData, err := cosmoseip712.WrapTxToTypedData(chainID, signDoc)
	if err != nil {
		return apitypes.TypedData{}, err
	}
	renames := make(map[string]string)
	for name := range typedData.Types {
		if strings.HasPrefix(name, "Type") && strings.HasSuffix(name, "0") {
			base := strings.TrimSuffix(name, "0")
			if _, exists := typedData.Types[base]; exists {
				return apitypes.TypedData{}, fmt.Errorf("ambiguous EIP-712 type names %q and %q", name, base)
			}
			renames[name] = base
		}
	}
	for oldName, newName := range renames {
		typedData.Types[newName] = typedData.Types[oldName]
		delete(typedData.Types, oldName)
	}
	for name, fields := range typedData.Types {
		for i := range fields {
			arraySuffix := ""
			fieldType := fields[i].Type
			if strings.HasSuffix(fieldType, "[]") {
				fieldType = strings.TrimSuffix(fieldType, "[]")
				arraySuffix = "[]"
			}
			if renamed, ok := renames[fieldType]; ok {
				fields[i].Type = renamed + arraySuffix
			}
		}
		typedData.Types[name] = fields
	}
	return typedData, nil
}

func keccak256(input []byte) [32]byte {
	hash := sha3.NewLegacyKeccak256()
	_, _ = hash.Write(input)
	var digest [32]byte
	hash.Sum(digest[:0])
	return digest
}

func trueopenSignatureGasConsumer(
	meter storetypes.GasMeter,
	sig signing.SignatureV2,
	params authtypes.Params,
) error {
	switch sig.PubKey.(type) {
	case *ethsecp256k1.PubKey:
		if _, err := requireEthSecp256k1PublicKey(sig.PubKey); err != nil {
			return errorsmod.Wrap(sdkerrors.ErrInvalidPubKey, err.Error())
		}
		meter.ConsumeGas(params.SigVerifyCostSecp256k1, "ante verify: eth_secp256k1")
		return nil
	case *secp256k1.PubKey:
		// The stock gas decorator substitutes this key for unsigned simulation.
		// Real standard-secp accounts still fail the strict verifier below.
		return ante.DefaultSigVerificationGasConsumer(meter, sig, params)
	default:
		return errorsmod.Wrapf(sdkerrors.ErrInvalidPubKey, "unrecognized public key type: %T", sig.PubKey)
	}
}

var _ sdk.AnteDecorator = trueopenSignaturePathDecorator{}
var _ sdk.AnteDecorator = trueopenSignatureVerificationDecorator{}
