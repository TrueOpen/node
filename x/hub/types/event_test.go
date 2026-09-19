package types

import (
	"reflect"
	"strings"
	"testing"

	proto "github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"
)

// The following eight rows were deleted from the list below rather
// than renamed, because proto/hub/v1/event.proto:525-548 records that they
// have no successor message in this revision:
//
//   - EventEarningsPendingMatured: no §5.11 code exists; codes 40/41
//     (EventEarningsAccrued / EventEarningsClaimed) carry the whole earnings
//     ledger surface. The maturity path must assert the code it emits.
//   - EventRewardEligibilityUpdated, EventRewardEpochCursorAdvanced,
//     EventBuilderRewardEpochClosed, EventTreasuryEpochClosed,
//     EventModelSupportP30Candidate: §5.11 forbids a runner-level summary event
//     and the builder-reward / treasury-epoch ledgers are deleted. Reward runner
//     tests must assert the codes it really emits (70, 80, 81).
//   - EventChallengeBondUpdated, EventChallengeEconomicEffectApplied,
//     EventChallengeEffectPoolUpdated: K-BLOCK-03/04, no public challenge event
//     code may be assigned yet.
//   - EventBuilderSetUpdated: §5.11 codes 60/90/91/92 are not registered in this
//     revision (its payload still described the deleted per-profile pool and the
//     string-typed builder set).
//
// Renamed in place, assertion intent unchanged (same event code, same fact):
// EventFreezeSignalClosed -> EventFreezeSignalExpired (99),
// EventEmergencyFreezeVoteAccepted -> EventEmergencyFreezeVoteRecorded (96),
// EventRewardMarked + EventMarkGateUpdated -> EventMarkHit (70, two old names
// collapse into one message so only one row remains),
// EventReferenceBucketUpdated -> EventParameterBucketUpdated (5).
func TestHubEventDescriptorsAreOwnedAndUnique(t *testing.T) {
	events := []proto.Message{
		&EventModelProfileRegistered{},
		&EventModelProfileStateChanged{},
		&EventModelStateChanged{},
		&EventServiceKeyRotated{},
		&EventServiceKeyRevoked{},
		&EventServiceDescriptorUpdated{},
		&EventEarningsClaimed{},
		&EventFreezeSignalExpired{},
		&EventModelSupportUpdated{},
		&EventModelSupportActivated{},
		&EventFreezeSignalSubmitted{},
		&EventEmergencyFreezeVoteRecorded{},
		&EventEmergencyFreezeAccepted{},
		&EventServiceStakeChanged{},
		&EventServiceUnbondingStarted{},
		&EventServiceUnbondingWithdrawn{},
		&EventParameterBucketUpdated{},
	}

	names := make(map[string]struct{}, len(events))
	for _, event := range events {
		name := proto.MessageName(event)
		require.True(t, strings.HasPrefix(name, "hub.v1.Event"), name)
		_, duplicate := names[name]
		require.False(t, duplicate, name)
		names[name] = struct{}{}
	}
}

func TestHubEventsRoundTripWithoutStatePayloads(t *testing.T) {
	events := []proto.Message{
		&EventModelProfileRegistered{
			ModelId:               "model-a",
			ProfileVersion:        3,
			ManifestHash:          make([]byte, 32),
			Proposer:              "trueopen1proposer",
			RegistrationFeeAmount: AmountFromUint64(50),
		},
		&EventModelProfileStateChanged{
			ModelId:        "model-a",
			ProfileVersion: 3,
			OldStatus:      ModelStatusRegistered,
			NewStatus:      ModelStatusActive,
			Source:         ProfileStatusSourceAutoSupport,
		},
	}

	for _, event := range events {
		encoded, err := proto.Marshal(event)
		require.NoError(t, err)
		decoded := reflect.New(reflect.TypeOf(event).Elem()).Interface().(proto.Message)
		require.NoError(t, proto.Unmarshal(encoded, decoded))
		require.True(t, proto.Equal(event, decoded), proto.MessageName(event))
	}
}
