package gbrain

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// startPagingServer serves `count` synthetic pages through list_pages,
// honouring `limit` and `offset` the way gbrain >= 0.48 does. It records how
// many calls were made so a test can catch a loop that never advances.
func startPagingServer(t *testing.T, count int, calls *int) *Client {
	t.Helper()

	server := mcp.NewServer(&mcp.Implementation{Name: "fake-gbrain", Version: "0.1.0"}, nil)
	handler := func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		*calls++
		limit, offset := 100, 0
		if v, ok := args["limit"].(float64); ok {
			limit = int(v)
		}
		if v, ok := args["offset"].(float64); ok {
			offset = int(v)
		}
		rows := []map[string]any{}
		for i := offset; i < count && len(rows) < limit; i++ {
			rows = append(rows, map[string]any{
				"slug":       fmt.Sprintf("entities/p%03d", i),
				"title":      fmt.Sprintf("Page %d", i),
				"type":       "person",
				"updated_at": "2026-09-01T00:00:00.000Z",
			})
		}
		body, err := json.Marshal(rows)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(body)}}}, nil, nil
	}
	mcp.AddTool(server, &mcp.Tool{Name: toolListPages, Description: "list"}, handler)

	clientTr, serverTr := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = server.Run(ctx, serverTr) }()

	rawClient := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := rawClient.Connect(ctx, clientTr, nil)
	if err != nil {
		t.Fatalf("connect fake: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	c := NewClient(WithRemoteURL("inmemory://fake"))
	c.session = session
	return c
}

// TestListAllPagesOffset walks past the page size and terminates.
//
// Every synthetic row shares ONE updated_at. That is deliberate: it is exactly
// the tie cluster the updated_after cursor cannot walk, and it demonstrates why
// offset is the better path where it is supported.
func TestListAllPagesOffset(t *testing.T) {
	cases := []struct {
		name      string
		count     int
		limit     int
		wantCalls int
	}{
		{"exact multiple needs a final short read", 20, 10, 3},
		{"partial last page", 25, 10, 3},
		{"single short page", 4, 10, 1},
		{"empty brain", 0, 10, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := startPagingServer(t, tc.count, &calls)

			got, err := c.listAllPagesOffset(context.Background(), ListPageOptions{Limit: tc.limit})
			if err != nil {
				t.Fatalf("listAllPagesOffset: %v", err)
			}
			if len(got) != tc.count {
				t.Errorf("returned %d pages, want %d", len(got), tc.count)
			}
			if calls != tc.wantCalls {
				t.Errorf("made %d list_pages calls, want %d", calls, tc.wantCalls)
			}
			// Every slug distinct and present: offset paging must not repeat or
			// skip a row.
			seen := map[string]bool{}
			for _, p := range got {
				if seen[p.Slug] {
					t.Errorf("duplicate slug %q", p.Slug)
				}
				seen[p.Slug] = true
			}
			for i := 0; i < tc.count; i++ {
				if want := fmt.Sprintf("entities/p%03d", i); !seen[want] {
					t.Errorf("missing slug %q", want)
				}
			}
		})
	}
}

// TestListAllPagesOffsetSendsStableSort pins the ordering argument.
//
// Offset paging enumerates each row once only under a stable order. gbrain
// defaults to updated_desc, which reshuffles as pages are written, so a
// concurrent write during a scan would make offset skip or repeat rows.
func TestListAllPagesOffsetSendsStableSort(t *testing.T) {
	var args map[string]any
	server := mcp.NewServer(&mcp.Implementation{Name: "fake-gbrain", Version: "0.1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: toolListPages, Description: "list"},
		func(_ context.Context, _ *mcp.CallToolRequest, a map[string]any) (*mcp.CallToolResult, any, error) {
			args = a
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `[]`}}}, nil, nil
		})

	clientTr, serverTr := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = server.Run(ctx, serverTr) }()
	rawClient := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := rawClient.Connect(ctx, clientTr, nil)
	if err != nil {
		t.Fatalf("connect fake: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	c := NewClient(WithRemoteURL("inmemory://fake"))
	c.session = session

	if _, err := c.listAllPagesOffset(context.Background(), ListPageOptions{Limit: 10}); err != nil {
		t.Fatalf("listAllPagesOffset: %v", err)
	}
	if got := args["sort"]; got != "slug" {
		t.Errorf(`sort = %v, want "slug" (a stable order for offset paging)`, got)
	}
	// The first call must not send offset:0 as a no-op argument on a version
	// that would reject unknown/zero values differently.
	if _, present := args["offset"]; present {
		t.Errorf("first page sent an offset argument: %v", args["offset"])
	}
}
