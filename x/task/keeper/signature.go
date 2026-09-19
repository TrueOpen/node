package keeper

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) VerifyDigestSignature(ctx context.Context, signer string, signature, signingDigest []byte) error {
	addr, canonical, err := k.canonicalAddress("signer", signer)
	if err != nil {
		return err
	}
	account := k.authKeeper.GetAccount(ctx, sdk.AccAddress(addr))
	if account == nil || account.GetPubKey() == nil {
		return fmt.Errorf("account public key is required for signer %s", canonical)
	}
	return types.VerifyStrictSecp256k1Digest(account.GetPubKey(), signingDigest, signature)
}

func (k Keeper) RequireCurrentServiceSubmitter(ctx context.Context, participantType, operatorAddress, submitter string) error {
	_, operator, err := k.canonicalAddress("operator_address", operatorAddress)
	if err != nil {
		return err
	}
	_, signer, err := k.canonicalAddress("submitter", submitter)
	if err != nil {
		return err
	}
	binding, err := k.hubKeeper.GetCurrentServiceKey(ctx, participantType, operator)
	if err != nil {
		return fmt.Errorf("current service key unavailable for %s: %w", operator, err)
	}
	if strings.TrimSpace(binding.ParticipantType) != participantType || strings.TrimSpace(binding.OperatorAddress) != operator {
		return fmt.Errorf("current service key identity mismatch for %s", operator)
	}
	if binding.Status != hubtypes.ServiceKeyStatusActive {
		return fmt.Errorf("current service key for %s is not active", operator)
	}
	if strings.TrimSpace(binding.ServiceAddress) != signer {
		return fmt.Errorf("submitter must equal the current service address for %s", operator)
	}
	return nil
}

func (k Keeper) canonicalAddress(field, value string) ([]byte, string, error) {
	return shared.CanonicalAddress(k.addressCodec, field, value)
}
