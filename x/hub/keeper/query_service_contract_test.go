package keeper_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestServiceUnbondingsPageTokenBindsRawAddressAndStatus(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))

	operator := hubAddress(t, 0xd6)
	firstID := bytes.Repeat([]byte{0x41}, shared.Hash32KeySize)
	secondID := bytes.Repeat([]byte{0x42}, shared.Hash32KeySize)
	for index, id := range [][]byte{firstID, secondID} {
		row := types.UnbondingState{
			UnbondingId: id, OperatorAddress: operator, Amount: 100,
			RequestHeight: 10, MatureHeight: uint64(20 + index), Status: types.UnbondingStatusOpen,
		}
		require.NoError(t, row.Validate())
		require.NoError(t, f.keeper.WriteUnbondingValue(
			f.ctx, types.NewUnbondingKey(operator, shared.Hash32Key(id)), row,
		))
	}

	queries := keeper.NewQueryServerImpl(f.keeper)
	request := &types.QueryServiceUnbondingsRequest{
		OperatorAddress: operator,
		Status:          types.UnbondingStatusOpen,
		Page:            shared.QueryPageRequestV1{Limit: 1},
	}
	first, err := queries.ServiceUnbondings(f.ctx, request)
	require.NoError(t, err)
	require.Len(t, first.Entries, 1)
	require.NotEmpty(t, first.Page.NextPageToken)

	var token shared.PageTokenV1
	require.NoError(t, proto.Unmarshal(first.Page.NextPageToken, &token))
	operatorBytes, err := sdk.AccAddressFromBech32(operator)
	require.NoError(t, err)
	sdkCtx := sdk.UnwrapSDKContext(f.ctx)
	rpcDigest := shared.CanonicalHashBytes(
		shared.MustDomain(shared.DomainQueryRPCV1), []byte("/hub.v1.Query/ServiceUnbondings"),
	)
	selectorDigest := shared.CanonicalHashBytes(
		shared.MustDomain(shared.DomainQuerySelectorV1), []byte(sdkCtx.ChainID()), rpcDigest,
		operatorBytes, shared.EnumBE(uint32(types.UnbondingStatusOpen)),
	)
	require.Equal(t, rpcDigest, token.RpcMethodDigest)
	require.Equal(t, selectorDigest, token.SelectorDigest)
	require.Equal(t, uint64(sdkCtx.BlockHeight()), token.QueryHeight)
	require.Equal(t, "8d4135919c2c5feae0f96dc269e9290aa6aad992a63d6f9e601a9cac21a22534", hex.EncodeToString(rpcDigest))
	require.Equal(t, "9f83016d5dc5c643c1ccbcef8f30b5d6affbb5c3824e1110a5ac4aa0cef8202e", hex.EncodeToString(selectorDigest))

	request.Page.PageToken = first.Page.NextPageToken
	second, err := queries.ServiceUnbondings(f.ctx, request)
	require.NoError(t, err)
	require.Len(t, second.Entries, 1)
	require.Empty(t, second.Page.NextPageToken)

	_, err = queries.ServiceUnbondings(f.ctx, &types.QueryServiceUnbondingsRequest{
		OperatorAddress: operator,
		Status:          types.UnbondingStatusMature,
		Page:            shared.QueryPageRequestV1{Limit: 1, PageToken: first.Page.NextPageToken},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err), "status changes must invalidate the token")

	_, err = queries.ServiceUnbondings(f.ctx, &types.QueryServiceUnbondingsRequest{
		OperatorAddress: hubAddress(t, 0xd7),
		Status:          types.UnbondingStatusOpen,
		Page:            shared.QueryPageRequestV1{Limit: 1, PageToken: first.Page.NextPageToken},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err), "operator changes must invalidate the token")
}

func TestServiceLifecycleRetainsTerminalBondAfterNodeCleanup(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	operator := hubAddress(t, 0xd8)
	bond := types.ServiceBondState{
		OperatorAddress: operator, ActiveBond: 999_100, EffectiveActiveBond: 999_100,
		BondVersion: 2, Status: types.ServiceBondStatus_SERVICE_BOND_STATUS_TOMBSTONED,
		JailCount: 1, LastStakeHeight: 1,
	}
	require.NoError(t, bond.Validate())
	require.NoError(t, f.keeper.WriteServiceBondValue(f.ctx, operator, bond))

	queries := keeper.NewQueryServerImpl(f.keeper)
	response, err := queries.ServiceLifecycle(f.ctx, &types.QueryServiceLifecycleRequest{OperatorAddress: operator})
	require.NoError(t, err)
	require.Nil(t, response.Lifecycle.XNode)
	require.Equal(t, bond, response.Lifecycle.Bond)

	bond.Status = types.ServiceBondStatus_SERVICE_BOND_STATUS_ACTIVE
	require.NoError(t, f.keeper.WriteServiceBondValue(f.ctx, operator, bond))
	_, err = queries.ServiceLifecycle(f.ctx, &types.QueryServiceLifecycleRequest{OperatorAddress: operator})
	require.Equal(t, codes.Internal, status.Code(err), "an active bond still requires its Cortex primary")

	require.NoError(t, f.keeper.ServiceBond.Remove(f.ctx, operator))
	node := types.CortexNodeState{OperatorAddress: operator}
	require.NoError(t, f.keeper.StoreCortexNode(f.ctx, operator, node))
	_, err = queries.ServiceLifecycle(f.ctx, &types.QueryServiceLifecycleRequest{OperatorAddress: operator})
	require.Equal(t, codes.Internal, status.Code(err), "a Cortex primary without its bond is an invariant failure")
}
