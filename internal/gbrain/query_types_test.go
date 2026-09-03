package gbrain

import (
	"context"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// startArgRecorder stands up a fake `query` tool that records the arguments it
// was called with, so a test can assert on the WIRE shape rather than on a
// return value. The original bug was invisible in return values: gbrain
// answered every call happily, just without filtering.
func startArgRecorder(t *testing.T, got *map[string]any) *Client {
	t.Helper()

	server := mcp.NewServer(&mcp.Implementation{Name: "fake-gbrain", Version: "0.1.0"}, nil)
	record := func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		*got = args
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `[]`}}}, nil, nil
	}
	mcp.AddTool(server, &mcp.Tool{Name: toolQuery, Description: "hybrid search"}, record)

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

// TestQueryTypesSendsPluralTypes pins the parameter NAME.
//
// This is the regression test for a bug that shipped and survived review: the
// filter was sent as `type` (a string), which is list_pages' parameter, not
// query's. MCP drops unknown arguments silently, so the call succeeded and
// returned unfiltered rows — indistinguishable from a server-side filter being
// ignored, which is exactly how it was misdiagnosed. Only the wire arguments
// can catch this.
func TestQueryTypesSendsPluralTypes(t *testing.T) {
	var args map[string]any
	c := startArgRecorder(t, &args)

	if _, err := c.QueryTypes(context.Background(), "who ships retrieval", 5,
		[]string{"person", "company"}); err != nil {
		t.Fatalf("QueryTypes: %v", err)
	}

	if _, bad := args["type"]; bad {
		t.Error(`sent singular "type"; query only honours the plural "types"`)
	}
	raw, ok := args["types"]
	if !ok {
		t.Fatalf(`no "types" argument sent; got %v`, keysOf(args))
	}
	// It must be a LIST. gbrain rejects a non-array `types` outright rather
	// than falling back to an unfiltered search.
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf(`"types" must be a list, got %T (%v)`, raw, raw)
	}
	if want := []any{"person", "company"}; !reflect.DeepEqual(list, want) {
		t.Errorf("types = %v, want %v", list, want)
	}
}

// TestQueryOmitsTypesWhenUnfiltered keeps a plain Query from narrowing results.
func TestQueryOmitsTypesWhenUnfiltered(t *testing.T) {
	var args map[string]any
	c := startArgRecorder(t, &args)

	if _, err := c.Query(context.Background(), "anything", 5); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if _, present := args["types"]; present {
		t.Errorf(`unfiltered Query sent a "types" filter: %v`, args["types"])
	}
}

// TestQueryTypesDropsEmptyFilter guards the same thing for an all-blank slice:
// sending types:[] would match nothing rather than everything.
func TestQueryTypesDropsEmptyFilter(t *testing.T) {
	var args map[string]any
	c := startArgRecorder(t, &args)

	if _, err := c.QueryTypes(context.Background(), "anything", 5, []string{"", "  "}); err != nil {
		t.Fatalf("QueryTypes: %v", err)
	}
	if _, present := args["types"]; present {
		t.Errorf(`blank-only filter still sent "types": %v`, args["types"])
	}
}

func TestCleanTypes(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"trims", []string{" person ", "company"}, []string{"person", "company"}},
		{"drops blanks", []string{"person", "", "  "}, []string{"person"}},
		{"dedupes preserving order", []string{"b", "a", "b"}, []string{"b", "a"}},
		{"nil stays empty", nil, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cleanTypes(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("cleanTypes(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
