package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// VrfPoPVerifier supplies the ECVRF possession check. Hub declares the
// interface and the app supplies the curve implementation, exactly as it does
// for BeaconProofVerifier: the keeper must not carry a second copy of the
// ECVRF suite.
type VrfPoPVerifier interface {
	VerifyVrfPossession(pubkey, alpha, proof []byte) error
}

// WithVrfPoPVerifier returns the module-owned Keeper copy that can check
// possession proofs.
func (k Keeper) WithVrfPoPVerifier(verifier VrfPoPVerifier) Keeper {
	k.vrfPoPVerifier = verifier
	return k
}

// RegisterVrfKey registers or rotates the validator's
// independent VRF key; the new key never takes effect in the current epoch.
//
// The deferral is the point: the beacon for the current epoch is already being
// produced against the active key, so letting a rotation land mid-epoch would
// let a validator swap the key its own pending proofs are verified under.
func (m msgServer) RegisterVrfKey(ctx context.Context, msg *types.MsgRegisterVrfKey) (*types.MsgRegisterVrfKeyResponse, error) {
	if msg == nil {
		return nil, status.Error(codes.InvalidArgument, "request must not be nil")
	}
	if !types.IsVrfParticipantType(msg.ParticipantType) {
		return nil, status.Error(codes.InvalidArgument, "participant_type must be VALIDATOR or BUILDER")
	}
	operatorBytes, operator, err := m.k.requireCanonicalAddress("operator_address", msg.OperatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if len(msg.VrfPubkey) != types.VrfPubkeyLen {
		return nil, status.Errorf(codes.InvalidArgument, "vrf_pubkey must be exactly %d bytes", types.VrfPubkeyLen)
	}
	if msg.VrfAuthorizationNonce == 0 {
		return nil, status.Error(codes.InvalidArgument, "vrf_authorization_nonce must be greater than zero")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	// Step 1: only a registered validator has a beacon duty to hold a VRF key for.
	if msg.ParticipantType == shared.ParticipantType_PARTICIPANT_TYPE_VALIDATOR {
		if m.k.validatorSnapshots == nil {
			return nil, status.Error(codes.FailedPrecondition, "validator snapshot provider is not installed")
		}
		if err := m.k.requireKnownValidator(sdkCtx, operator, operatorBytes); err != nil {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
	} else if _, err := m.k.GetBuilderState(ctx, operator); err != nil {
		return nil, status.Error(codes.FailedPrecondition, "Builder identity is not registered")
	}

	current, found, err := m.k.getVrfKeyState(ctx, msg.ParticipantType, operator)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	// Step 4: the nonce must advance by exactly one. Equal nonce is either an
	// exact replay or a conflict; nothing else may reuse it.
	expected := uint64(1)
	if found {
		if current.VrfAuthorizationNonce == math.MaxUint64 && msg.VrfAuthorizationNonce != current.VrfAuthorizationNonce {
			return nil, status.Error(codes.FailedPrecondition, "VRF authorization nonce overflow")
		}
		expected = current.VrfAuthorizationNonce + 1
		if msg.VrfAuthorizationNonce == current.VrfAuthorizationNonce {
			if bytes.Equal(current.GetPendingVrfPubkey(), msg.VrfPubkey) ||
				bytes.Equal(current.ActiveVrfPubkey, msg.VrfPubkey) {
				return vrfKeyResponse(operator, current, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP), nil
			}
			return nil, status.Error(codes.FailedPrecondition,
				"vrf_authorization_nonce was already used by a different vrf_pubkey")
		}
	}
	if msg.VrfAuthorizationNonce != expected {
		return nil, status.Errorf(codes.FailedPrecondition,
			"vrf_authorization_nonce must be %d, got %d", expected, msg.VrfAuthorizationNonce)
	}
	// Step 6: one pending rotation at a time. Anything else would make "which key
	// activates next epoch" ambiguous.
	if found && current.XPendingVrfPubkey != nil {
		return nil, status.Error(codes.FailedPrecondition, "a pending vrf key rotation is already scheduled")
	}

	// Steps 2-3: the key must be a usable point and the submitter must prove
	// possession of it over the domain-separated digest.
	digest, err := types.VrfKeyPoPDigest(sdkCtx.ChainID(), msg.ParticipantType, operator, msg.VrfPubkey, msg.VrfAuthorizationNonce)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if m.k.vrfPoPVerifier == nil {
		return nil, status.Error(codes.FailedPrecondition, "vrf possession verifier is not installed")
	}
	if err := m.k.vrfPoPVerifier.VerifyVrfPossession(msg.VrfPubkey, digest[:], msg.VrfKeyPop); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "vrf possession proof: %s", err.Error())
	}

	// Step 5: the activation epoch is derived, never supplied.
	epoch, err := m.k.currentBridgeEpoch(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	pendingFrom, err := checkedAdd(epoch, 1)
	if err != nil {
		return nil, err
	}
	height := uint64(sdkCtx.BlockHeight())

	next := current
	if !found {
		next = types.VrfKeyState{ParticipantType: msg.ParticipantType, OperatorAddress: operator}
	}
	next.VrfAuthorizationNonce = msg.VrfAuthorizationNonce
	next.RegisteredHeight = height
	next.XPendingVrfPubkey = &types.VrfKeyState_PendingVrfPubkey{PendingVrfPubkey: append([]byte(nil), msg.VrfPubkey...)}
	next.XPendingFromEpoch = &types.VrfKeyState_PendingFromEpoch{PendingFromEpoch: pendingFrom}
	if err := types.ValidateVrfKeyState(next); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	cacheCtx, commit := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	if err := m.k.StoreVrfKey(cache, next); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := m.k.VrfKeyActivationIndex.Set(cache, types.NewVrfKeyActivationKey(pendingFrom, next.ParticipantType, operator)); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if next.ParticipantType == shared.ParticipantType_PARTICIPANT_TYPE_BUILDER {
		mustEmitHubEvent(cache, &types.EventBuilderVrfKeyRegistered{Builder: operator, PendingFromEpoch: pendingFrom, VrfAuthorizationNonce: next.VrfAuthorizationNonce})
	}
	commit()
	return vrfKeyResponse(operator, next, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED), nil
}

func vrfKeyResponse(operator string, state types.VrfKeyState, mutation shared.MutationStatusV1) *types.MsgRegisterVrfKeyResponse {
	return &types.MsgRegisterVrfKeyResponse{
		ParticipantType: state.ParticipantType,
		OperatorAddress: operator, PendingFromEpoch: state.GetPendingFromEpoch(),
		VrfAuthorizationNonce: state.VrfAuthorizationNonce, Status: mutation,
	}
}

func (k Keeper) getVrfKeyState(ctx context.Context, role shared.ParticipantType, operator string) (types.VrfKeyState, bool, error) {
	state, err := k.GetVrfKey(ctx, role, operator)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.VrfKeyState{}, false, nil
		}
		return types.VrfKeyState{}, false, err
	}
	return state, true, nil
}

