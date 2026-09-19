package bridge

import (
	"bytes"
	"context"
	"fmt"

	"github.com/bcp-innovations/hyperlane-cosmos/util"
	ismkeeper "github.com/bcp-innovations/hyperlane-cosmos/x/core/01_interchain_security/keeper"
	ismtypes "github.com/bcp-innovations/hyperlane-cosmos/x/core/01_interchain_security/types"
	corekeeper "github.com/bcp-innovations/hyperlane-cosmos/x/core/keeper"
	coretypes "github.com/bcp-innovations/hyperlane-cosmos/x/core/types"
)

// BridgeAdmin is the narrow module-to-module capability used by the accepted
// governance action executor. It is intentionally not supplied through
// depinject and no public Msg server holds it.
type BridgeAdmin interface {
	CreateMessageIDMultisigISM(context.Context, string, [][]byte, uint32) ([]byte, error)
	SetMailboxDefaultISM(context.Context, string, []byte, []byte) error
}

type bridgeAdmin struct {
	core *corekeeper.Keeper
}

// NewBridgeAdmin must only be called by the x/gov typed action executor wiring.
func NewBridgeAdmin(core *corekeeper.Keeper) BridgeAdmin {
	return bridgeAdmin{core: core}
}

func (a bridgeAdmin) CreateMessageIDMultisigISM(
	ctx context.Context,
	owner string,
	validators [][]byte,
	threshold uint32,
) ([]byte, error) {
	if a.core == nil {
		return nil, fmt.Errorf("Hyperlane core keeper is unavailable")
	}
	encoded := make([]string, len(validators))
	for index, validator := range validators {
		if len(validator) != 20 || bytes.Equal(validator, make([]byte, 20)) {
			return nil, fmt.Errorf("bridge validator %d must be a non-zero raw20 address", index)
		}
		if index != 0 && bytes.Compare(validators[index-1], validator) >= 0 {
			return nil, fmt.Errorf("bridge validators must ascend strictly by raw address bytes")
		}
		encoded[index] = util.EncodeEthHex(validator)
	}
	response, err := ismkeeper.NewMsgServerImpl(&a.core.IsmKeeper).CreateMessageIdMultisigIsm(ctx, &ismtypes.MsgCreateMessageIdMultisigIsm{
		Creator: owner, Validators: encoded, Threshold: threshold,
	})
	if err != nil {
		return nil, err
	}
	id := response.Id.Bytes()
	if len(id) != util.HEX_ADDRESS_LENGTH || bytes.Equal(id, make([]byte, util.HEX_ADDRESS_LENGTH)) {
		return nil, fmt.Errorf("upstream returned an invalid ISM id")
	}
	return append([]byte(nil), id...), nil
}

func (a bridgeAdmin) SetMailboxDefaultISM(ctx context.Context, owner string, mailboxID, ismID []byte) error {
	if a.core == nil {
		return fmt.Errorf("Hyperlane core keeper is unavailable")
	}
	mailbox, err := hexAddressFromBytes(mailboxID)
	if err != nil {
		return err
	}
	ism, err := hexAddressFromBytes(ismID)
	if err != nil {
		return err
	}
	if _, err := corekeeper.NewMsgServerImpl(a.core).SetMailbox(ctx, &coretypes.MsgSetMailbox{
		Owner: owner, MailboxId: mailbox, DefaultIsm: &ism,
	}); err != nil {
		return err
	}
	updated, err := a.core.GetMailbox(ctx, mailbox)
	if err != nil {
		return err
	}
	if updated.Owner != owner || updated.DefaultIsm != ism {
		return fmt.Errorf("upstream mailbox did not retain the requested owner/default ISM")
	}
	return nil
}

var _ BridgeAdmin = bridgeAdmin{}
