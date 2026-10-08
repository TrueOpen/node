package app

import (
	"math"
	"testing"

	"github.com/cosmos/cosmos-sdk/codec/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	"github.com/stretchr/testify/require"

	cosmoseip712 "github.com/cosmos/evm/ethereum/eip712"
)

// web3OptionsTx exposes just enough of a transaction for inspectSignatureEnvelope
// to reach the typed-data chain id check.
type web3OptionsTx struct {
	authsigning.SigVerifiableTx
	options []*types.Any
}

func (tx web3OptionsTx) GetExtensionOptions() []*types.Any            { return tx.options }
func (tx web3OptionsTx) GetNonCriticalExtensionOptions() []*types.Any { return nil }
func (tx web3OptionsTx) GetSigners() ([][]byte, error)                { return [][]byte{{1}}, nil }
func (tx web3OptionsTx) GetSignaturesV2() ([]signing.SignatureV2, error) {
	return []signing.SignatureV2{{}}, nil
}

func newWeb3OptionsTx(t *testing.T, typedDataChainID uint64) web3OptionsTx {
	t.Helper()
	raw, err := (&cosmoseip712.ExtensionOptionsWeb3Tx{TypedDataChainID: typedDataChainID}).Marshal()
	require.NoError(t, err)
	return web3OptionsTx{options: []*types.Any{{TypeUrl: web3ExtensionTypeURL, Value: raw}}}
}

// TestInspectSignatureEnvelopeTypedDataChainIDIsSignerChosen pins ADR-0033: the
// typed-data chainId is whatever the signer's wallet network is, so only its
// range is checked. The stub transaction cannot go further than that check, so a
// range failure is told apart from "got past it" by the error it returns.
func TestInspectSignatureEnvelopeTypedDataChainIDIsSignerChosen(t *testing.T) {
	for _, id := range []uint64{1, 31337, 424242, uint64(math.MaxInt64)} {
		_, err := inspectSignatureEnvelope(newWeb3OptionsTx(t, id), false)
		require.NotErrorIs(t, err, sdkerrors.ErrInvalidChainID, "chain id %d must pass the range check", id)
	}
	for _, id := range []uint64{0, uint64(math.MaxInt64) + 1} {
		_, err := inspectSignatureEnvelope(newWeb3OptionsTx(t, id), false)
		require.ErrorIs(t, err, sdkerrors.ErrInvalidChainID, "chain id %d is outside 1..MaxInt64", id)
	}
}
