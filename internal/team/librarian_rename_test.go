package team

import "testing"

// TestLibrarianNameReconciledOnLoad pins that a built-in's display name is
// owned by the code rather than by whatever is saved on disk.
//
// Renaming the Librarian to "Pam the librarian" changed only the constant, so
// every office already on disk kept showing "Pam". A rename that reaches only
// brand-new workspaces reaches nobody who is already using the product.
func TestLibrarianNameReconciledOnLoad(t *testing.T) {
	b := newTestBroker(t)
	b.mu.Lock()
	b.members = []officeMember{
		{Slug: "ceo", Name: "CEO", Role: "lead"},
		{Slug: LibrarianSlug, Name: "Pam", Role: "Librarian"}, // the stale saved name
		{Slug: "designer", Name: "Designer", Role: "Design"},
	}
	b.normalizeLoadedStateLocked()
	var librarian, designer *officeMember
	for i := range b.members {
		if isLibrarianSlug(b.members[i].Slug) {
			librarian = &b.members[i]
		}
		if b.members[i].Slug == "designer" {
			designer = &b.members[i]
		}
	}
	b.mu.Unlock()

	if librarian == nil {
		t.Fatal("librarian missing after load")
	}
	if librarian.Name != librarianName {
		t.Errorf("librarian name = %q, want %q — a built-in's name comes from the code, not the saved roster", librarian.Name, librarianName)
	}
	// A normal member's name is the user's to set, and must NOT be rewritten.
	if designer == nil || designer.Name != "Designer" {
		t.Errorf("a non-built-in member's name must be left alone, got %+v", designer)
	}
}
