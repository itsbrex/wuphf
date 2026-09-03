package teammcp

import (
	"context"
	"fmt"
	"strings"
)

// System-skill gates. App building and wiki maintenance are system skills:
// always present, enabled for every agent by default, and disableable per
// agent from the Skills tab (internal/team/system_skills.go). The MCP tools
// that exercise those capabilities check the switch before acting.

const (
	systemSkillAppBuilding     = "app-building"
	systemSkillWikiMaintenance = "wiki-maintenance"
)

// systemSkillEnabledFor reads the broker's skill list and honors a per-agent
// switch-off on the named system skill. Fail-open on every error path: the
// gate exists to honor an explicit disable, and a broken skills read must
// never brick an agent's core capabilities.
func systemSkillEnabledFor(ctx context.Context, skillName, agent string) bool {
	agent = strings.TrimSpace(strings.ToLower(agent))
	if agent == "" {
		return true
	}
	var resp struct {
		Skills []struct {
			Name           string   `json:"name"`
			System         bool     `json:"system"`
			DisabledAgents []string `json:"disabled_agents"`
		} `json:"skills"`
	}
	if err := brokerGetJSON(ctx, "/skills", &resp); err != nil {
		return true
	}
	for _, sk := range resp.Skills {
		if !sk.System || !strings.EqualFold(strings.TrimSpace(sk.Name), skillName) {
			continue
		}
		for _, off := range sk.DisabledAgents {
			if strings.EqualFold(strings.TrimSpace(off), agent) {
				return false
			}
		}
		return true
	}
	return true
}

// systemSkillDisabledError is the uniform refusal a gated tool returns.
func systemSkillDisabledError(skillName, agent string) error {
	return fmt.Errorf(
		"the %s system skill is disabled for @%s. Ask the human to re-enable it from the Skills tab (POST /skills/%s/enable-for) if this work is yours to do",
		skillName, agent, skillName,
	)
}
