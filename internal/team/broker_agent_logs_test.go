package team

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// newTestBroker returns a Broker whose state file is pinned under
// t.TempDir(). Use this for tests that don't care about the exact
// state path — only that the broker writes to an isolated location.
// For tests that also need the path itself (persistence, reload), call
// NewBrokerAt(filepath.Join(tmpDir, "broker-state.json")) directly so
// the tmpDir is in scope.
func newTestBroker(t *testing.T) *Broker {
	t.Helper()
	b := NewBrokerAt(filepath.Join(t.TempDir(), "broker-state.json"))
	seedTestTeamRoom(b)
	return b
}

// testTeamRoom is the shared room tests post to when the thing under test is
// NOT the DM model itself -- mention routing, message scoping, task plumbing.
//
// It used to be #general, which every test leaned on because it existed and
// held the whole roster. #general is retired, and a DM is the wrong
// substitute: a DM has exactly two members, so an agent posting into another
// agent's DM is correctly refused with "channel access denied". That refusal
// is the DM privacy model working, not a broken fixture.
//
// So tests that need a room where several agents can speak get a NAMED
// channel, which is still a real product surface. Tests that are about DMs
// use DMs.
const testTeamRoom = "team"

// newBrokerWithTeamRoom is NewBrokerAt plus the shared test room. Tests call
// this instead of NewBrokerAt directly so that every test broker has a room
// several agents can speak in, which is what #general used to provide for
// free. Idempotent.
func newBrokerWithTeamRoom(path string) *Broker {
	b := NewBrokerAt(path)
	seedTestTeamRoom(b)
	return b
}

func seedTestTeamRoom(b *Broker) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.findChannelLocked(testTeamRoom) != nil {
		return
	}
	members := make([]string, 0, len(b.members)+1)
	members = append(members, "human")
	for _, m := range b.members {
		members = append(members, m.Slug)
	}
	b.channels = append(b.channels, teamChannel{
		Slug:        testTeamRoom,
		Name:        testTeamRoom,
		Type:        "channel",
		Description: "Shared room for tests that are not about DMs",
		Members:     members,
	})
}

func TestHandleAgentLogs_ListsRecent(t *testing.T) {
	logRoot := t.TempDir()
	taskDir := filepath.Join(logRoot, "eng-12345")
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "output.log"),
		[]byte(`{"tool_name":"grep_search","agent_slug":"eng","started_at":1700000000000}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	b := newTestBroker(t)
	b.SetAgentLogRoot(logRoot)
	srv := httptest.NewServer(b.requireAuth(b.handleAgentLogs))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("Authorization", "Bearer "+b.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d, body %s", resp.StatusCode, string(body))
	}

	var payload struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(payload.Tasks))
	}
	if payload.Tasks[0]["taskId"] != "eng-12345" {
		t.Fatalf("unexpected taskId: %v", payload.Tasks[0]["taskId"])
	}
}

func TestHandleAgentLogs_ReadsSingleTask(t *testing.T) {
	logRoot := t.TempDir()
	taskDir := filepath.Join(logRoot, "eng-12345")
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "output.log"),
		[]byte(`{"tool_name":"grep_search","agent_slug":"eng"}`+"\n"+
			`{"tool_name":"write_file","agent_slug":"eng"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	b := newTestBroker(t)
	b.SetAgentLogRoot(logRoot)
	srv := httptest.NewServer(b.requireAuth(b.handleAgentLogs))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"?task=eng-12345", nil)
	req.Header.Set("Authorization", "Bearer "+b.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d, body %s", resp.StatusCode, string(body))
	}

	var payload struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(payload.Entries))
	}
}

func TestHandleAgentLogs_RejectsPathTraversal(t *testing.T) {
	b := newTestBroker(t)
	b.SetAgentLogRoot(t.TempDir())
	srv := httptest.NewServer(b.requireAuth(b.handleAgentLogs))
	defer srv.Close()

	for _, bad := range []string{"../etc/passwd", "eng/../../../etc/passwd", "a/b"} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"?task="+bad, nil)
		req.Header.Set("Authorization", "Bearer "+b.Token())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request %q: %v", bad, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("task=%q: expected 400, got %d", bad, resp.StatusCode)
		}
	}
}
