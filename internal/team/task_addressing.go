package team

import (
	"strings"
	"unicode"
)

// messageAddressesTask reports whether a channel message is aimed at a
// particular task.
//
// This exists because the one-room change removed the address that used to be
// implicit. When every task had its own channel, posting in that channel WAS
// the address — the broker could wake the task simply because the message
// landed in its room. Now that the whole office shares #general, "which task is
// this about" has to be read off the message itself.
//
// Two signals, both things a human actually does:
//
//   - The message is a reply in the task's thread. The strongest signal there
//     is: the human clicked into the task's conversation.
//   - The message names the task id in its text ("DUNDE-12", "task-3"). The
//     chat surface renders these as links to the task card, so a human writing
//     one is deliberately pointing at that task.
//
// A bare @mention of the owner is deliberately NOT a signal. Owners hold many
// tasks at once, so it would wake all of them and reproduce the broadcast storm
// the old #general guard was written to avoid.
func messageAddressesTask(msg channelMessage, task *teamTask) bool {
	if task == nil {
		return false
	}
	if thread := strings.TrimSpace(task.ThreadID); thread != "" {
		if strings.TrimSpace(msg.ReplyTo) == thread {
			return true
		}
	}
	// A message the broker itself emitted about this task (a delivery post, a
	// human-change announcement) carries the task id directly.
	if strings.TrimSpace(msg.SourceTaskID) == strings.TrimSpace(task.ID) && strings.TrimSpace(task.ID) != "" {
		return true
	}
	return textMentionsTaskID(msg.Content, task.ID)
}

// textMentionsTaskID reports whether body names taskID as a standalone token.
//
// Substring matching is not enough in either direction: "DUNDE-1" must not
// match inside "DUNDE-12" (the wrong task would wake), and the id has to be
// found even when punctuation hugs it ("fixed DUNDE-12." or "(DUNDE-12)").
//
// Matching is CASE-SENSITIVE, and that is a deliberate cross-layer contract
// rather than strictness for its own sake. The justification for treating a
// typed id as an address is that the chat surface renders it as a link to the
// task card — the human can see they addressed something. That renderer is
// case-sensitive: it matches an uppercase prefix form (DUNDE-12) or the
// lowercase literal form (task-12), and nothing else, because a
// case-insensitive pattern also linkifies ordinary words like "request-75".
//
// So a case-insensitive match here would wake a task on "dunde-12" while the
// human saw only plain text — addressed on the server, invisible in the UI,
// with no confirmation anything had happened. What the interface shows as a
// link is the honest definition of what a person deliberately addressed, so
// this side matches that and not more.
func textMentionsTaskID(body, taskID string) bool {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || strings.TrimSpace(body) == "" {
		return false
	}
	for offset := 0; ; {
		idx := strings.Index(body[offset:], taskID)
		if idx < 0 {
			return false
		}
		start := offset + idx
		end := start + len(taskID)
		if isTaskIDBoundary(body, start-1) && isTaskIDBoundary(body, end) {
			return true
		}
		offset = start + 1
		if offset >= len(body) {
			return false
		}
	}
}

// isTaskIDBoundary reports whether position i sits outside the id token. A
// hyphen counts as part of the token so "DUNDE-1" does not match the "DUNDE-1"
// prefix of "DUNDE-12"; digits and letters do too.
func isTaskIDBoundary(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return true
	}
	c := rune(s[i])
	return !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '-' && c != '_'
}
