package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"
	hlcorekeeper "github.com/bcp-innovations/hyperlane-cosmos/x/core/keeper"
	cryptokey "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	govkeeper "github.com/cosmos/cosmos-sdk/x/gov/keeper"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	govv1beta1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1beta1"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"
	slashingkeeper "github.com/cosmos/cosmos-sdk/x/slashing/keeper"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/cosmos/gogoproto/proto"

	appbridge "github.com/TrueOpen/node/app/bridge"
	hubkeeper "github.com/TrueOpen/node/x/hub/keeper"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

// GovernanceBankKeeperBinding is what keeps x/gov's forfeited deposits out of
// bank.BurnCoins; see bindGuardedInterface for why the names are derived rather
// than written.
var GovernanceBankKeeperBinding = bindGuardedInterface[govtypes.BankKeeper, GovernedGovBankKeeper]()

// governanceReplayChecker keeps TreasurySpend retention tied to the
// authoritative x/gov proposal lifecycle. A proposal that is still accepting
// deposits or votes may still reach execution; every terminal status cannot.
type governanceReplayChecker struct {
	keeper *govkeeper.Keeper
}

func ProvideGovernanceActionReplayChecker(keeper *govkeeper.Keeper) hubkeeper.GovernanceActionReplayChecker {
	return governanceReplayChecker{keeper: keeper}
}

func (c governanceReplayChecker) IsGovernanceActionReExecutable(ctx context.Context, proposalID uint64, itemIndex uint32) (bool, error) {
	proposal, err := c.keeper.Proposals.Get(ctx, proposalID)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	messages, err := proposal.GetMsgs()
	if err != nil {
		return false, err
	}
	if uint64(itemIndex) >= uint64(len(messages)) {
		return false, fmt.Errorf("governance proposal %d has no item %d", proposalID, itemIndex)
	}
	content, _, err := unwrapGovernanceContent(messages[itemIndex])
	if err != nil {
		return false, err
	}
	spend, ok := content.(*hubtypes.ExecuteTreasurySpendV1)
	if !ok || spend.ProposalId != proposalID || spend.ItemIndex != itemIndex {
		return false, fmt.Errorf("governance proposal %d item %d is not its TreasurySpend action", proposalID, itemIndex)
	}
	switch proposal.Status {
	case govv1.ProposalStatus_PROPOSAL_STATUS_PASSED,
		govv1.ProposalStatus_PROPOSAL_STATUS_REJECTED,
		govv1.ProposalStatus_PROPOSAL_STATUS_FAILED:
		return false, nil
	default:
		return true, nil
	}
}

var _ hubkeeper.GovernanceActionReplayChecker = governanceReplayChecker{}

type governanceRuntime struct {
	gov      *govkeeper.Keeper
	hub      hubkeeper.Keeper
	staking  *stakingkeeper.Keeper
	slashing slashingkeeper.Keeper
	bank     bankkeeper.Keeper
	bridge   appbridge.BridgeAdmin
}

func ProvideGovernanceContentRuntime(
	gov *govkeeper.Keeper,
	hub hubkeeper.Keeper,
	staking *stakingkeeper.Keeper,
	slashing slashingkeeper.Keeper,
	bank bankkeeper.Keeper,
	core *hlcorekeeper.Keeper,
) hubkeeper.GovernanceContentRuntime {
	return &governanceRuntime{
		gov: gov, hub: hub, staking: staking, slashing: slashing, bank: bank,
		bridge: appbridge.NewBridgeAdmin(core),
	}
}

