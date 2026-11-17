package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExportAndImportBundle(t *testing.T) {
	source := newTestStore(t)
	it := testInterpreter(t, source)
	folder, err := source.CreateFolder("", "运维")
	if err != nil {
		t.Fatal(err)
	}
	sub, err := source.CreateFolder(folder.ID, "数据库")
	if err != nil {
		t.Fatal(err)
	}
	sc, err := source.CreateScript(sub.ID, "备份", "shell", "sh", it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.WriteScript(sc.ID, "echo backup\n"); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(t.TempDir(), "export.zip")
	if err := ExportLibrary(source, target, ExportOptions{ScriptIDs: []string{sc.ID}, IncludeRevisions: true}); err != nil {
		t.Fatal(err)
	}

	manifest, err := InspectBundle(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Scripts) != 1 || manifest.Scripts[0].Name != "备份" {
		t.Fatalf("manifest = %+v", manifest)
	}
	if got := manifest.Scripts[0].Folder; len(got) != 2 || got[0] != "运维" || got[1] != "数据库" {
		t.Fatalf("folder path = %v", got)
	}

	dest := newTestStore(t)
	destIt := testInterpreter(t, dest)
	result, err := ImportLibrary(dest, target, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Imported) != 1 || result.Imported[0] != "备份" {
		t.Fatalf("result = %+v", result)
	}
	var imported Script
	for _, s := range dest.Scripts() {
		if s.Name == "备份" {
			imported = s
		}
	}
	if imported.ID == "" {
		t.Fatal("imported script missing")
	}
	body, err := dest.ReadScript(imported)
	if err != nil || body != "echo backup\n" {
		t.Fatalf("body = %q err=%v", body, err)
	}
	if imported.InterpID != destIt.ID {
		t.Fatalf("interp = %q, want %q", imported.InterpID, destIt.ID)
	}
	path, err := dest.EnsureFolderPath([]string{"运维", "数据库"})
	if err != nil || path == "" {
		t.Fatalf("folder path = %q err=%v", path, err)
	}
	if imported.FolderID != path {
		t.Fatalf("folder = %q, want %q", imported.FolderID, path)
	}
}

func TestImportConflictPolicies(t *testing.T) {
	source := newTestStore(t)
	it := testInterpreter(t, source)
	sc, err := source.CreateScript("", "部署", "shell", "sh", it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.WriteScript(sc.ID, "echo deploy\n"); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "export.zip")
	if err := ExportLibrary(source, target, ExportOptions{ScriptIDs: []string{sc.ID}}); err != nil {
		t.Fatal(err)
	}

	dest := newTestStore(t)
	destIt := testInterpreter(t, dest)
	existing, err := dest.CreateScript("", "部署", "shell", "sh", destIt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dest.WriteScript(existing.ID, "echo local\n"); err != nil {
		t.Fatal(err)
	}

	result, err := ImportLibrary(dest, target, ImportOptions{Conflict: ConflictSkip})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skipped) != 1 || len(result.Imported) != 0 {
		t.Fatalf("skip result = %+v", result)
	}

	result, err = ImportLibrary(dest, target, ImportOptions{Conflict: ConflictCopy})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Imported) != 1 || result.Imported[0] != "部署 (2)" {
		t.Fatalf("copy result = %+v", result)
	}

	result, err = ImportLibrary(dest, target, ImportOptions{Conflict: ConflictOverwrite})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Replaced) != 1 {
		t.Fatalf("overwrite result = %+v", result)
	}
	body, err := dest.ReadScript(existing)
	if err != nil || body != "echo deploy\n" {
		t.Fatalf("body = %q err=%v", body, err)
	}
}

func TestInspectBundleRejectsForeignZip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "other.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("not a zip"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := InspectBundle(path); err == nil {
		t.Fatal("expected an error for a file that is not an export")
	}
}
