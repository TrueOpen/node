package types

import (
	"bytes"
	"fmt"

	"github.com/cosmos/gogoproto/proto"
)

func DecodePageTokenV1(encoded, rpcDigest, selectorDigest []byte, queryHeight uint64) ([]byte, error) {
	if len(encoded) == 0 {
		return nil, nil
	}
	var token PageTokenV1
	if err := proto.Unmarshal(encoded, &token); err != nil {
		return nil, fmt.Errorf("page_token is not a PageTokenV1")
	}
	canonical, err := proto.Marshal(&token)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return nil, fmt.Errorf("page_token is not canonically encoded")
	}
	if !bytes.Equal(token.RpcMethodDigest, rpcDigest) || !bytes.Equal(token.SelectorDigest, selectorDigest) ||
		token.QueryHeight != queryHeight || len(token.LastPrimaryKey) == 0 {
		return nil, fmt.Errorf("page_token does not match this RPC, selector, or query height")
	}
	return append([]byte(nil), token.LastPrimaryKey...), nil
}

func EncodePageTokenV1(rpcDigest, selectorDigest, lastPrimaryKey []byte, queryHeight uint64) ([]byte, error) {
	if len(rpcDigest) != 32 || len(selectorDigest) != 32 || len(lastPrimaryKey) == 0 {
		return nil, fmt.Errorf("page_token scope and primary key are required")
	}
	return proto.Marshal(&PageTokenV1{
		RpcMethodDigest: append([]byte(nil), rpcDigest...), SelectorDigest: append([]byte(nil), selectorDigest...),
		LastPrimaryKey: append([]byte(nil), lastPrimaryKey...), QueryHeight: queryHeight,
	})
}