func (r *governanceRuntime) ValidateSubmittedGovernanceProposal(ctx context.Context, proposalID uint64) error {
	proposal, err := r.gov.Proposals.Get(ctx, proposalID)
	if err != nil {
		return err
	}
	messages, err := proposal.GetMsgs()
	if err != nil {
		return err
	}
	type located struct {
		index   uint32
		content govv1beta1.Content
	}
	actions := make([]located, 0)
	for index, message := range messages {
		content, trueopen, err := unwrapGovernanceContent(message)
		if err != nil {
			return fmt.Errorf("governance proposal %d item %d: %w", proposalID, index, err)
		}
		if !trueopen {
			continue
		}
		if governanceContentProposalID(content) != proposalID {
			return fmt.Errorf("TrueOpen governance content proposal_id does not match assigned proposal %d", proposalID)
		}
		if spend, ok := content.(*hubtypes.ExecuteTreasurySpendV1); ok && spend.ItemIndex != uint32(index) {
			return fmt.Errorf("TreasurySpend item_index %d does not match proposal item %d", spend.ItemIndex, index)
		}
		actions = append(actions, located{index: uint32(index), content: content})
	}
	if len(actions) == 0 {
		return nil
	}
	beginIndex := -1
	for _, action := range actions {
		if _, ok := action.content.(*hubtypes.BeginBridgeCutoverV1); ok {
			if beginIndex >= 0 {
				return fmt.Errorf("a governance proposal may contain only one BeginBridgeCutoverV1")
			}
			beginIndex = int(action.index)
		}
	}
	for _, action := range actions {
		switch action.content.(type) {
		case *hubtypes.MintBondV1, *hubtypes.BurnBondV1, *hubtypes.RotateBridgeSignerV1:
			if beginIndex < 0 || int(action.index) >= beginIndex {
				return fmt.Errorf("validator/signer changes must precede one paired BeginBridgeCutoverV1 in the same proposal")
			}
		}
	}
	return nil
}

func (r *governanceRuntime) HandleGovernanceContent(ctx sdk.Context, content govv1beta1.Content) error {
	if err := content.ValidateBasic(); err != nil {
		return err
	}
	proposalID := governanceContentProposalID(content)
	execution, found, err := r.acceptedExecutionContext(ctx, proposalID, content)
	if err != nil {
		return err
	}
	if !found {
		// x/gov preflights legacy content in a throwaway cache before assigning
		// the proposal ID. ValidateBasic is the complete read-only path.
		return nil
	}
	switch action := content.(type) {
	case *hubtypes.ExecuteTreasurySpendV1:
		_, err = r.hub.ExecuteTreasurySpendV1(ctx, execution, *action)
	case *hubtypes.ReplaceBuilderSetV1:
		_, err = r.hub.ExecuteReplaceBuilderSetV1(ctx, execution, *action)
	case *hubtypes.SetBridgeFreezeV1:
		err = r.hub.ExecuteSetBridgeFreezeV1(ctx, execution, *action)
	case *hubtypes.SetBridgeLimitV1:
		err = r.hub.ExecuteSetBridgeLimitV1(ctx, execution, *action)
	case *hubtypes.RotateBridgeSignerV1:
		err = r.hub.ExecuteRotateBridgeSignerV1(ctx, execution, *action)
	case *hubtypes.BeginBridgeCutoverV1:
		err = r.executeBeginBridgeCutover(ctx, execution, *action)
	case *hubtypes.ConfirmBridgeCutoverV1:
		err = r.hub.ExecuteConfirmBridgeCutoverV1(ctx, execution, *action)
	case *hubtypes.MintBondV1:
		err = r.executeMintBond(ctx, *action)
	case *hubtypes.BurnBondV1:
		err = r.executeBurnBond(ctx, *action)
	default:
		return fmt.Errorf("unsupported TrueOpen governance content %T", content)
	}
	return err
}

