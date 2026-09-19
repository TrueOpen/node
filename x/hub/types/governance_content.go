package types

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
)

const GovernanceProposalRoute = "trueopen"

func governanceContentText(kind string) string { return "TrueOpen " + kind }

func validateGovernanceProposalID(id uint64) error {
	if id == 0 {
		return fmt.Errorf("proposal_id must be non-zero")
	}
	return nil
}

func (*ExecuteTreasurySpendV1) ProposalRoute() string    { return GovernanceProposalRoute }
func (*ExecuteTreasurySpendV1) ProposalType() string     { return "ExecuteTreasurySpendV1" }
func (m *ExecuteTreasurySpendV1) GetTitle() string       { return governanceContentText(m.ProposalType()) }
func (m *ExecuteTreasurySpendV1) GetDescription() string { return m.GetTitle() }
func (m *ExecuteTreasurySpendV1) ValidateBasic() error {
	if err := validateGovernanceProposalID(m.ProposalId); err != nil {
		return err
	}
	if _, err := sdk.AccAddressFromBech32(m.Recipient); err != nil {
		return fmt.Errorf("recipient: %w", err)
	}
	amount, err := shared.ParseAmount(m.Amount)
	if err != nil || amount == 0 {
		return fmt.Errorf("amount must be a positive canonical Amount")
	}
	if m.PurposeCode == TreasuryPurposeCode_TREASURY_PURPOSE_CODE_UNSPECIFIED || m.NotBeforeHeight > m.ExpiryHeight || m.ExpiryHeight == 0 {
		return fmt.Errorf("treasury purpose or execution window is invalid")
	}
	return nil
}

func (*MintBondV1) ProposalRoute() string    { return GovernanceProposalRoute }
func (*MintBondV1) ProposalType() string     { return "MintBondV1" }
func (m *MintBondV1) GetTitle() string       { return governanceContentText(m.ProposalType()) }
func (m *MintBondV1) GetDescription() string { return m.GetTitle() }
func (m *MintBondV1) ValidateBasic() error {
	if err := validateGovernanceProposalID(m.ProposalId); err != nil {
		return err
	}
	_, err := MintBondActionDigest("trueopen-governance-preflight", *m)
	return err
}

func (*BurnBondV1) ProposalRoute() string    { return GovernanceProposalRoute }
func (*BurnBondV1) ProposalType() string     { return "BurnBondV1" }
func (m *BurnBondV1) GetTitle() string       { return governanceContentText(m.ProposalType()) }
func (m *BurnBondV1) GetDescription() string { return m.GetTitle() }
func (m *BurnBondV1) ValidateBasic() error {
	if err := validateGovernanceProposalID(m.ProposalId); err != nil {
		return err
	}
	_, err := BurnBondActionDigest("trueopen-governance-preflight", *m)
	return err
}

func (*ReplaceBuilderSetV1) ProposalRoute() string    { return GovernanceProposalRoute }
func (*ReplaceBuilderSetV1) ProposalType() string     { return "ReplaceBuilderSetV1" }
func (m *ReplaceBuilderSetV1) GetTitle() string       { return governanceContentText(m.ProposalType()) }
func (m *ReplaceBuilderSetV1) GetDescription() string { return m.GetTitle() }
func (m *ReplaceBuilderSetV1) ValidateBasic() error {
	if err := validateGovernanceProposalID(m.ProposalId); err != nil {
		return err
	}
	_, err := ReplaceBuilderSetActionDigest("trueopen-governance-preflight", *m)
	return err
}

func (*RotateBridgeSignerV1) ProposalRoute() string    { return GovernanceProposalRoute }
func (*RotateBridgeSignerV1) ProposalType() string     { return "RotateBridgeSignerV1" }
func (m *RotateBridgeSignerV1) GetTitle() string       { return governanceContentText(m.ProposalType()) }
func (m *RotateBridgeSignerV1) GetDescription() string { return m.GetTitle() }
func (m *RotateBridgeSignerV1) ValidateBasic() error {
	if err := validateGovernanceProposalID(m.ProposalId); err != nil {
		return err
	}
	_, err := RotateBridgeSignerActionDigest("trueopen-governance-preflight", *m)
	return err
}

func (*BeginBridgeCutoverV1) ProposalRoute() string    { return GovernanceProposalRoute }
func (*BeginBridgeCutoverV1) ProposalType() string     { return "BeginBridgeCutoverV1" }
func (m *BeginBridgeCutoverV1) GetTitle() string       { return governanceContentText(m.ProposalType()) }
func (m *BeginBridgeCutoverV1) GetDescription() string { return m.GetTitle() }
func (m *BeginBridgeCutoverV1) ValidateBasic() error {
	if err := validateGovernanceProposalID(m.ProposalId); err != nil {
		return err
	}
	_, err := BeginBridgeCutoverActionDigest("trueopen-governance-preflight", *m)
	return err
}

func (*ConfirmBridgeCutoverV1) ProposalRoute() string    { return GovernanceProposalRoute }
func (*ConfirmBridgeCutoverV1) ProposalType() string     { return "ConfirmBridgeCutoverV1" }
func (m *ConfirmBridgeCutoverV1) GetTitle() string       { return governanceContentText(m.ProposalType()) }
func (m *ConfirmBridgeCutoverV1) GetDescription() string { return m.GetTitle() }
func (m *ConfirmBridgeCutoverV1) ValidateBasic() error {
	if err := validateGovernanceProposalID(m.ProposalId); err != nil {
		return err
	}
	_, err := ConfirmBridgeCutoverActionDigest("trueopen-governance-preflight", *m)
	return err
}

func (*SetBridgeFreezeV1) ProposalRoute() string    { return GovernanceProposalRoute }
func (*SetBridgeFreezeV1) ProposalType() string     { return "SetBridgeFreezeV1" }
func (m *SetBridgeFreezeV1) GetTitle() string       { return governanceContentText(m.ProposalType()) }
func (m *SetBridgeFreezeV1) GetDescription() string { return m.GetTitle() }
func (m *SetBridgeFreezeV1) ValidateBasic() error {
	if err := validateGovernanceProposalID(m.ProposalId); err != nil {
		return err
	}
	_, err := SetBridgeFreezeActionDigest("trueopen-governance-preflight", *m)
	return err
}

func (*SetBridgeLimitV1) ProposalRoute() string    { return GovernanceProposalRoute }
func (*SetBridgeLimitV1) ProposalType() string     { return "SetBridgeLimitV1" }
func (m *SetBridgeLimitV1) GetTitle() string       { return governanceContentText(m.ProposalType()) }
func (m *SetBridgeLimitV1) GetDescription() string { return m.GetTitle() }
func (m *SetBridgeLimitV1) ValidateBasic() error {
	if err := validateGovernanceProposalID(m.ProposalId); err != nil {
		return err
	}
	_, err := SetBridgeLimitActionDigest("trueopen-governance-preflight", *m)
	return err
}
