package team

import (
	"regexp"
	"strings"
)

// Tool results can carry a whole screenshot as base64 (the computer tools
// return image blocks). That payload must never become the activity pill's
// text: the sidebar showed `[{"source":{"data":"iVBORw0KGgo…` while a bot
// looked at its screen. Detect the common image encodings and describe the
// result instead of quoting it.
var imagePayloadPattern = regexp.MustCompile(`(?:"data"\s*:\s*"|data:image/[a-z]+;base64,)(?:iVBORw0KGgo|/9j/|R0lGOD|UklGR)`)

// progressDetail is the activity-pill text for a tool result.
func progressDetail(text string, limit int) string {
	if imagePayloadPattern.MatchString(text) || strings.HasPrefix(strings.TrimSpace(text), "iVBORw0KGgo") || strings.HasPrefix(strings.TrimSpace(text), "/9j/") {
		return "looked at the screen (image result)"
	}
	return truncate(text, limit)
}
