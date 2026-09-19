package types

const (
	DutyWorker   = Duty_DUTY_WORKER
	DutyVerifier = Duty_DUTY_VERIFIER
)

// These strings are used only by the legacy positional HubKeeper service-key
// lookup boundary. Consensus fields and hash preimages use ParticipantType.
const (
	ParticipantTypeBuilder    = "BUILDER"
	ParticipantTypeCortexNode = "CORTEX"
)