func (r *governanceRuntime) acceptedExecutionContext(
	ctx sdk.Context,
	proposalID uint64,
	content govv1beta1.Content,
) (hubkeeper.AcceptedGovernanceActionContext, bool, error) {
	proposal, err := r.gov.Proposals.Get(ctx, proposalID)
	if errors.Is(err, collections.ErrNotFound) {
		return hubkeeper.AcceptedGovernanceActionContext{}, false, nil
	}
	if err != nil {
		return hubkeeper.AcceptedGovernanceActionContext{}, false, err
	}
	if proposal.Status != govv1.ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD ||
		proposal.VotingEndTime == nil || proposal.VotingEndTime.After(ctx.BlockTime()) {
		return hubkeeper.AcceptedGovernanceActionContext{}, false,
			fmt.Errorf("proposal %d is not at its accepted execution boundary", proposalID)
	}
	queued, err := r.gov.ActiveProposalsQueue.Has(ctx, collections.Join(*proposal.VotingEndTime, proposalID))
	if err != nil {
		return hubkeeper.AcceptedGovernanceActionContext{}, false, err
	}
	if queued {
		return hubkeeper.AcceptedGovernanceActionContext{}, false,
			fmt.Errorf("proposal %d has not entered accepted execution", proposalID)
	}
	messages, err := proposal.GetMsgs()
	if err != nil {
		return hubkeeper.AcceptedGovernanceActionContext{}, false, err
	}
	match := -1
	executingMessage, ok := content.(proto.Message)
	if !ok {
		return hubkeeper.AcceptedGovernanceActionContext{}, false,
			fmt.Errorf("TrueOpen governance content %T is not protobuf", content)
	}
	for index, message := range messages {
		candidate, trueopen, err := unwrapGovernanceContent(message)
		if err != nil {
			return hubkeeper.AcceptedGovernanceActionContext{}, false, err
		}
		candidateMessage, protobuf := candidate.(proto.Message)
		if trueopen && protobuf && proto.Equal(candidateMessage, executingMessage) {
			if match >= 0 {
				return hubkeeper.AcceptedGovernanceActionContext{}, false,
					fmt.Errorf("proposal %d repeats one TrueOpen governance action", proposalID)
			}
			match = index
		}
	}
	if match < 0 {
		return hubkeeper.AcceptedGovernanceActionContext{}, false,
			fmt.Errorf("proposal %d does not contain the executing TrueOpen action", proposalID)
	}
	return hubkeeper.AcceptedGovernanceActionContext{
		AuthorityAddress: r.gov.GetAuthority(),
		ProposalID:       proposalID,
		ItemIndex:        uint32(match),
		Accepted:         true,
	}, true, nil
}

func (r *governanceRuntime) executeBeginBridgeCutover(
	ctx sdk.Context,
	execution hubkeeper.AcceptedGovernanceActionContext,
	action hubtypes.BeginBridgeCutoverV1,
) error {
	route, err := r.hub.BridgeRoute.Get(ctx)
	if err != nil {
		return err
	}
	ismID, err := r.bridge.CreateMessageIDMultisigISM(
		ctx, execution.AuthorityAddress, action.NextSignerSet, action.NextThreshold,
	)
	if err != nil {
		return err
	}
	if err := r.bridge.SetMailboxDefaultISM(
		ctx, execution.AuthorityAddress, route.LocalMailboxId, ismID,
	); err != nil {
		return err
	}
	return r.hub.ExecuteBeginBridgeCutoverV1(ctx, execution, action, ismID)
}

