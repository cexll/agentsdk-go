package agent

import (
	"fmt"
	"strings"
)

// TeamRole categorizes the responsibilities of a team member.
type TeamRole string

const (
	TeamRoleUnknown  TeamRole = ""
	TeamRoleLeader   TeamRole = "leader"
	TeamRoleWorker   TeamRole = "worker"
	TeamRoleReviewer TeamRole = "reviewer"
)

// ParseTeamRole normalizes user supplied strings into a TeamRole constant.
func ParseTeamRole(raw string) (TeamRole, error) {
	role := TeamRole(strings.ToLower(strings.TrimSpace(raw)))
	switch role {
	case TeamRoleLeader, TeamRoleWorker, TeamRoleReviewer:
		return role, nil
	case TeamRoleUnknown:
		return TeamRoleUnknown, nil
	default:
		return TeamRoleUnknown, fmt.Errorf("team: unknown role %q", raw)
	}
}

// Default returns TeamRoleWorker when the receiver is not set.
func (r TeamRole) Default() TeamRole {
	if r == TeamRoleUnknown {
		return TeamRoleWorker
	}
	return r
}

// String implements fmt.Stringer.
func (r TeamRole) String() string {
	if r == TeamRoleUnknown {
		return "unknown"
	}
	return string(r)
}

// Validate ensures the role is a known variant.
func (r TeamRole) Validate() error {
	switch r.Default() {
	case TeamRoleLeader, TeamRoleWorker, TeamRoleReviewer:
		return nil
	default:
		return fmt.Errorf("team: unsupported role %q", r)
	}
}

// Matches reports whether candidate equals the receiver or the receiver is unknown.
func (r TeamRole) Matches(candidate TeamRole) bool {
	if r == TeamRoleUnknown {
		return true
	}
	return r == candidate
}
