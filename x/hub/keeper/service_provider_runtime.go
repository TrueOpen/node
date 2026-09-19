package keeper

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

const initialServiceKeyNonce = uint64(1)

// Hard constraint 8: domains must go through the shared registry so an unregistered or
// drifted key panics at init instead of silently producing a second preimage.
// A bare string literal here bypasses that check.
var serviceRegistrationDomain = shared.MustDomain(shared.DomainServiceRegistrationV1)

func serviceRegistrationDigest(
	chainID string,
	participantType shared.ParticipantType,
	operatorBytes, servicePubkey []byte,
	authorizationNonce uint64,
) ([]byte, error) {
	return shared.NewCanonicalHashBuilderV1(serviceRegistrationDomain).Raw(
		[]byte(chainID), shared.EnumBE(uint32(participantType)), operatorBytes,
		servicePubkey, shared.Uint64BE(authorizationNonce),
	).Sum()
}

type StakeServiceResult struct {
	Node types.CortexNodeState
	Bond types.ServiceBondState
}

func (k Keeper) prepareRegisterService(
	ctx context.Context,
	chainID, operatorAddress string,
	servicePubkey, serviceKeyProof []byte,
	amount, height, currentEpoch uint64,
) (StakeServiceResult, error) {
	operatorBytes, operatorAddress, err := k.requireCanonicalAddress("operator_address", operatorAddress)
	if err != nil {
		return StakeServiceResult{}, err
	}
	if chainID == "" || amount == 0 {
		return StakeServiceResult{}, fmt.Errorf("chain_id and positive amount are required")
	}
	pub, err := parseServicePubKey("service_pubkey", servicePubkey)
	if err != nil {
		return StakeServiceResult{}, err
	}
	if _, exists, err := k.loadCortexNode(ctx, operatorAddress); err != nil {
		return StakeServiceResult{}, err
	} else if exists {
		return StakeServiceResult{}, fmt.Errorf("cortex node already exists; use top_up")
	}
	existingBond, returning, err := k.loadServiceBond(ctx, operatorAddress)
	if err != nil {
		return StakeServiceResult{}, err
	}
	if returning {
		if existingBond.Status == types.ServiceBondStatus_SERVICE_BOND_STATUS_TOMBSTONED {
			return StakeServiceResult{}, fmt.Errorf("service bond is tombstoned")
		}
		if existingBond.Status != types.ServiceBondStatusExited || existingBond.ActiveBond != 0 ||
			existingBond.PendingUnbondingTotal != 0 || existingBond.ReservedLiability != 0 {
			return StakeServiceResult{}, fmt.Errorf("service bond already exists without a cortex identity")
		}
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return StakeServiceResult{}, err
	}
	minInitial, err := shared.ParseAmount(params.Service.ServiceBondMinInitial)
	if err != nil {
		return StakeServiceResult{}, fmt.Errorf("invalid service_bond_min_initial: %w", err)
	}
	if amount < minInitial {
		return StakeServiceResult{}, fmt.Errorf("initial service bond must be at least %d", minInitial)
	}
	// Ruling 17/24: address codec bytes, not Bech32 text (keeper_api_contract.md
	// §1.2). Cortex
	// registration and Builder registration (msg_server_builder.go) share this one
	// domain, so both must frame the operator identically or §1.4 rule 1 is broken.
	//
	// participant_type is bound right after chain_id, matching the field order of
	// the two sibling domains TRUEOPEN_SERVICE_KEY_ROTATION_V1 and
	// TRUEOPEN_UNBONDING_ID_V1. Without it Cortex and Builder registration build a
	// byte-identical preimage, which is the §1.4 rule 1 breach of one domain with
	// two semantics: the same service-key proof satisfies either role, and §1.3
	// rule 7's role-replay negative test cannot even be written. This is not a
	// funds path - requireServiceAddressAvailable already scans both participant
	// types before binding a service address, and both Msgs are authorized by the
	// operator's own account signature - it is a wire-identity fix.
	digest, err := serviceRegistrationDigest(
		chainID, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX,
		operatorBytes, servicePubkey, initialServiceKeyNonce,
	)
	if err != nil {
		return StakeServiceResult{}, err
	}
	if err := verifyServiceKeyProof(pub, digest, serviceKeyProof); err != nil {
		return StakeServiceResult{}, err
	}
	serviceAddress, err := k.addressCodec.BytesToString(types.ServicePubKeyAddress(pub))
	if err != nil {
		return StakeServiceResult{}, err
	}
	if err := k.requireServiceAddressAvailable(ctx, serviceAddress); err != nil {
		return StakeServiceResult{}, err
	}
	effectiveEpoch, err := checkedAdd(currentEpoch, types.ServiceBondEffectiveEpochDelay)
	if err != nil {
		return StakeServiceResult{}, fmt.Errorf("effective bond epoch overflow: %w", err)
	}
	node := types.CortexNodeState{
		SchemaVersion:             serviceIdentitySchemaVersion,
		OperatorAddress:           operatorAddress,
		CurrentServiceAddress:     serviceAddress,
		CurrentServicePubkey:      append([]byte(nil), servicePubkey...),
		ServiceAuthorizationNonce: initialServiceKeyNonce,
		ServiceKeyStatus:          types.ServiceKeyStatus_SERVICE_KEY_STATUS_ACTIVE,
		RegisteredHeight:          height,
		UpdatedHeight:             height,
	}
	bond := existingBond
	if !returning {
		bond = types.ServiceBondState{OperatorAddress: operatorAddress}
	}
	if bond.BondVersion == math.MaxUint64 {
		return StakeServiceResult{}, fmt.Errorf("service bond version overflow")
	}
	bond.ActiveBond = amount
	bond.EffectiveActiveBond = 0
	bond.BondVersion++
	bond.EffectiveBondEpoch = effectiveEpoch
	// Re-registration reuses the existing row, so jail_count and its recovery
	// counter survive a full exit. Writing REGISTERED unconditionally would launder
	// a jailed operator back into candidate selection while still carrying the
	// count that feeds the tombstone threshold. ADR-0019 lets only §10.0c's
	// jail-clear leave JAILED, and it does so by decrementing to zero first.
	bond.Status = types.ServiceBondStatus_SERVICE_BOND_STATUS_REGISTERED
	if bond.JailCount != 0 {
		bond.Status = types.ServiceBondStatusJailed
	}
	bond.LastStakeHeight = height
	if err := validateCortexNodeState(node); err != nil {
		return StakeServiceResult{}, err
	}
	if err := bond.Validate(); err != nil {
		return StakeServiceResult{}, err
	}
	// §5.11 fixes the event order as "material accepted -> primary state
	// transition -> budget/earning transfer": code 112 service_registered must
	// precede code 6 service_stake_changed, which the msg server emits in the same
	// cached event stream, so a failed bank transfer or persist discards both
	// lifecycle events with the writes.
	emitServiceRegisteredEvent(ctx, node, bond)
	return StakeServiceResult{Node: node, Bond: bond}, nil
}

