package team

import (
	"strings"
	"testing"
)

// TestTaskDeliveredLineSaysItOnce pins the copy fix. A task with no Definition
// falls back to its title for the summary, so the old template rendered
// "Canary smoke-check task delivered: Canary smoke-check task" — the same
// sentence twice, which is what shipped to the founder's screen.
func TestTaskDeliveredLineSaysItOnce(t *testing.T) {
	line := taskDeliveredContentLine(&teamTask{ID: "DUNDE-95", Title: "Canary smoke-check task"})
	if got := strings.Count(line, "Canary smoke-check task"); got != 1 {
		t.Fatalf("title must appear exactly once, appeared %d times: %q", got, line)
	}
	if !strings.Contains(line, "delivered") {
		t.Fatalf("line must still say delivered: %q", line)
	}

	// A real summary (a success criterion) is genuinely new information and
	// must still be shown alongside the title.
	withDef := taskDeliveredContentLine(&teamTask{
		ID: "DUNDE-96", Title: "Ship the header",
		Definition: &TaskDefinition{SuccessCriteria: []string{"header renders in all three themes"}},
	})
	if !strings.Contains(withDef, "Ship the header") || !strings.Contains(withDef, "header renders in all three themes") {
		t.Fatalf("a distinct summary must survive: %q", withDef)
	}
}

// TestTaskDeliveredRaisesNoAcknowledgeCard pins the ceremony removal: landing a
// task posts exactly one chat message and zero human-decision cards.
func TestTaskDeliveredRaisesNoAcknowledgeCard(t *testing.T) {
	b := newTestBroker(t)
	b.mu.Lock()
	before := len(b.requests)
	task := &teamTask{ID: "DUNDE-97", Title: "Land the thing", Channel: "general", Owner: "eng", CompletedAt: "2026-08-22T00:00:00Z"}
	b.postTaskDeliveredLocked(task)
	posts, cards := 0, len(b.requests)-before
	for _, m := range b.messages {
		if m.Kind == taskDeliveredMessageKind && m.SourceTaskID == task.ID {
			posts++
		}
	}
	b.mu.Unlock()

	if posts != 1 {
		t.Errorf("want exactly 1 task_delivered chat post, got %d", posts)
	}
	if cards != 0 {
		t.Errorf("a delivery must raise no human-decision card, got %d", cards)
	}
}
