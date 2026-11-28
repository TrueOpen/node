package keeper

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// requireSeparateFromConsensus refuses an audit capability for an operator
// whose account, service address or service key belongs to block production.
// Panel audit replaces the consensus validators' own audit vote, so the two
// groups must not overlap. The rule is in force when the service parameter
// min_validator_separation is set. A chain that sets it without an identity
// lookup bound refuses every audit declaration instead of accepting one
// unchecked.
func (k Keeper) requireSeparateFromConsensus(ctx context.Context, operatorAddress string, enforced bool) error {
	if !enforced {
		return nil
	}
	if k.consensusIdentities == nil {
		return fmt.Errorf("audit capability is not available on this chain")
	}
	node, err := k.GetCortexNodeState(ctx, operatorAddress)
	if err != nil {
		return err
	}
	operator, err := k.addressCodec.StringToBytes(node.OperatorAddress)
	if err != nil {
		return fmt.Errorf("operator address is invalid: %w", err)
	}
	var service sdk.AccAddress
	if node.CurrentServiceAddress != "" {
		raw, err := k.addressCodec.StringToBytes(node.CurrentServiceAddress)
		if err != nil {
			return fmt.Errorf("service address is invalid: %w", err)
		}
		service = raw
	}
	overlaps, err := k.consensusIdentities.IsConsensusIdentity(ctx, operator, service, node.CurrentServicePubkey)
	if err != nil {
		return err
	}
	if overlaps {
		return fmt.Errorf("audit capability cannot be declared by a block-producer identity")
	}
	return nil
}
