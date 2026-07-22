package kafka

// State is the Manager's observable one-way lifecycle state.
type State string

const (
	StateNew      State = "new"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateStopping State = "stopping"
	StateClosed   State = "closed"
	StateFailed   State = "failed"
)
