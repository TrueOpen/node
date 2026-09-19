package keeper

import (
	"errors"
	"strings"

	errorsmod "cosmossdk.io/errors"

	"github.com/TrueOpen/node/x/hub/types"
)

var publicServiceErrors = []error{
	types.ErrInvalidServiceBond,
	types.ErrServiceBondNotFound,
	types.ErrInsufficientServiceBond,
	types.ErrInvalidServiceProvider,
	types.ErrServiceProviderNotFound,
	types.ErrInvalidSigner,
	types.ErrDeadlineNotReached,
	types.ErrPendingResponsibility,
	types.ErrExpectedVersionMismatch,
	types.ErrInvalidSignature,
	types.ErrLimitExceeded,
	types.ErrUnbondingNotFound,
}

func classifyPublicServiceError(err error, fallback error) error {
	if err == nil {
		return nil
	}
	for _, sentinel := range publicServiceErrors {
		if errors.Is(err, sentinel) {
			return err
		}
	}
	message := strings.ToLower(err.Error())
	sentinel := fallback
	switch {
	case strings.Contains(message, "not mature"), strings.Contains(message, "deadline"):
		sentinel = types.ErrDeadlineNotReached
	case strings.Contains(message, "responsibilit"), strings.Contains(message, "duties are open"), strings.Contains(message, "pending slash hold"):
		sentinel = types.ErrPendingResponsibility
	case strings.Contains(message, "nonce does not match"), strings.Contains(message, "expected version"):
		sentinel = types.ErrExpectedVersionMismatch
	case strings.Contains(message, "signature"), strings.Contains(message, "proof-of-possession"), strings.Contains(message, "service_key_proof"):
		sentinel = types.ErrInvalidSignature
	case strings.Contains(message, "max open"), strings.Contains(message, "max_items"), strings.Contains(message, "configured limit"):
		sentinel = types.ErrLimitExceeded
	case strings.Contains(message, "unbonding not found"):
		sentinel = types.ErrUnbondingNotFound
	case strings.Contains(message, "does not exist"), strings.Contains(message, "not found"):
		sentinel = types.ErrServiceBondNotFound
	case strings.Contains(message, "below unstake"), strings.Contains(message, "must be at least"), strings.Contains(message, "insufficient"):
		sentinel = types.ErrInsufficientServiceBond
	}
	return errorsmod.Wrap(sentinel, err.Error())
}
