package api

import (
	"fmt"
	"strings"
)

// Mode controls which API surface a Lethe process exposes.
//
// Legacy is the default OpenLethe behavior. Git is the Charon-only, versioned
// memory surface. Hybrid exists for migration and development; production
// deployments should prefer two processes with separate databases.
type Mode string

const (
	ModeLegacy Mode = "legacy"
	ModeGit    Mode = "git"
	ModeHybrid Mode = "hybrid"
)

// ParseMode validates a configured Lethe API mode. An empty value preserves
// the locally installed OpenLethe behavior.
func ParseMode(value string) (Mode, error) {
	switch mode := Mode(strings.ToLower(strings.TrimSpace(value))); mode {
	case "", ModeLegacy:
		return ModeLegacy, nil
	case ModeGit:
		return ModeGit, nil
	case ModeHybrid:
		return ModeHybrid, nil
	default:
		return "", fmt.Errorf("invalid Lethe mode %q; must be legacy, git, or hybrid", value)
	}
}

// WithMode selects the API surface exposed by a Server.
func WithMode(mode Mode) Option {
	return func(s *Server) {
		s.mode = mode
	}
}

// LegacyEnabled reports whether session/event/checkpoint APIs are available.
func (m Mode) LegacyEnabled() bool {
	return m == ModeLegacy || m == ModeHybrid
}

// GitEnabled reports whether Memory Git APIs are available.
func (m Mode) GitEnabled() bool {
	return m == ModeGit || m == ModeHybrid
}
