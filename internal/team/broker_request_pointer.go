package team

import (
	"fmt"
	"strings"
	"time"
)

// postRequestPointerToDelegatorLocked keeps the human informed without
// letting a third party into a DM. When agent B raises a request on work
// that agent A delegated to it, the card lives in B's own DM with the human
// (requestChannelForLocked), but the human is usually still sitting in A's
// DM where they asked. A one-line system pointer there says who needs a
// decision and where — a system note, not B speaking in A's thread. Human
// eval, 2026-09-03: the Designer's approval sat unseen for nine minutes.
func (b *Broker) postRequestPointerToDelegatorLocked(req *humanInterview, cardChannel string) {
	if req == nil || strings.TrimSpace(req.IssueID) == "" {
		return
	}
	task := b.findTaskByIDLocked(req.IssueID)
	if task == nil {
		return
	}
	delegator := normalizeActorSlug(task.CreatedBy)
	from := normalizeActorSlug(req.From)
	if delegator == "" || delegator == from || isHumanMessageSender(delegator) || b.findMemberLocked(delegator) == nil {
		return
	}
	delegatorDM := DMSlugFor(delegator)
	if normalizeChannelSlug(delegatorDM) == normalizeChannelSlug(cardChannel) {
		return
	}
	if b.findChannelLocked(delegatorDM) == nil {
		if dm := b.ensureDMConversationLocked(delegatorDM); dm != nil {
			delegatorDM = dm.Slug
		} else {
			return
		}
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = strings.TrimSpace(task.Title)
	}
	b.counter++
	b.appendMessageLocked(channelMessage{
		ID:        fmt.Sprintf("msg-%d", b.counter),
		From:      "system",
		Channel:   delegatorDM,
		Kind:      "human_request_pointer",
		Title:     title,
		Content:   fmt.Sprintf("❓ @%s needs your go-ahead on %s (%s) — [open their chat to decide](#/agents/%s)", from, title, task.ID, from),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}
