package types

import "cosmossdk.io/collections"

var (
	PendingServiceKeyRotationKey         = MustVersionedStorePrefix("pending_service_key_rotation", CurrentStoreSchemaVersion)
	ServiceKeyRotationActivationIndexKey = MustVersionedStorePrefix("service_key_rotation_activation", CurrentStoreSchemaVersion)
	PendingServiceAddressIndexKey        = MustVersionedStorePrefix("pending_service_address", CurrentStoreSchemaVersion)
)

// ServiceKeyRotationActivationKey orders pending rotations by height and identity.
type ServiceKeyRotationActivationKey = collections.Triple[uint64, int32, string]
