package main

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestDeleteRevisionRemovesOnlyThatSnapshot(t *testing.T) {
	store := newTestStore(t)
	it := testInterpreter(t, store)
	sc, err := store.CreateScript("", "删除版本", "shell", "sh", it.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"echo one\n", "echo two\n", "echo three\n"} {
		if _, err := store.WriteScript(sc.ID, body); err != nil {
			t.Fatal(err)
		}
		if _, err := store.AddRevision(sc.ID); err != nil {
			t.Fatal(err)
		}
	}
	revs := store.RevisionsOf(sc.ID)
	if len(revs) != 4 {
		t.Fatalf("revisions = %d", len(revs))
	}
	victim := revs[1]
	if err := store.DeleteRevision(sc.ID, victim.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	left := store.RevisionsOf(sc.ID)
	if len(left) != 3 {
		t.Fatalf("revisions after delete = %d", len(left))
	}
	for _, rev := range left {
		if rev.ID == victim.ID {
			t.Fatalf("deleted revision still listed")
		}
	}
	content, err := store.ReadScript(sc)
	if err != nil {
		t.Fatal(err)
	}
	if content != "echo three\n" {
		t.Fatalf("script content changed: %q", content)
	}
	if err := store.DeleteRevision(sc.ID, "rev-missing"); err == nil {
		t.Fatalf("deleting a missing revision succeeded")
	}
}

func TestHistoryDeleteButtonRemovesRevision(t *testing.T) {
	a, store, sc := newTestApp(t)
	for _, body := range []string{"echo hello\n", "echo hello again\n"} {
		if _, err := store.WriteScript(sc.ID, body); err != nil {
			t.Fatal(err)
		}
		if _, err := store.AddRevision(sc.ID); err != nil {
			t.Fatal(err)
		}
	}
	before := len(store.RevisionsOf(sc.ID))
	a.openRevisions(sc)
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Advance(600 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("全部版本") {
		t.Fatalf("history page missing:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	revs := store.RevisionsOf(sc.ID)
	a.deleteRevision(sc, revs[0])
	tt.Frame()
	if !tt.HasText("删除这份历史版本") {
		t.Fatalf("confirm dialog missing:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	if err := tt.Click("删除"); err != nil {
		t.Fatalf("click 删除: %v", err)
	}
	tt.Frame()
	after := len(store.RevisionsOf(sc.ID))
	if after != before-1 {
		t.Fatalf("revisions %d -> %d", before, after)
	}
}