func (r *governanceRuntime) executeMintBond(ctx sdk.Context, action hubtypes.MintBondV1) error {
	if _, err := hubtypes.MintBondActionDigest(ctx.ChainID(), action); err != nil {
		return err
	}
	target, err := sdk.AccAddressFromBech32(action.TargetOperator)
	if err != nil || target.String() != action.TargetOperator {
		return fmt.Errorf("MintBond target_operator is not canonical")
	}
	valAddr := sdk.ValAddress(target)
	if existing, err := r.staking.GetValidator(ctx, valAddr); err == nil {
		signer, signerErr := r.hub.GetValidatorBridgeSigner(ctx, action.TargetOperator)
		pubkey, pubkeyErr := existing.ConsPubKey()
		if signerErr == nil && pubkeyErr == nil &&
			bytes.Equal(pubkey.Bytes(), action.ConsensusPubkey) &&
			bytes.Equal(signer.BridgeSignerAddressRaw20, action.BridgeSignerAddressRaw20) &&
			bytes.Equal(signer.PopSignature, action.BridgeSignerPopSignature) &&
			signer.KeyVersion == 1 {
			return nil
		}
		return fmt.Errorf("MintBond target already has a conflicting Validator registration")
	}
	if _, err := r.hub.GetValidatorBridgeSigner(ctx, action.TargetOperator); err == nil {
		return fmt.Errorf("MintBond target already has a bridge signer")
	} else if !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	if err := r.requireUnusedBridgeSigner(ctx, action.BridgeSignerAddressRaw20); err != nil {
		return err
	}
	if err := hubtypes.VerifyBridgeSignerPoP(
		ctx.ChainID(), action.TargetOperator, action.BridgeSignerAddressRaw20,
		action.BridgeSignerPopSignature, 1,
	); err != nil {
		return err
	}
	params, err := r.hub.Params.Get(ctx)
	if err != nil {
		return err
	}
	bondUnit, err := strconv.ParseUint(params.Phase0.ValidatorBondUnit, 10, 64)
	if err != nil || bondUnit == 0 || strconv.FormatUint(bondUnit, 10) != params.Phase0.ValidatorBondUnit {
		return fmt.Errorf("validator_bond_unit is invalid")
	}
	stakingDenom, err := r.staking.BondDenom(ctx)
	if err != nil {
		return err
	}
	if params.Phase0.ConsensusBondDenom != stakingDenom {
		return fmt.Errorf("consensus bond denom %q does not match x/staking denom %q", params.Phase0.ConsensusBondDenom, stakingDenom)
	}
	if balance := r.bank.GetBalance(ctx, target, params.Phase0.ConsensusBondDenom); !balance.Amount.IsZero() {
		return fmt.Errorf("MintBond target already holds transferable consensus bond")
	}
	pubkey := &cryptokey.PubKey{Key: append([]byte(nil), action.ConsensusPubkey...)}
	if _, err := r.staking.GetValidatorByConsAddr(ctx, sdk.GetConsAddress(pubkey)); err == nil {
		return fmt.Errorf("MintBond consensus_pubkey is already registered")
	}
	minCommission, err := r.staking.MinCommissionRate(ctx)
	if err != nil {
		return err
	}
	coin := sdk.NewCoin(params.Phase0.ConsensusBondDenom, sdkmath.NewIntFromUint64(bondUnit))
	create, err := stakingtypes.NewMsgCreateValidator(
		valAddr.String(),
		pubkey,
		coin,
		stakingtypes.NewDescription(action.TargetOperator, "", "", "", ""),
		stakingtypes.NewCommissionRates(minCommission, sdkmath.LegacyOneDec(), sdkmath.LegacyZeroDec()),
		sdkmath.NewIntFromUint64(bondUnit),
	)
	if err != nil {
		return err
	}
	if err := r.bank.MintCoins(ctx, minttypes.ModuleName, sdk.NewCoins(coin)); err != nil {
		return err
	}
	if err := r.bank.SendCoinsFromModuleToAccount(ctx, minttypes.ModuleName, target, sdk.NewCoins(coin)); err != nil {
		return err
	}
	if _, err := stakingkeeper.NewMsgServerImpl(r.staking).CreateValidator(ctx, create); err != nil {
		return err
	}
	height, err := positiveGovernanceHeight(ctx)
	if err != nil {
		return err
	}
	return r.hub.StoreValidatorBridgeSigner(ctx, hubtypes.ValidatorBridgeSignerState{
		OperatorAddress:          action.TargetOperator,
		BridgeSignerAddressRaw20: append([]byte(nil), action.BridgeSignerAddressRaw20...),
		KeyVersion:               1,
		PopSignature:             append([]byte(nil), action.BridgeSignerPopSignature...),
		RegisteredHeight:         height,
	})
}

