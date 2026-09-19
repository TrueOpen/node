package keeper

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
)

func TestClassifyPublicServiceErrorUsesStableFamilies(t *testing.T) {
	tests := []struct {
		message  string
		expected error
	}{
		{"unbonding is not mature", types.ErrDeadlineNotReached},
		{"service key cannot change while responsibilities are pending", types.ErrPendingResponsibility},
		{"expected current service authorization nonce does not match", types.ErrExpectedVersionMismatch},
		{"invalid service key proof-of-possession", types.ErrInvalidSignature},
		{"max open unbonding entries reached", types.ErrLimitExceeded},
		{"service unbonding not found", types.ErrUnbondingNotFound},
		{"available bond is below unstake amount", types.ErrInsufficientServiceBond},
	}
	for _, test := range tests {
		t.Run(test.message, func(t *testing.T) {
			err := classifyPublicServiceError(errors.New(test.message), types.ErrInvalidServiceBond)
			require.ErrorIs(t, err, test.expected)
		})
	}
}
