package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/hub/types"
)

// VrfKey projects one validator's VRF key row (§9.3a). The raw private key never
// exists in any state, message or query, so the view is the two public points
// plus the epochs they are authoritative in.
func (q queryServer) VrfKey(ctx context.Context, req *types.QueryVrfKeyRequest) (*types.QueryVrfKeyResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request must not be nil")
	}
	_, operator, err := q.k.requireCanonicalAddress("operator_address", req.OperatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	state, err := q.k.VrfKey.Get(ctx, operator)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "operator %s has no registered vrf key", operator)
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := types.ValidateVrfKeyState(state); err != nil {
		// A stored row that cannot be validated is a broken invariant, not an
		// empty answer; §16.1 forbids dressing it up as a normal zero value.
		return nil, status.Error(codes.Internal, err.Error())
	}
	response := &types.QueryVrfKeyResponse{
		ActiveVrfPubkey:       append([]byte(nil), state.ActiveVrfPubkey...),
		ActiveFromEpoch:       state.ActiveFromEpoch,
		VrfAuthorizationNonce: state.VrfAuthorizationNonce,
	}
	if state.XPendingVrfPubkey != nil {
		response.XPendingVrfPubkey = &types.QueryVrfKeyResponse_PendingVrfPubkey{
			PendingVrfPubkey: append([]byte(nil), state.GetPendingVrfPubkey()...),
		}
		response.XPendingFromEpoch = &types.QueryVrfKeyResponse_PendingFromEpoch{
			PendingFromEpoch: state.GetPendingFromEpoch(),
		}
	}
	return response, nil
}

// ValidatorBridgeSigner projects one validator's current bridge signer row
// (cross_chain_asset_bridge_protocol.md §4.1). The PoP signature is stored but not
// returned: it is
// recomputable from the row plus chain_id, and returning it would invite
// consumers to re-verify possession out of band instead of trusting the
// registration that already did.
func (q queryServer) ValidatorBridgeSigner(ctx context.Context, req *types.QueryValidatorBridgeSignerRequest) (*types.QueryValidatorBridgeSignerResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request must not be nil")
	}
	_, operator, err := q.k.requireCanonicalAddress("operator_address", req.OperatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	state, err := q.k.ValidatorBridgeSigner.Get(ctx, operator)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "operator %s has no registered bridge signer", operator)
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	if _, err := types.RequireEVMAddress("bridge_signer_address_raw20", state.BridgeSignerAddressRaw20); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryValidatorBridgeSignerResponse{
		Signer: types.ValidatorBridgeSignerViewV1{
			OperatorAddress:          state.OperatorAddress,
			BridgeSignerAddressRaw20: append([]byte(nil), state.BridgeSignerAddressRaw20...),
			KeyVersion:               state.KeyVersion,
			RegisteredHeight:         state.RegisteredHeight,
		},
	}, nil
}