func emitServiceRegisteredEvent(ctx context.Context, node types.CortexNodeState, bond types.ServiceBondState) {
	mustEmitHubEvent(ctx, &types.EventServiceRegistered{
		Operator:           node.OperatorAddress,
		ServiceAddress:     node.CurrentServiceAddress,
		AuthorizationNonce: node.ServiceAuthorizationNonce,
		ActiveBond:         shared.NewAmount(bond.ActiveBond),
		BondVersion:        bond.BondVersion,
		EffectiveEpoch:     bond.EffectiveBondEpoch,
	})
}

func (k Keeper) prepareTopUpService(ctx context.Context, operatorAddress string, amount, height, currentEpoch uint64) (StakeServiceResult, error) {
	_, operatorAddress, err := k.requireCanonicalAddress("operator_address", operatorAddress)
	if err != nil {
		return StakeServiceResult{}, err
	}
	if amount == 0 {
		return StakeServiceResult{}, fmt.Errorf("amount must be > 0")
	}
	node, exists, err := k.loadCortexNode(ctx, operatorAddress)
	if err != nil {
		return StakeServiceResult{}, err
	}
	if !exists {
		return StakeServiceResult{}, fmt.Errorf("cortex node does not exist; use register")
	}
	if err := validateCortexNodeState(node); err != nil {
		return StakeServiceResult{}, err
	}
	bond, exists, err := k.loadServiceBond(ctx, operatorAddress)
	if err != nil {
		return StakeServiceResult{}, err
	}
	if !exists {
		return StakeServiceResult{}, fmt.Errorf("service bond does not exist; use register")
	}
	if bond.Status == types.ServiceBondStatus_SERVICE_BOND_STATUS_TOMBSTONED {
		return StakeServiceResult{}, fmt.Errorf("service bond is tombstoned")
	}
	effectiveBond := types.EffectiveActiveBond(bond, currentEpoch)
	bond.ActiveBond, err = checkedAdd(bond.ActiveBond, amount)
	if err != nil {
		return StakeServiceResult{}, fmt.Errorf("service bond amount overflow: %w", err)
	}
	if bond.BondVersion == math.MaxUint64 {
		return StakeServiceResult{}, fmt.Errorf("service bond version overflow")
	}
	bond.BondVersion++
	bond.EffectiveBondEpoch, err = checkedAdd(currentEpoch, types.ServiceBondEffectiveEpochDelay)
	if err != nil {
		return StakeServiceResult{}, fmt.Errorf("effective bond epoch overflow: %w", err)
	}
	bond.EffectiveActiveBond = effectiveBond
	bond.LastStakeHeight = height
	// EXITED no longer owns a Cortex identity (A-20), so it cannot be revived by a
	// top-up that carries no new service-key proof. Re-entry goes through register
	// and its initial-bond floor; UNBONDING still owns its identity and may cancel a
	// full exit by topping up to the same floor.
	switch bond.Status {
	case types.ServiceBondStatus_SERVICE_BOND_STATUS_UNBONDING:
		params, err := k.Params.Get(ctx)
		if err != nil {
			return StakeServiceResult{}, err
		}
		minInitial, err := shared.ParseAmount(params.Service.ServiceBondMinInitial)
		if err != nil {
			return StakeServiceResult{}, fmt.Errorf("invalid service_bond_min_initial: %w", err)
		}
		if bond.ActiveBond < minInitial {
			return StakeServiceResult{}, fmt.Errorf("returning service bond must be at least %d", minInitial)
		}
		bond.Status = types.ServiceBondStatus_SERVICE_BOND_STATUS_REGISTERED
	}
	node.UpdatedHeight = height
	if err := bond.Validate(); err != nil {
		return StakeServiceResult{}, err
	}
	return StakeServiceResult{Node: node, Bond: bond}, nil
}