func (r *governanceRuntime) executeBurnBond(ctx sdk.Context, action hubtypes.BurnBondV1) error {
	if _, err := hubtypes.BurnBondActionDigest(ctx.ChainID(), action); err != nil {
		return err
	}
	target, err := sdk.AccAddressFromBech32(action.TargetOperator)
	if err != nil || target.String() != action.TargetOperator {
		return fmt.Errorf("BurnBond target_operator is not canonical")
	}
	valAddr := sdk.ValAddress(target)
	validator, err := r.staking.GetValidator(ctx, valAddr)
	if err != nil {
		return fmt.Errorf("BurnBond target has no Validator registration: %w", err)
	}
	params, err := r.hub.Params.Get(ctx)
	if err != nil {
		return err
	}
	bondUnit, err := strconv.ParseUint(params.Phase0.ValidatorBondUnit, 10, 64)
	if err != nil || bondUnit == 0 {
		return fmt.Errorf("validator_bond_unit is invalid")
	}
	delegations, err := r.staking.GetValidatorDelegations(ctx, valAddr)
	if err != nil {
		return err
	}
	if len(delegations) != 1 || delegations[0].DelegatorAddress != action.TargetOperator {
		return fmt.Errorf("BurnBond requires exactly the target's full self-delegation")
	}
	if !validator.Tokens.Equal(sdkmath.NewIntFromUint64(bondUnit)) {
		return fmt.Errorf("BurnBond validator tokens do not equal validator_bond_unit")
	}
	consAddr, err := validator.GetConsAddr()
	if err != nil {
		return err
	}
	if r.slashing.IsTombstoned(ctx, sdk.ConsAddress(consAddr)) {
		if _, signerErr := r.hub.GetValidatorBridgeSigner(ctx, action.TargetOperator); !errors.Is(signerErr, collections.ErrNotFound) {
			return fmt.Errorf("BurnBond replay retains a bridge signer")
		}
		if _, unbondErr := r.staking.GetUnbondingDelegation(ctx, target, valAddr); unbondErr != nil {
			return fmt.Errorf("BurnBond replay has no pending full unbond: %w", unbondErr)
		}
		return nil
	}
	if err := r.slashing.Tombstone(ctx, sdk.ConsAddress(consAddr)); err != nil {
		return err
	}
	coin := sdk.NewCoin(params.Phase0.ConsensusBondDenom, sdkmath.NewIntFromUint64(bondUnit))
	if _, err := stakingkeeper.NewMsgServerImpl(r.staking).Undelegate(ctx, &stakingtypes.MsgUndelegate{
		DelegatorAddress: action.TargetOperator,
		ValidatorAddress: valAddr.String(),
		Amount:           coin,
	}); err != nil {
		return err
	}
	return r.hub.ValidatorBridgeSigner.Remove(ctx, action.TargetOperator)
}

func (r *governanceRuntime) requireUnusedBridgeSigner(ctx context.Context, signer []byte) error {
	rows, err := r.hub.ValidatorBridgeSigner.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer rows.Close()
	for ; rows.Valid(); rows.Next() {
		operator, err := rows.Key()
		if err != nil {
			return err
		}
		row, err := rows.Value()
		if err != nil {
			return err
		}
		if bytes.Equal(row.BridgeSignerAddressRaw20, signer) {
			return fmt.Errorf("bridge signer is already registered to %s", operator)
		}
	}
	return nil
}

func positiveGovernanceHeight(ctx sdk.Context) (uint64, error) {
	if ctx.BlockHeight() <= 0 {
		return 0, fmt.Errorf("governance action requires a positive block height")
	}
	return uint64(ctx.BlockHeight()), nil
}

func unwrapGovernanceContent(message sdk.Msg) (govv1beta1.Content, bool, error) {
	legacy, ok := message.(*govv1.MsgExecLegacyContent)
	if !ok {
		return nil, false, nil
	}
	content, err := govv1.LegacyContentFromMessage(legacy)
	if err != nil {
		return nil, false, err
	}
	switch content.(type) {
	case *hubtypes.ExecuteTreasurySpendV1, *hubtypes.MintBondV1, *hubtypes.BurnBondV1,
		*hubtypes.ReplaceBuilderSetV1, *hubtypes.RotateBridgeSignerV1,
		*hubtypes.BeginBridgeCutoverV1, *hubtypes.ConfirmBridgeCutoverV1,
		*hubtypes.SetBridgeFreezeV1, *hubtypes.SetBridgeLimitV1:
		return content, true, nil
	default:
		return content, false, nil
	}
}

func governanceContentProposalID(content govv1beta1.Content) uint64 {
	if action, ok := content.(interface{ GetProposalId() uint64 }); ok {
		return action.GetProposalId()
	}
	return 0
}

var _ hubkeeper.GovernanceContentRuntime = (*governanceRuntime)(nil)
