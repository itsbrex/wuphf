package team

import "strings"

// app_builder_agent.go holds what remains of the App Builder, an agent
// RETIRED as a default. New workspaces do not seed it and nothing back-fills
// it; app building is a system skill every agent carries (the register_app
// gate in teammcp is open to all agents). Legacy workspaces still hold the
// member on disk and load it as an ordinary, removable agent.
//
// appBuilderSlug itself is defined in broker_apps_proposal.go (the proposal
// path needed it first).

const appBuilderRole = "App Builder"

// isAppBuilderSlug reports whether slug is the App Builder (case-insensitive).
func isAppBuilderSlug(slug string) bool {
	return strings.EqualFold(strings.TrimSpace(slug), appBuilderSlug)
}
