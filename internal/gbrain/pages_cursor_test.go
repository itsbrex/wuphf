package gbrain

import "testing"

// TestPenultimateTimestamp covers the cursor-advance rule that keeps
// ListAllPages from dropping rows at a batch boundary.
//
// `updated_after` is strictly greater-than, so advancing to the batch MAXIMUM
// would skip any rows sharing that timestamp which did not fit in the batch.
// The cursor advances to the second-largest distinct timestamp instead, so the
// boundary rows are re-fetched and deduplicated.
func TestPenultimateTimestamp(t *testing.T) {
	mk := func(ts ...string) []PageMeta {
		out := make([]PageMeta, 0, len(ts))
		for i, v := range ts {
			out = append(out, PageMeta{Slug: string(rune('a' + i)), Updated: v})
		}
		return out
	}

	t.Run("returns second-largest distinct", func(t *testing.T) {
		got, ok := penultimateTimestamp(mk(
			"2026-08-30T10:00:00Z", "2026-08-30T12:00:00Z", "2026-08-30T11:00:00Z"))
		if !ok || got != "2026-08-30T11:00:00Z" {
			t.Errorf("got (%q,%v), want (2026-08-30T11:00:00Z,true)", got, ok)
		}
	})

	t.Run("duplicates at the max collapse to one distinct value", func(t *testing.T) {
		// Three rows, two share the max. Distinct set is {10,12}, so the cursor
		// is 10 and the two rows at 12 are re-fetched rather than skipped.
		got, ok := penultimateTimestamp(mk(
			"2026-08-30T12:00:00Z", "2026-08-30T12:00:00Z", "2026-08-30T10:00:00Z"))
		if !ok || got != "2026-08-30T10:00:00Z" {
			t.Errorf("got (%q,%v), want (2026-08-30T10:00:00Z,true)", got, ok)
		}
	})

	t.Run("a single distinct timestamp cannot advance", func(t *testing.T) {
		// This is the unrepresentable case: a full batch where every row shares
		// one timestamp. Any cursor either loops forever or drops rows, so the
		// caller must surface an error instead.
		if _, ok := penultimateTimestamp(mk(
			"2026-08-30T12:00:00Z", "2026-08-30T12:00:00Z")); ok {
			t.Error("expected ok=false for a single-distinct-timestamp batch")
		}
	})

	t.Run("empty batch cannot advance", func(t *testing.T) {
		if _, ok := penultimateTimestamp(nil); ok {
			t.Error("expected ok=false for an empty batch")
		}
	})

	t.Run("rows with no timestamp are ignored", func(t *testing.T) {
		if _, ok := penultimateTimestamp(mk("", "")); ok {
			t.Error("expected ok=false when no row carries a timestamp")
		}
	})
}

// TestPageMetaAcceptsBothUpdatedKeys pins the decoding fix: gbrain emits
// `updated_at`, the struct originally declared only `updated`, and the field
// silently decoded empty — which blanked LastEditedTs everywhere and left the
// pagination cursor with nothing to advance on.
func TestPageMetaAcceptsBothUpdatedKeys(t *testing.T) {
	cases := map[string]string{
		`{"slug":"a","updated_at":"2026-08-30T12:00:00Z"}`:                    "2026-08-30T12:00:00Z",
		`{"slug":"a","updated":"2026-08-30T11:00:00Z"}`:                       "2026-08-30T11:00:00Z",
		`{"slug":"a","updated_at":"2026-08-30T12:00:00Z","updated":"legacy"}`: "2026-08-30T12:00:00Z",
		`{"slug":"a"}`: "",
	}
	for body, want := range cases {
		var p PageMeta
		if err := decodeJSON(body, &p); err != nil {
			t.Fatalf("decode %s: %v", body, err)
		}
		if p.Updated != want {
			t.Errorf("decode %s -> Updated=%q, want %q", body, p.Updated, want)
		}
		if p.Slug != "a" {
			t.Errorf("decode %s lost Slug: %q", body, p.Slug)
		}
	}
}