// StakeService is the custody-safe top-up entry point for keeper consumers.
// New identity creation is only available through MsgStakeService.register.
// The bank debit and all logical state changes share one cache transaction.
func (k Keeper) StakeService(ctx context.Context, operatorAddress string, amount, height, currentEpoch uint64) (StakeServiceResult, error) {
	operatorBytes, _, err := k.requireCanonicalAddress("operator_address", operatorAddress)
	if err != nil {
		return StakeServiceResult{}, err
	}
	coins, err := k.hubCoins(ctx, amount)
	if err != nil {
		return StakeServiceResult{}, err
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	result, err := k.prepareTopUpService(cache, operatorAddress, amount, height, currentEpoch)
	if err != nil {
		return StakeServiceResult{}, err
	}
	if err := k.bankKeeper.SendCoinsFromAccountToModule(cache, sdk.AccAddress(operatorBytes), types.ServiceBondModuleName, coins); err != nil {
		return StakeServiceResult{}, err
	}
	if err := k.persistStakeService(cache, result); err != nil {
		return StakeServiceResult{}, err
	}
	if err := k.syncCandidateSlotMembershipForEpoch(cache, result.Node.OperatorAddress, height, result.Bond.EffectiveBondEpoch); err != nil {
		return StakeServiceResult{}, err
	}
	emitServiceStakeChangedEventWithAmount(cache, result.Bond, amount)
	write()
	return result, nil
}

func (k Keeper) persistStakeService(ctx context.Context, result StakeServiceResult) error {
	if err := validateCortexNodeState(result.Node); err != nil {
		return err
	}
	if err := result.Bond.Validate(); err != nil {
		return err
	}
	if result.Node.OperatorAddress != result.Bond.OperatorAddress {
		return fmt.Errorf("cortex node and service bond operators do not match")
	}
	var previous *types.ServiceBondState
	stored, err := k.ServiceBond.Get(ctx, types.NewServiceBondKey(result.Bond.OperatorAddress))
	if err == nil {
		previous = &stored
	} else if !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	if err := k.CortexNode.Set(ctx, result.Node.OperatorAddress, result.Node); err != nil {
		return err
	}
	if err := k.ServiceBond.Set(ctx, types.NewServiceBondKey(result.Bond.OperatorAddress), result.Bond); err != nil {
		return err
	}
	if err := k.replaceServiceBondEffectiveIndex(ctx, previous, result.Bond); err != nil {
		return err
	}
	if result.Node.ServiceKeyStatus == types.ServiceKeyStatus_SERVICE_KEY_STATUS_REVOKED {
		return nil
	}
	return k.CurrentServiceAddressIndex.Set(
		ctx,
		types.NewCurrentServiceAddressIndexKey(shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, result.Node.CurrentServiceAddress),
		types.CurrentServiceAddressIndexState{
			OperatorAddress: result.Node.OperatorAddress, ServiceAuthorizationNonce: result.Node.ServiceAuthorizationNonce,
		},
	)
}

func (k Keeper) GetCortexNodeState(ctx context.Context, operatorAddress string) (types.CortexNodeState, error) {
	// Canonicalized here as well so the not-found message keeps naming the same
	// canonical address it always did; the helper's own call is idempotent.
	_, canonical, err := k.requireCanonicalAddress("operator_address", operatorAddress)
	if err != nil {
		return types.CortexNodeState{}, err
	}
	state, exists, err := k.getCortexNodeStateIfPresent(ctx, canonical)
	if err != nil {
		return types.CortexNodeState{}, err
	}
	if !exists {
		return types.CortexNodeState{}, fmt.Errorf("cortex node %s not found", canonical)
	}
	return state, nil
}

// getCortexNodeStateIfPresent is GetCortexNodeState minus its "must exist" half:
// same canonicalization, same key/value identity check, same state validation,
// but absence is reported instead of rejected. Only callers that can name a
// legitimate reason for the row to be gone may use it — terminal bond cleanup
// (removeCortexIdentityOnTerminalBond) retires the identity while other
// participant-scoped state lives on, and the genesis cross-check requires that
// pairing rather than tolerating it.
func (k Keeper) getCortexNodeStateIfPresent(ctx context.Context, operatorAddress string) (types.CortexNodeState, bool, error) {
	_, operatorAddress, err := k.requireCanonicalAddress("operator_address", operatorAddress)
	if err != nil {
		return types.CortexNodeState{}, false, err
	}
	state, exists, err := k.loadCortexNode(ctx, operatorAddress)
	if err != nil {
		return types.CortexNodeState{}, false, err
	}
	if !exists {
		return types.CortexNodeState{}, false, nil
	}
	if state.OperatorAddress != operatorAddress {
		return types.CortexNodeState{}, false, fmt.Errorf("cortex node does not match its store key")
	}
	if err := validateCortexNodeState(state); err != nil {
		return types.CortexNodeState{}, false, fmt.Errorf("invalid cortex node: %w", err)
	}
	return state, true, nil
}

func validateCortexNodeState(state types.CortexNodeState) error {
	if state.SchemaVersion != serviceIdentitySchemaVersion {
		return fmt.Errorf("cortex node schema_version must be %d", serviceIdentitySchemaVersion)
	}
	if state.OperatorAddress == "" || state.OperatorAddress != strings.TrimSpace(state.OperatorAddress) {
		return fmt.Errorf("cortex node operator_address must be canonical")
	}
	if state.RegisteredHeight == 0 || state.UpdatedHeight < state.RegisteredHeight {
		return fmt.Errorf("cortex node registration heights are invalid")
	}
	if state.ServiceAuthorizationNonce == 0 {
		return fmt.Errorf("cortex node service_authorization_nonce must be > 0")
	}
	if err := validateServiceKeyStatus(state.ServiceKeyStatus); err != nil {
		return err
	}
	pub, err := parseServicePubKey("current_service_pubkey", state.CurrentServicePubkey)
	if err != nil {
		return err
	}
	if len(state.CurrentServiceAddress) == 0 || len(types.ServicePubKeyAddress(pub)) == 0 {
		return fmt.Errorf("cortex node current service identity is incomplete")
	}
	return nil
}

func (k Keeper) loadCortexNode(ctx context.Context, operatorAddress string) (types.CortexNodeState, bool, error) {
	state, err := k.CortexNode.Get(ctx, strings.TrimSpace(operatorAddress))
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.CortexNodeState{}, false, nil
		}
		return types.CortexNodeState{}, false, err
	}
	return state, true, nil
}
