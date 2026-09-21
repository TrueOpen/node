package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

type verifierAdmissionHubStub struct {
	internalStubHubKeeper
	member                    hubtypes.CandidatePoolMemberState
	binding                   hubtypes.CandidateSlotBindingState
	profileHash               []byte
	performance               uint64
	performanceV              uint64
	signatureErr              error
	serviceByActor            map[string]string
	membersBySlot             map[uint32]hubtypes.CandidatePoolMemberState
	bindingsBySlot            map[uint32]hubtypes.CandidateSlotBindingState
	selectionBeacon           hubtypes.BeaconSnapshot
	reservations              *[]hubtypes.FrozenFactLiabilityRequest
	reservationErr            error
	reservationAttempts       *int
	reservationFailureAttempt int
	responsibilities          *[]hubtypes.ServiceKeyResponsibilityState
	responsibilityReleases    *[]string
	pendingStageDuties        *int
}

func (s verifierAdmissionHubStub) GetCortexNode(_ sdk.Context, address sdk.AccAddress) (hubtypes.CortexNodeSnapshot, bool) {
	return hubtypes.CortexNodeSnapshot{
		OperatorAddress: address.String(), ServiceAuthorizationNonce: 1,
		ServiceKeyStatus: hubtypes.ServiceKeyStatusActive,
	}, true
}

func (verifierAdmissionHubStub) GetModelStatus(sdk.Context, string) hubtypes.ModelStatus {
	return hubtypes.ModelStatusActive
}

func (s verifierAdmissionHubStub) GetProfileState(sdk.Context, string, uint32) (hubtypes.ProfileStateSnapshot, bool) {
	return hubtypes.ProfileStateSnapshot{
		Status: hubtypes.ModelStatusActive, MinStake: 1_000_000,
		ExecutionSnapshotHash: append([]byte(nil), s.profileHash...),
	}, true
}

func (verifierAdmissionHubStub) IsProfileFrozen(sdk.Context, string, uint32) bool { return false }
func (verifierAdmissionHubStub) IsEmergencyFrozen(sdk.Context) bool               { return false }

func (verifierAdmissionHubStub) GetProfileCapability(_ sdk.Context, address sdk.AccAddress, modelID string, profileVersion uint32) (hubtypes.ProfileCapabilitySnapshot, bool) {
	return hubtypes.ProfileCapabilitySnapshot{
		OperatorAddress: address.String(), ModelID: modelID, ProfileVersion: profileVersion,
		VerificationCapability: true, CapabilityVersion: 1,
	}, true
}

func (verifierAdmissionHubStub) GetModelSupport(_ sdk.Context, address sdk.AccAddress, modelID string, profileVersion uint32) (hubtypes.ModelSupportSnapshot, bool) {
	return hubtypes.ModelSupportSnapshot{
		OperatorAddress: address.String(), ModelID: modelID, ProfileVersion: profileVersion,
		DeclaredSupport: true, SupportActive: true, SupportFreshUntilEpoch: 10, SupportVersion: 1,
	}, true
}

func (verifierAdmissionHubStub) GetServiceBond(_ sdk.Context, address sdk.AccAddress, _ uint64) (hubtypes.ServiceBondSnapshot, bool) {
	return hubtypes.ServiceBondSnapshot{
		OperatorAddress: address.String(), Status: hubtypes.ServiceBondStatusActive,
		ActiveBond: 3_000_000, EffectiveActiveBond: 3_000_000, AvailableBond: 3_000_000,
		RequiredTaskLiability: 30_000, BondVersion: 1,
	}, true
}

func (verifierAdmissionHubStub) GetNodeJailStatus(sdk.Context, sdk.AccAddress, string) hubtypes.JailStatus {
	return ""
}

func (verifierAdmissionHubStub) GetNodeTombstone(sdk.Context, sdk.AccAddress) bool { return false }

func (s verifierAdmissionHubStub) GetRoleScoringSnapshot(sdk.Context, sdk.AccAddress, string) (hubtypes.RoleScoringSnapshot, error) {
	performance := s.performance
	if performance == 0 {
		performance = uint64(types.PerformanceScoreDefaultPpm)
	}
	version := s.performanceV
	if version == 0 {
		version = uint64(types.PerformanceMethodRawQ16V1)
	}
	return hubtypes.RoleScoringSnapshot{PerformanceScorePpm: performance, PerformanceVersion: version}, nil
}