// requireKnownValidator checks the operator against the same validator snapshot
// the beacon path uses, so "is a validator" has one definition in the module.
func (k Keeper) requireKnownValidator(ctx sdk.Context, operator string, operatorBytes []byte) error {
	snapshot, err := k.validatorSnapshots.CaptureValidatorSetSnapshot(ctx, uint64(ctx.BlockHeight()))
	if err != nil {
		return fmt.Errorf("validator snapshot is unavailable: %w", err)
	}
	// The signer is the stable operator, so the snapshot is asked
	// for exactly that identity rather than for a separate consensus address.
	if _, found, err := k.validatorSnapshots.GetValidatorSnapshotMemberBySigner(ctx, snapshot, operator); err != nil {
		return fmt.Errorf("validator lookup failed: %w", err)
	} else if !found {
		return fmt.Errorf("operator %s has no validator record", operator)
	}
	_ = operatorBytes
	return nil
}

// ActivateDueVrfKeys performs VRF key activation at the epoch boundary: in ascending
// operator order the old active key is retired to history and the pending key is
// promoted. A failure aborts the block rather than leaving half the set rotated,
// because a partially rotated validator set would verify next epoch's beacons
// under two different key generations.
func (k Keeper) ActivateDueVrfKeys(ctx context.Context, epoch uint64) error {
	iter, err := k.VrfKeyActivationIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	due := make([]types.VrfKeyActivationKeyTriple, 0)
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			iter.Close()
			return err
		}
		if key.K1() > epoch {
			break
		}
		due = append(due, key)
	}
	if err := iter.Close(); err != nil {
		return err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	cacheCtx, commit := sdk.UnwrapSDKContext(ctx).CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	for _, key := range due {
		role, operator := shared.ParticipantType(key.K2()), key.K3()
		state, found, err := k.getVrfKeyState(cache, role, operator)
		if err != nil {
			return err
		}
		if !found || state.XPendingVrfPubkey == nil || state.GetPendingFromEpoch() != key.K1() {
			if err := k.VrfKeyActivationIndex.Remove(cache, key); err != nil {
				return err
			}
			continue
		}
		if len(state.ActiveVrfPubkey) == types.VrfPubkeyLen {
			pruneEpoch, err := checkedAdd(epoch, uint64(params.Beacon.MaxVrfKeyHistoryEpochs))
			if err != nil {
				return err
			}
			history := types.VrfKeyHistoryState{ParticipantType: role, OperatorAddress: operator, EffectiveFromEpoch: state.ActiveFromEpoch, VrfPubkey: append([]byte(nil), state.ActiveVrfPubkey...), RetiredAtEpoch: epoch}
			if err := k.StoreVrfKeyHistory(cache, history); err != nil {
				return err
			}
			if err := k.VrfKeyPruneIndex.Set(cache, types.NewVrfKeyPruneKey(pruneEpoch, role, operator, state.ActiveFromEpoch)); err != nil {
				return err
			}
		}
		state.ActiveVrfPubkey = append([]byte(nil), state.GetPendingVrfPubkey()...)
		state.ActiveFromEpoch = epoch
		state.XPendingVrfPubkey = nil
		state.XPendingFromEpoch = nil
		if err := types.ValidateVrfKeyState(state); err != nil {
			return err
		}
		if err := k.StoreVrfKey(cache, state); err != nil {
			return err
		}
		if err := k.VrfKeyActivationIndex.Remove(cache, key); err != nil {
			return err
		}
	}
	commit()
	return nil
}
