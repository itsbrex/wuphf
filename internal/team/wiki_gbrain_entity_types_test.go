package team

import (
	"sort"
	"testing"
)

// TestEntityPageTypesMatchesKindMapping keeps the retrieval filter in sync with
// the writer.
//
// entityPageTypes is passed to gbrain as a `types` filter, so it is applied at
// the SQL level: a type an entity page can be WRITTEN with but that is missing
// from this list makes those entities silently unreachable by search. The two
// declarations are in different files, so nothing but this test couples them.
func TestEntityPageTypesMatchesKindMapping(t *testing.T) {
	// Every kind the extractor can produce, including the ones that fall
	// through to the "concept" default.
	kinds := []string{"person", "company", "project", "team", "workspace", "", "  ", "InVeNtEd-LaTeR"}

	allowed := map[string]bool{}
	for _, ty := range entityPageTypes {
		allowed[ty] = true
	}
	for _, kind := range kinds {
		if ty := entityKindToPageType(kind); !allowed[ty] {
			t.Errorf("kind %q writes page type %q, which entityPageTypes omits: "+
				"those entities would be filtered out of every search", kind, ty)
		}
	}

	// And no dead entries: a type in the filter that nothing writes silently
	// widens the filter.
	written := map[string]bool{}
	for _, kind := range kinds {
		written[entityKindToPageType(kind)] = true
	}
	for _, ty := range entityPageTypes {
		if !written[ty] {
			t.Errorf("entityPageTypes lists %q but no entity kind writes it", ty)
		}
	}
}

// TestCategoryPageTypeIsNotAnEntityType is the point of the category type.
//
// Categories were typed "concept", which is ALSO the fallback type for
// concept-kind entities (team, workspace, anything invented later). Sharing one
// type makes the server-side filter unable to separate them, and a category
// page then competes with real entities for slots — verified live, where a
// category page took the #1 result slot ahead of both matching entities.
func TestCategoryPageTypeIsNotAnEntityType(t *testing.T) {
	for _, ty := range entityPageTypes {
		if ty == entityCategoryPageType {
			t.Fatalf("category page type %q collides with an entity page type; "+
				"the types filter cannot exclude category pages", entityCategoryPageType)
		}
	}
}

// TestEntityPageTypesIsDeterministic guards the value sent on the wire.
func TestEntityPageTypesIsDeterministic(t *testing.T) {
	got := append([]string(nil), entityPageTypes...)
	sort.Strings(got)
	want := []string{"company", "concept", "person", "project"}
	if len(got) != len(want) {
		t.Fatalf("entityPageTypes = %v, want the four types %v", entityPageTypes, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entityPageTypes = %v, want %v", entityPageTypes, want)
		}
	}
}
