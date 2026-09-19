package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

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

// RegisterVrfKey implements §9.3a. It registers or rotates the validator's
// independent VRF key; the new key never takes effect in the current epoch.
//
// The deferral is the point: the beacon for the current epoch is already being
// produced against the active key, so letting a rotation land mid-epoch would
// let a validator swap the key its own pending proofs are verified under.
func (m msgServer) RegisterVrfKey(ctx context.Context, msg *types.MsgRegisterVrfKey) (*types.MsgRegisterVrfKeyResponse, error) {
	if msg == nil {
		return nil, status.Error(codes.InvalidArgument, "request must not be nil")
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
	if m.k.validatorSnapshots == nil {
		return nil, status.Error(codes.FailedPrecondition, "validator snapshot provider is not installed")
	}
	if err := m.k.requireKnownValidator(sdkCtx, operator, operatorBytes); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	current, found, err := m.k.getVrfKeyState(ctx, operator)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	// Step 4: the nonce must advance by exactly one. Equal nonce is either an
	// exact replay or a conflict; nothing else may reuse it.
	expected := uint64(1)
	if found {
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
	digest, err := types.VrfKeyPoPDigest(sdkCtx.ChainID(), operator, msg.VrfPubkey, msg.VrfAuthorizationNonce)
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
	pendingFrom := epoch + 1
	height := uint64(sdkCtx.BlockHeight())

	next := current
	if !found {
		next = types.VrfKeyState{OperatorAddress: operator}
	}
	next.VrfAuthorizationNonce = msg.VrfAuthorizationNonce
	next.RegisteredHeight = height
	next.XPendingVrfPubkey = &types.VrfKeyState_PendingVrfPubkey{PendingVrfPubkey: append([]byte(nil), msg.VrfPubkey...)}
	next.XPendingFromEpoch = &types.VrfKeyState_PendingFromEpoch{PendingFromEpoch: pendingFrom}
	if err := types.ValidateVrfKeyState(next); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := m.k.VrfKey.Set(ctx, operator, next); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := m.k.VrfKeyActivationIndex.Set(ctx, types.NewVrfKeyActivationKey(pendingFrom, operator)); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return vrfKeyResponse(operator, next, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED), nil
}

func vrfKeyResponse(operator string, state types.VrfKeyState, mutation shared.MutationStatusV1) *types.MsgRegisterVrfKeyResponse {
	return &types.MsgRegisterVrfKeyResponse{
		OperatorAddress: operator, PendingFromEpoch: state.GetPendingFromEpoch(),
		VrfAuthorizationNonce: state.VrfAuthorizationNonce, Status: mutation,
	}
}

func (k Keeper) getVrfKeyState(ctx context.Context, operator string) (types.VrfKeyState, bool, error) {
	state, err := k.VrfKey.Get(ctx, operator)
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
	// §9.3a step 1: the signer is the stable operator, so the snapshot is asked
	// for exactly that identity rather than for a separate consensus address.
	if _, found, err := k.validatorSnapshots.GetValidatorSnapshotMemberBySigner(ctx, snapshot, operator); err != nil {
		return fmt.Errorf("validator lookup failed: %w", err)
	} else if !found {
		return fmt.Errorf("operator %s has no validator record", operator)
	}
	_ = operatorBytes
	return nil
}

// ActivateDueVrfKeys performs §9.3a step 6 at the epoch boundary: in ascending
// operator order the old active key is retired to history and the pending key is
// promoted. A failure aborts the block rather than leaving half the set rotated,
// because a partially rotated validator set would verify next epoch's beacons
// under two different key generations.
func (k Keeper) ActivateDueVrfKeys(ctx context.Context, epoch uint64) error {
	iter, err := k.VrfKeyActivationIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	// The whole key is carried, not just the operator. Every retirement below has
	// to remove the exact row that was iterated: a row whose activation epoch is
	// older than the current one — a rotation that came due while the chain was
	// down, say — would otherwise be deleted under (epoch, operator), miss, and be
	// re-visited on every single block for the life of the chain.
	due := make([]types.VrfKeyActivationKeyPair, 0)
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			iter.Close()
			return err
		}
		if key.K1() > epoch {
			// The index ascends by activation epoch, so the first future row ends it.
			break
		}
		due = append(due, key)
	}
	if err := iter.Close(); err != nil {
		return err
	}

	var historyRetention uint64
	for _, key := range due {
		operator := key.K2()
		state, found, err := k.getVrfKeyState(ctx, operator)
		if err != nil {
			return err
		}
		if !found || state.XPendingVrfPubkey == nil {
			// A stale index row still has to be retired or the sweep revisits it
			// every block.
			if err := k.VrfKeyActivationIndex.Remove(ctx, key); err != nil {
				return err
			}
			continue
		}
		pendingFrom := state.GetPendingFromEpoch()
		if pendingFrom > epoch {
			// The row claims this rotation is due but the state disagrees. Retiring
			// the row is the only way out: leaving it makes this block's work repeat
			// forever, and honouring it would activate a key before its epoch.
			if err := k.VrfKeyActivationIndex.Remove(ctx, key); err != nil {
				return err
			}
			continue
		}
		if len(state.ActiveVrfPubkey) == types.VrfPubkeyLen {
			if historyRetention == 0 {
				params, err := k.Params.Get(ctx)
				if err != nil {
					return err
				}
				historyRetention = uint64(params.Beacon.MaxVrfKeyHistoryEpochs)
			}
			pruneEpoch, err := checkedAdd(pendingFrom, historyRetention)
			if err != nil {
				return fmt.Errorf("schedule VRF key history prune for %s: %w", operator, err)
			}
			history := types.VrfKeyHistoryState{
				OperatorAddress: operator, EffectiveFromEpoch: state.ActiveFromEpoch,
				VrfPubkey: append([]byte(nil), state.ActiveVrfPubkey...), RetiredAtEpoch: pendingFrom,
			}
			if err := k.VrfKeyHistory.Set(ctx, types.NewVrfKeyHistoryKey(operator, state.ActiveFromEpoch), history); err != nil {
				return err
			}
			if err := k.VrfKeyPruneIndex.Set(ctx, types.NewVrfKeyPruneKey(pruneEpoch, operator, state.ActiveFromEpoch)); err != nil {
				return err
			}
		}
		state.ActiveVrfPubkey = append([]byte(nil), state.GetPendingVrfPubkey()...)
		state.ActiveFromEpoch = pendingFrom
		state.XPendingVrfPubkey = nil
		state.XPendingFromEpoch = nil
		if err := types.ValidateVrfKeyState(state); err != nil {
			return err
		}
		if err := k.VrfKey.Set(ctx, operator, state); err != nil {
			return err
		}
		if err := k.VrfKeyActivationIndex.Remove(ctx, key); err != nil {
			return err
		}
	}
	return nil
}
