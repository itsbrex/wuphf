package team

import "testing"

func TestProgressDetailHidesImagePayloads(t *testing.T) {
	cases := map[string]string{
		`[{"source":{"data":"iVBORw0KGgoAAAANSUhEUgAA","media_type":"image/png"}}]`: "looked at the screen (image result)",
		`data:image/jpeg;base64,/9j/4AAQSkZJRg`:                                     "looked at the screen (image result)",
		`{"active":true,"windows":[]}`:                                              `{"active":true,"windows":[]}`,
	}
	for in, want := range cases {
		if got := progressDetail(in, 140); got != want {
			t.Fatalf("progressDetail(%q) = %q, want %q", in, got, want)
		}
	}
	long := "x" + string(make([]byte, 300))
	if got := progressDetail(long, 140); len(got) > 145 {
		t.Fatalf("long text must still be truncated, got %d bytes", len(got))
	}
}
