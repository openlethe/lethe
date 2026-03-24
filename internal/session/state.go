package session

import "github.com/mentholmike/lethe/internal/models"

// ValidTransitions maps valid state transitions.
var ValidTransitions = map[models.SessionState]map[models.SessionState]bool{
	models.SessionActive: {
		models.SessionInterrupted: true,
		models.SessionCompleted:   true,
	},
	models.SessionInterrupted: {
		models.SessionActive:     true, // resume
		models.SessionCompleted: true,
	},
	models.SessionCompleted: {}, // terminal
}

// IsValidTransition returns true if a transition is allowed.
func IsValidTransition(from, to models.SessionState) bool {
	if from == to {
		return true // no-op is valid
	}
	return ValidTransitions[from][to]
}

// HeartbeatThreshold is the maximum time between heartbeats before a session
// is considered interrupted.
const HeartbeatThresholdSeconds = 300 // 5 minutes

// ShouldCheckpoint returns true when a state transition should also write a
// checkpoint snapshot.
func ShouldCheckpoint(from, to models.SessionState) bool {
	return from == models.SessionActive && to == models.SessionInterrupted
}
