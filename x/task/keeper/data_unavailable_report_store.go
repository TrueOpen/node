package keeper

import (
	"bytes"
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) dataUnavailableReportToStore(state types.DataUnavailableReportState) (internaltypes.DataUnavailableReportStoreState, error) {
	address, _, err := k.canonicalAddress("data unavailable report verifier", state.VerifierOperatorAddress)
	if err != nil {
		return internaltypes.DataUnavailableReportStoreState{}, err
	}
	return internaltypes.DataUnavailableReportStoreState{
		TaskId:                            append([]byte(nil), state.TaskId...),
		VerifyRound:                       state.VerifyRound,
		VerifierOperatorAddress:           append([]byte(nil), address...),
		UnavailableTaskBuilderBitmap:      append([]byte(nil), state.UnavailableTaskBuilderBitmap...),
		ServiceAuthorizationNonceSnapshot: state.ServiceAuthorizationNonceSnapshot,
		ReportHeight:                      state.ReportHeight,
		ReportDigest:                      append([]byte(nil), state.ReportDigest...),
	}, nil
}

func (k Keeper) ProjectDataUnavailableReportStore(stored internaltypes.DataUnavailableReportStoreState) (types.DataUnavailableReportState, error) {
	address, err := k.sessionAddressFromStore("data unavailable report verifier", stored.VerifierOperatorAddress)
	if err != nil {
		return types.DataUnavailableReportState{}, err
	}
	return types.DataUnavailableReportState{
		TaskId:                            append([]byte(nil), stored.TaskId...),
		VerifyRound:                       stored.VerifyRound,
		VerifierOperatorAddress:           address,
		UnavailableTaskBuilderBitmap:      append([]byte(nil), stored.UnavailableTaskBuilderBitmap...),
		ServiceAuthorizationNonceSnapshot: stored.ServiceAuthorizationNonceSnapshot,
		ReportHeight:                      stored.ReportHeight,
		ReportDigest:                      append([]byte(nil), stored.ReportDigest...),
	}, nil
}

func dataUnavailableReportKeyMatches(key types.VerifyActorKey, taskID []byte, round uint32, operator string) bool {
	return bytes.Equal(key.K1(), taskID) && key.K2() == round && key.K3() == operator
}

func (k Keeper) ReadDataUnavailableReport(ctx context.Context, key types.VerifyActorKey) (types.DataUnavailableReportState, error) {
	stored, err := k.DataUnavailableReport.Get(ctx, key)
	if err != nil {
		return types.DataUnavailableReportState{}, err
	}
	state, err := k.ProjectDataUnavailableReportStore(stored)
	if err != nil {
		return types.DataUnavailableReportState{}, err
	}
	if !dataUnavailableReportKeyMatches(key, state.TaskId, state.VerifyRound, state.VerifierOperatorAddress) {
		return types.DataUnavailableReportState{}, fmt.Errorf("data unavailable report key/value mismatch")
	}
	return state, nil
}

func (k Keeper) WriteDataUnavailableReport(ctx context.Context, key types.VerifyActorKey, state types.DataUnavailableReportState) error {
	if !dataUnavailableReportKeyMatches(key, state.TaskId, state.VerifyRound, state.VerifierOperatorAddress) {
		return fmt.Errorf("data unavailable report key/value mismatch")
	}
	stored, err := k.dataUnavailableReportToStore(state)
	if err != nil {
		return err
	}
	return k.DataUnavailableReport.Set(ctx, key, stored)
}

func (k Keeper) exportDataUnavailableReports(ctx context.Context) ([]types.DataUnavailableReportState, error) {
	iter, err := k.DataUnavailableReport.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.DataUnavailableReportState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		state, err := k.ProjectDataUnavailableReportStore(entry.Value)
		if err != nil {
			return nil, err
		}
		if !dataUnavailableReportKeyMatches(entry.Key, state.TaskId, state.VerifyRound, state.VerifierOperatorAddress) {
			return nil, fmt.Errorf("data unavailable report key/value mismatch")
		}
		states = append(states, state)
	}
	return states, nil
}
