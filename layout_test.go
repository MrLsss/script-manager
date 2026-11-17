package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSidebarBottomNavIsPinned(t *testing.T) {
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	rect, ok := tt.Find("设置")
	if !ok {
		t.Fatalf("设置 not found:\n%v", tt.Texts())
	}
	if rect.Y < 700 {
		t.Fatalf("设置 sits at y=%.1f, expected near the bottom of a 760 tall window", rect.Y)
	}
	interp, ok := tt.Find("解释器")
	if !ok {
		t.Fatal("解释器 not found")
	}
	if interp.Y > rect.Y {
		t.Fatalf("解释器 y=%.1f above 设置 y=%.1f", interp.Y, rect.Y)
	}
}

func TestSidebarTreeScrollsNotOverflow(t *testing.T) {
	a, store, _ := newTestApp(t)
	for i := 0; i < 40; i++ {
		sc, err := store.CreateScript("", "脚本"+string(rune('A'+i%26))+string(rune('a'+i/26)), "shell", "sh", store.Interpreters()[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.WriteScript(sc.ID, "echo hi\n"); err != nil {
			t.Fatal(err)
		}
	}
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	nav, ok := tt.Find("设置")
	if !ok {
		t.Fatal("设置 not found")
	}
	if nav.Y < 700 {
		t.Fatalf("设置 pushed up to y=%.1f by the tree", nav.Y)
	}
}

func TestScriptPageSplitsSevenToThree(t *testing.T) {
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	rect, ok := tt.Find("还没有运行记录，点右上角「运行」开始")
	if !ok {
		t.Fatalf("run pane header not found:\n%v", tt.Texts())
	}
	// The window content is 760 tall; the run pane starts below the 48 DIP header.
	content := float32(760 - 48)
	ratio := (rect.Y - 48) / content
	if ratio < 0.67 || ratio > 0.74 {
		t.Fatalf("run pane starts at %.0f of %.0f (%.0f%%), expected about 70%%", rect.Y-48, content, ratio*100)
	}
	if a.panelPct != 70 {
		t.Fatalf("panelPct = %.0f, want 70", a.panelPct)
	}
}
