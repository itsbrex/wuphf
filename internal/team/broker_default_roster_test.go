package team

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The founder retired every default agent except the lead: "planner,executor,
// reviewer always show up as default in a workspace. let's remove them
// completely from everywhere. that concept should now be gone with those bots
// as default. their defintions also shouldn't exist" — after twice asking for
// the Librarian and App Builder to go ("there should be no librarian or app
// builder agent anymore by default").
//
// These tests pin the OUTCOME on a constructed broker, not that a seeding
// function returned early. That distinction is the whole reason this file
// exists: the first removal attempt edited the default roster and looked done,
// while the ensure-style back-fills quietly re-added both agents on every
// load. A test on the seed list alone stayed green through the entire bug.

// A brand-new office is the Chief of Staff alone, reachable in one DM.
// Specialists are created on demand, not preinstalled.
func TestFreshOfficeSeedsOnlyTheChiefOfStaff(t *testing.T) {
	b := NewBrokerAt(filepath.Join(t.TempDir(), "broker-state.json"))
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.members) != 1 {
		slugs := make([]string, 0, len(b.members))
		for _, m := range b.members {
			slugs = append(slugs, m.Slug)
		}
		t.Fatalf("fresh roster = %v, want exactly [ceo]", slugs)
	}
	m := b.members[0]
	if m.Slug != "ceo" {
		t.Fatalf("sole member slug = %q, want \"ceo\"", m.Slug)
	}
	if m.Name != "Chief of Staff" {
		t.Errorf("lead name = %q, want \"Chief of Staff\"", m.Name)
	}
	if !m.BuiltIn {
		t.Error("the lead must stay built-in (undeletable)")
	}

	var dms []string
	for _, c := range b.channels {
		if c.Type == "dm" || IsDMSlug(c.Slug) {
			dms = append(dms, c.Slug)
		}
	}
	if len(dms) != 1 || dms[0] != "ceo__human" {
		t.Errorf("DMs = %v, want exactly [ceo__human]", dms)
	}
}

// The retired agents' definitions are gone from every seed path, and — the
// part that failed last time — nothing re-adds them on load. A roster without
// them stays without them.
func TestLoadDoesNotResurrectRetiredAgents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broker-state.json")
	b := NewBrokerAt(path)
	b.mu.Lock()
	if err := b.saveLocked(); err != nil {
		t.Fatalf("save: %v", err)
	}
	b.mu.Unlock()

	// A second boot is where the ensure-style back-fills used to fire.
	b2 := NewBrokerAt(path)
	b2.mu.Lock()
	defer b2.mu.Unlock()
	for _, m := range b2.members {
		switch m.Slug {
		case "librarian", "app-builder", "planner", "executor", "reviewer":
			t.Errorf("reboot resurrected retired agent %q: a back-fill is still live", m.Slug)
		}
	}
	if len(b2.members) != 1 {
		t.Errorf("roster after reboot has %d members, want 1", len(b2.members))
	}
}

// Existing workspaces are the other half of the contract. Their retired
// agents own DMs, tasks, and history on real disks, so they LOAD unchanged —
// except for two reconciliations: the Office-referencing display name
// "Pam the librarian" is rebranded (the founder banned Office references),
// and the retired agents lose BuiltIn so users can finally delete them.
func TestLegacyWorkspaceKeepsItsAgentsButDropsTheOfficeName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broker-state.json")

	legacy := map[string]any{
		"members": []map[string]any{
			{"slug": "ceo", "name": "CEO", "role": "CEO", "built_in": true},
			{"slug": "librarian", "name": "Pam the librarian", "role": "Librarian", "built_in": true},
			{"slug": "app-builder", "name": "App Builder", "role": "App Builder", "built_in": true},
			{"slug": "planner", "name": "Planner", "role": "Planner"},
		},
		"channels": []map[string]any{
			{"slug": "human__librarian", "name": "human__librarian", "type": "dm", "members": []string{"human", "librarian"}},
		},
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// The package TestMain sets skipBrokerStateLoadOnConstruct so tests start
	// fresh; this test exists precisely to exercise the disk-load path, so it
	// opts back in (same idiom as TestNewBroker_SkipStateLoadGateRespected —
	// safe while no test here calls t.Parallel()).
	oldGate := skipBrokerStateLoadOnConstruct
	skipBrokerStateLoadOnConstruct = false
	t.Cleanup(func() { skipBrokerStateLoadOnConstruct = oldGate })

	b := NewBrokerAt(path)
	b.mu.Lock()
	defer b.mu.Unlock()

	got := map[string]officeMember{}
	for _, m := range b.members {
		got[m.Slug] = m
	}
	for _, slug := range []string{"ceo", "librarian", "app-builder", "planner"} {
		if _, ok := got[slug]; !ok {
			t.Fatalf("legacy member %q was dropped on load: existing workspaces must keep their agents", slug)
		}
	}
	if name := got["librarian"].Name; name != "Librarian" {
		t.Errorf("librarian name = %q, want \"Librarian\" (Office references are banned)", name)
	}
	if got["librarian"].BuiltIn {
		t.Error("legacy librarian still BuiltIn: users could not delete an agent the product no longer defines")
	}
	if got["app-builder"].BuiltIn {
		t.Error("legacy app-builder still BuiltIn: users could not delete an agent the product no longer defines")
	}
	if !got["ceo"].BuiltIn {
		t.Error("the lead lost BuiltIn on load")
	}
	if got["ceo"].Name != "Chief of Staff" {
		t.Errorf("lead name = %q, want the reconciled \"Chief of Staff\"", got["ceo"].Name)
	}
	if b.findChannelLocked("human__librarian") == nil {
		t.Error("legacy librarian DM was dropped on load")
	}
}
