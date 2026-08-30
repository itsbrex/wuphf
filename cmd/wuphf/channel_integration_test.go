package main

import (
	"testing"

	"github.com/nex-crm/wuphf/internal/company"
	"github.com/nex-crm/wuphf/internal/team"
)

func TestSlugifyGroupTitleAddsTgPrefix(t *testing.T) {
	cases := map[string]string{
		"My Team":             "tg-my-team",
		" My!@#$Team Lounge ": "tg-my-team-lounge",
		"---":                 "tg-telegram",
		"":                    "tg-telegram",
		"NUMBERS 123":         "tg-numbers-123",
	}
	for input, want := range cases {
		if got := team.SlugifyTelegramTitle(input); got != want {
			t.Errorf("team.SlugifyTelegramTitle(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSlugifyOpenclawLabelFallsBackToSession(t *testing.T) {
	cases := map[string]string{
		"My Session":  "my-session",
		"--- ---":     "session",
		"":            "session",
		"NUMBERS 123": "numbers-123",
	}
	for input, want := range cases {
		if got := slugifyOpenclawLabel(input); got != want {
			t.Errorf("slugifyOpenclawLabel(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestFindManifestTelegramChannelMatchesRemoteIDOnly(t *testing.T) {
	manifest := company.Manifest{Channels: []company.ChannelSpec{{
		Slug: "tg-old-title",
		Surface: &company.ChannelSurfaceSpec{
			Provider: "telegram",
			RemoteID: "100",
		},
	}}}

	got, err := findManifestTelegramChannel(manifest, "tg-new-title", "100")
	if err != nil {
		t.Fatalf("same remote id: unexpected error: %v", err)
	}
	if got != "tg-old-title" {
		t.Fatalf("same remote id: got %q, want existing slug", got)
	}

	manifest.Channels = append(manifest.Channels, company.ChannelSpec{
		Slug: "tg-new-title",
		Surface: &company.ChannelSurfaceSpec{
			Provider: "telegram",
			RemoteID: "200",
		},
	})
	if _, err := findManifestTelegramChannel(manifest, "tg-new-title", "300"); err == nil {
		t.Fatal("slug collision with different remote id: expected error")
	}
}

func TestFindLiveTelegramChannelMatchesRemoteIDOnly(t *testing.T) {
	channels := []telegramBrokerChannel{{
		Slug: "tg-old-title",
		Surface: &telegramBrokerSurface{
			Provider: "telegram",
			RemoteID: "100",
		},
	}}

	got, err := findLiveTelegramChannel(channels, "tg-new-title", "100")
	if err != nil {
		t.Fatalf("same remote id: unexpected error: %v", err)
	}
	if got != "tg-old-title" {
		t.Fatalf("same remote id: got %q, want existing slug", got)
	}

	if _, err := findLiveTelegramChannel([]telegramBrokerChannel{{
		Slug: "tg-new-title",
	}}, "tg-new-title", "100"); err == nil {
		t.Fatal("slug collision with non-telegram channel: expected error")
	}

	if _, err := findLiveTelegramChannel([]telegramBrokerChannel{{
		Slug: "tg-new-title",
		Surface: &telegramBrokerSurface{
			Provider: "telegram",
			RemoteID: "200",
		},
	}}, "tg-new-title", "100"); err == nil {
		t.Fatal("slug collision with different Telegram remote id: expected error")
	}
}
