package keeper_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

type hubTestIdentity struct {
	Address   string
	PrivKey   *secp256k1.PrivKey
	PubKey    []byte
	PubKeyHex string
}

func hubAddress(t *testing.T, fill byte) string {
	t.Helper()
	address, err := hubAddressFromFill(fill)
	require.NoError(t, err)
	return address
}

func hubIdentity(t *testing.T, fill byte) hubTestIdentity {
	t.Helper()
	identity, err := hubIdentityFromFill(fill)
	require.NoError(t, err)
	return identity
}

func mustHubIdentity(fill byte) hubTestIdentity {
	identity, err := hubIdentityFromFill(fill)
	if err != nil {
		panic(err)
	}
	return identity
}

func hubAddressFromFill(fill byte) (string, error) {
	codec := addresscodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix())
	return codec.BytesToString(sdk.AccAddress(bytes.Repeat([]byte{fill}, 20)))
}

func hubIdentityFromFill(fill byte) (hubTestIdentity, error) {
	secret := sha256.Sum256([]byte{fill, 0xa5, 0x5a})
	priv := secp256k1.GenPrivKeyFromSecret(secret[:])
	pubKey := priv.PubKey()
	codec := addresscodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix())
	address, err := codec.BytesToString(types.ServicePubKeyAddress(pubKey.(*secp256k1.PubKey)))
	if err != nil {
		return hubTestIdentity{}, err
	}
	return hubTestIdentity{
		Address:   address,
		PrivKey:   priv,
		PubKey:    pubKey.Bytes(),
		PubKeyHex: hex.EncodeToString(pubKey.Bytes()),
	}, nil
}

func hubHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func hubHashBytes(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

// hubSign returns the raw 64-byte R||S signature that every proof/signature
// field and digest verifier in this module takes.
func hubSign(t *testing.T, identity hubTestIdentity, signingBytes []byte) []byte {
	t.Helper()
	sig, err := types.SignSecp256k1DigestForTest(identity.PrivKey, signingBytes)
	require.NoError(t, err)
	require.Len(t, sig, 64)
	return sig
}

// registerBuilderIdentityForTest registers a Builder through the production path.
// The standalone ServiceKeyBinding collection was deleted: the current online key
// lives inline on BuilderState, so the only thing left to seed by hand is the
// descriptor row that MsgUpdateServiceDescriptor would normally write.
func registerBuilderIdentityForTest(t *testing.T, f *fixture, identity hubTestIdentity, height uint64) types.BuilderState {
	t.Helper()
	state, err := f.keeper.RegisterBuilder(f.ctx, identity.Address, height)
	require.NoError(t, err)
	descriptor := testServiceDescriptor(
		shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, identity.Address,
		"grpc://builder.invalid/"+hubHash(identity.Address), height,
	)
	require.NoError(t, descriptor.Validate())
	require.NoError(t, f.keeper.ServiceDescriptor.Set(
		f.ctx, types.NewParticipantKey(shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, identity.Address), descriptor,
	))
	state.CurrentServiceAddress = identity.Address
	state.CurrentServicePubkey = identity.PubKey
	state.ServiceAuthorizationNonce = 1
	state.CurrentServiceKeyStatus = types.ServiceKeyStatusActive
	state.CurrentDescriptorVersion = 1
	require.NoError(t, f.keeper.Builder.Set(f.ctx, identity.Address, state))
	require.NoError(t, f.keeper.CurrentServiceAddressIndex.Set(
		f.ctx,
		types.NewCurrentServiceAddressIndexKey(shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, identity.Address),
		types.CurrentServiceAddressIndexState{OperatorAddress: identity.Address, ServiceAuthorizationNonce: 1},
	))
	return state
}

// fixtureRegistrationChainID is the chain the shared registration fixture binds
// its TRUEOPEN_SERVICE_REGISTRATION_V1 proof to. initFixture leaves the context
// chain id empty and prepareRegisterService rejects that, so the helper supplies
// one on a derived context instead of mutating f.ctx.
const fixtureRegistrationChainID = "trueopen-hub-fixture"

// registerCortexNodeIdentityForTest brings one operator to "registered cortex
// node with an active service key" through the production registration path.
//
// the API contract step 1 made MsgStakeService.register the only entry
// point that may create an identity: keeper.StakeService is a pure top-up now and
// refuses an operator that has no CortexNodeState. The helper therefore drives the
// real handler, which means every fixture built on it also exercises the bank
// debit, the proof-of-possession check, the service address derivation, the
// CurrentServiceAddressIndex write and the §5.11 event 112 -> 6 order.
//
// identity is the *service* key, not the operator key: prepareRegisterService
// derives current_service_address from its pubkey, so "the service address differs
// from the operator address" is expressed simply by passing a different identity.
//
// epoch is the epoch the caller wants the stake to have landed in. The handler
// derives that epoch from the block height, and these fixtures deliberately use
// small registration heights that they assert on, so effective_bond_epoch is
// re-pinned afterwards to exactly the value the register path would have written
// had the block height belonged to `epoch`.
func registerCortexNodeIdentityForTest(t *testing.T, f *fixture, operator string, identity hubTestIdentity, bondAmount, height, epoch uint64) types.CortexNodeState {
	t.Helper()
	registerCtx := sdk.WrapSDKContext(
		sdk.UnwrapSDKContext(f.ctx).WithChainID(fixtureRegistrationChainID).WithBlockHeight(int64(height)),
	)
	exists, err := f.keeper.CortexNode.Has(f.ctx, operator)
	require.NoError(t, err)
	action := topUpServiceAction(bondAmount)
	if !exists {
		proof := serviceRegistrationProofBytes(
			fixtureRegistrationChainID, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, operator, identity.PubKey,
		)
		action = registerServiceAction(identity.PubKey, hubSign(t, identity, proof), bondAmount)
	}
	f.bank.seedAccount(operator, bondAmount)
	_, err = keeper.NewMsgServerImpl(f.keeper).StakeService(registerCtx, &types.MsgStakeService{
		OperatorAddress: operator, Action: action,
	})
	require.NoError(t, err)

	bond, err := f.keeper.GetServiceBondState(f.ctx, operator)
	require.NoError(t, err)
	originalSchedule := types.NewServiceBondEffectiveKey(bond.EffectiveBondEpoch, operator)
	if has, err := f.keeper.ServiceBondEffectiveIndex.Has(f.ctx, originalSchedule); err == nil && has {
		require.NoError(t, f.keeper.ServiceBondEffectiveIndex.Remove(f.ctx, originalSchedule))
	} else {
		require.NoError(t, err)
	}
	bond.EffectiveBondEpoch = epoch + types.ServiceBondEffectiveEpochDelay
	require.NoError(t, f.keeper.ServiceBond.Set(f.ctx, types.NewServiceBondKey(operator), bond))
	if bond.EffectiveActiveBond != bond.ActiveBond {
		require.NoError(t, f.keeper.ServiceBondEffectiveIndex.Set(
			f.ctx, types.NewServiceBondEffectiveKey(bond.EffectiveBondEpoch, operator),
		))
	}

	node, err := f.keeper.GetCortexNodeState(f.ctx, operator)
	require.NoError(t, err)
	require.Equal(t, identity.Address, node.CurrentServiceAddress)
	return node
}

// activateServiceBondForTest brings an operator's staked bond into force at
// `epoch`.
//
// §10.0c makes a stake effective only from effective_bond_epoch onwards, which
// production reaches by letting the chain advance. An in-memory fixture cannot
// wait, so the tests that need the bond to already count (support weighting,
// candidate eligibility, task liability) pin the snapshot explicitly.
func activateServiceBondForTest(t *testing.T, f *fixture, operator string, epoch uint64) {
	t.Helper()
	bond, err := f.keeper.GetServiceBondState(f.ctx, operator)
	require.NoError(t, err)
	scheduledKey := types.NewServiceBondEffectiveKey(bond.EffectiveBondEpoch, operator)
	if has, err := f.keeper.ServiceBondEffectiveIndex.Has(f.ctx, scheduledKey); err == nil && has {
		require.NoError(t, f.keeper.ServiceBondEffectiveIndex.Remove(f.ctx, scheduledKey))
	} else {
		require.NoError(t, err)
	}
	bond.EffectiveActiveBond = bond.ActiveBond
	bond.EffectiveBondEpoch = epoch
	require.NoError(t, f.keeper.ServiceBond.Set(f.ctx, types.NewServiceBondKey(operator), bond))
}

// declareTaskLiabilitySupportForTest brings one operator to the point where
// ReserveTaskLiability succeeds: effective bond in force at the current epoch plus
// a declared capability for the profile.
func declareTaskLiabilitySupportForTest(t *testing.T, f *fixture, operatorAddress, duty, modelID string, profileVersion uint32, minStake, height uint64) uint64 {
	t.Helper()
	activateServiceBondForTest(t, f, operatorAddress, 0)
	registerTestModelProfile(t, f, modelID, profileVersion, minStake, height)
	result, err := f.keeper.DeclareModelSupport(
		f.ctx, operatorAddress, modelID, profileVersion, true, true, height,
	)
	require.NoError(t, err)
	require.Equal(t, result.Capability.CapabilityVersion, result.Support.SupportVersion)
	return result.Capability.CapabilityVersion
}
