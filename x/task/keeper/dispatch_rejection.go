package keeper

import (
	"errors"
	"fmt"

	"github.com/TrueOpen/node/x/task/types"
)

var errDispatchBatchDependency = errors.New("an earlier Session sequence was rejected in this batch")

// dispatchRejection is a business failure isolated to one batch item. Store and
// invariant failures retain their original errors and abort the enclosing Msg.
type dispatchRejection struct {
	code  types.DispatchRejectCodeV1
	cause error
}

func (e *dispatchRejection) Error() string { return fmt.Sprintf("%s: %v", e.code, e.cause) }
func (e *dispatchRejection) Unwrap() error { return e.cause }

func rejectDispatch(code types.DispatchRejectCodeV1, cause error) error {
	return &dispatchRejection{code: code, cause: cause}
}

func dispatchRejectionCode(err error) (types.DispatchRejectCodeV1, bool) {
	var rejected *dispatchRejection
	if errors.As(err, &rejected) {
		return rejected.code, true
	}
	switch {
	case errors.Is(err, types.ErrSessionKeyInvalid):
		return types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_SESSION_KEY_INVALID, true
	case errors.Is(err, types.ErrSessionKeyExpired):
		return types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_SESSION_KEY_EXPIRED, true
	case errors.Is(err, types.ErrSessionKeySpendCapExceeded):
		return types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_SESSION_KEY_SPEND_CAP_EXCEEDED, true
	}
	return types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_UNSPECIFIED, false
}
