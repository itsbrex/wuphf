package team

import (
	"strings"
	"testing"
)

// TestEditActionRenamesWithoutMovingStatus pins the verb the Tasks surface
// needs to save a human's edits.
//
// Before this existed the founder's four editable fields were not all
// reachable: Title was read only on create, so renaming a task was impossible
// over the wire, and every verb that REPLACED Details also mutated status —
// while `comment`, the one verb open to everyone, appends, so using it to save
// an edited description duplicated the text on every save.
func TestEditActionRenamesWithoutMovingStatus(t *testing.T) {
	b := newTestBroker(t)
	created, err := b.MutateTask(TaskPostRequest{
		Action: "create", Channel: "team", Title: "Old name",
		Details: "old body", CreatedBy: "human", Owner: "cos",
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	before := created.Task.Status()

	got, err := b.MutateTask(TaskPostRequest{
		Action: "edit", ID: created.Task.ID,
		Title: "New name", Details: "new body", CreatedBy: "human", Channel: "team"})
	if err != nil {
		t.Fatalf("edit failed: %v", err)
	}
	if got.Task.Title != "New name" {
		t.Errorf("title = %q, want New name", got.Task.Title)
	}
	if got.Task.Details != "new body" {
		t.Errorf("details = %q, want new body (replace, not append)", got.Task.Details)
	}
	if strings.Contains(got.Task.Details, "old body") {
		t.Errorf("edit must REPLACE the description, not append to it: %q", got.Task.Details)
	}
	if got.Task.Status() != before {
		t.Errorf("edit must not move status: %q -> %q", before, got.Task.Status())
	}
}

// TestEditActionClearsDescription pins the form-save semantic: an empty details
// string is a real value (the human cleared the box), not "field omitted". The
// generic details block skips empty strings, which is exactly why the edit case
// assigns Details itself.
func TestEditActionClearsDescription(t *testing.T) {
	b := newTestBroker(t)
	created, err := b.MutateTask(TaskPostRequest{
		Action: "create", Channel: "team", Title: "Has a body",
		Details: "delete me", CreatedBy: "human", Owner: "cos",
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	got, err := b.MutateTask(TaskPostRequest{
		Action: "edit", ID: created.Task.ID, Title: "Has a body",
		Details: "", CreatedBy: "human", Channel: "team"})
	if err != nil {
		t.Fatalf("edit failed: %v", err)
	}
	if strings.TrimSpace(got.Task.Details) != "" {
		t.Errorf("clearing the description must stick, got %q", got.Task.Details)
	}
}

// TestEditActionRejectsEmptyTitle — a task must keep a name.
func TestEditActionRejectsEmptyTitle(t *testing.T) {
	b := newTestBroker(t)
	created, err := b.MutateTask(TaskPostRequest{
		Action: "create", Channel: "team", Title: "Keeps its name",
		CreatedBy: "human", Owner: "cos",
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if _, err := b.MutateTask(TaskPostRequest{
		Action: "edit", ID: created.Task.ID, Title: "   ", CreatedBy: "human", Channel: "team"}); err == nil {
		t.Fatal("an empty title must be rejected")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if got := b.findTaskByIDLocked(created.Task.ID); got != nil && strings.TrimSpace(got.Title) == "" {
		t.Error("a rejected edit must not leave the task nameless")
	}
}

// TestEditActionAnnouncesTheChange closes the loop with the human-change
// reporter: a manual edit is news the office needs, so it posts once into the
// channel and tags the owner. Before `edit` existed this reporter could never
// fire for a rename, because no verb could change a title.
func TestEditActionAnnouncesTheChange(t *testing.T) {
	b := newTestBroker(t)
	created, err := b.MutateTask(TaskPostRequest{
		Action: "create", Channel: "team", Title: "Before",
		CreatedBy: "human", Owner: "cos",
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	b.mu.Lock()
	baseline := 0
	for _, m := range b.messages {
		if m.Kind == "task_changed" {
			baseline++
		}
	}
	b.mu.Unlock()

	if _, err := b.MutateTask(TaskPostRequest{
		Action: "edit", ID: created.Task.ID, Title: "After", CreatedBy: "human", Channel: "team"}); err != nil {
		t.Fatalf("edit failed: %v", err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	var announced *channelMessage
	for i := range b.messages {
		if b.messages[i].Kind == "task_changed" {
			announced = &b.messages[i]
		}
	}
	count := 0
	for _, m := range b.messages {
		if m.Kind == "task_changed" {
			count++
		}
	}
	if count != baseline+1 {
		t.Fatalf("want exactly one new task_changed post, got %d new", count-baseline)
	}
	if announced == nil || !strings.Contains(announced.Content, "After") {
		t.Errorf("the announcement must name the new title, got %+v", announced)
	}
}