func (s verifierAdmissionHubStub) VerifyCurrentCortexServiceDigest(context.Context, string, []byte, []byte, uint64) error {
	return s.signatureErr
}

func (s verifierAdmissionHubStub) GetCurrentServiceKey(_ context.Context, participantType, operatorAddress string) (hubtypes.CurrentServiceKeySnapshot, error) {
	serviceAddress := operatorAddress
	if rotated := s.serviceByActor[operatorAddress]; rotated != "" {
		serviceAddress = rotated
	}
	return hubtypes.CurrentServiceKeySnapshot{
		ParticipantType: participantType, OperatorAddress: operatorAddress,
		ServiceAddress: serviceAddress, Status: hubtypes.ServiceKeyStatusActive,
	}, nil
}

func (s verifierAdmissionHubStub) GetBeaconForDomain(_ sdk.Context, domain string, height int64) (hubtypes.BeaconSnapshot, bool) {
	if domain != shared.DomainWeightedDrawV1 && domain != shared.DomainVerifierWindowRankV1 {
		return hubtypes.BeaconSnapshot{}, false
	}
	if s.selectionBeacon.Height != height || len(s.selectionBeacon.Randomness) == 0 {
		return hubtypes.BeaconSnapshot{}, false
	}
	return s.selectionBeacon, true
}

func (s verifierAdmissionHubStub) ReserveTaskLiabilityFromFrozenFact(_ context.Context, request hubtypes.FrozenFactLiabilityRequest) (hubtypes.TaskLiabilityReservationState, error) {
	if s.reservationAttempts != nil {
		*s.reservationAttempts++
	}
	if s.reservationErr != nil && (s.reservationFailureAttempt == 0 ||
		(s.reservationAttempts != nil && *s.reservationAttempts == s.reservationFailureAttempt)) {
		return hubtypes.TaskLiabilityReservationState{}, s.reservationErr
	}
	if s.reservations != nil {
		*s.reservations = append(*s.reservations, request)
	}
	return hubtypes.TaskLiabilityReservationState{
		SchemaVersion: 1, TaskId: append([]byte(nil), request.TaskID...), OperatorAddress: request.OperatorAddress,
		Duty: request.Duty, BondVersion: request.BondVersionSnapshot,
		CapabilityVersion: request.CapabilityVersionSnapshot, ReservedAmount: request.RequiredTaskLiability,
		Status:                  hubtypes.TaskLiabilityStatusReserved,
		CandidatePoolSnapshotId: append([]byte(nil), request.CandidatePoolSnapshotID...),
		Slot:                    request.Slot, SlotVersion: request.SlotVersion,
	}, nil
}

func (s verifierAdmissionHubStub) ReserveServiceKeyResponsibility(_ context.Context, responsibility hubtypes.ServiceKeyResponsibilityState) error {
	if s.responsibilities != nil {
		*s.responsibilities = append(*s.responsibilities, responsibility)
	}
	return nil
}

func (s verifierAdmissionHubStub) ReleaseServiceKeyResponsibility(_ context.Context, participantType, operatorAddress, responsibilityID string) error {
	if s.responsibilityReleases != nil {
		*s.responsibilityReleases = append(*s.responsibilityReleases, participantType+"/"+operatorAddress+"/"+responsibilityID)
	}
	return nil
}

func (s verifierAdmissionHubStub) AcquirePendingStageDuty(context.Context, string) error {
	if s.pendingStageDuties != nil {
		*s.pendingStageDuties++
	}
	return nil
}

func (s verifierAdmissionHubStub) ReleasePendingStageDuty(context.Context, string) error {
	if s.pendingStageDuties != nil {
		*s.pendingStageDuties--
	}
	return nil
}

func (s verifierAdmissionHubStub) ResolveCandidatePoolMember(_ context.Context, _ []byte, slot uint32) (hubtypes.CandidatePoolMemberState, hubtypes.CandidateSlotBindingState, error) {
	if member, exists := s.membersBySlot[slot]; exists {
		return member, s.bindingsBySlot[slot], nil
	}
	return s.member, s.binding, nil
}

func (verifierAdmissionHubStub) GetCandidatePoolLayout(context.Context, []byte) (uint32, uint32, uint32, bool) {
	return 8, 1, 1, true
}
