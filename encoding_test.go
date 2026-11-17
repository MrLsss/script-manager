package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

func TestDecodeAndEncodeEditorText(t *testing.T) {
	original := "echo 你好\r\necho done\r\n"
	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(original))
	if err != nil {
		t.Fatal(err)
	}
	text, enc := decodeEditorText(gbk)
	if strings.Contains(text, "\r") {
		t.Fatalf("editor text still has CR: %q", text)
	}
	if !strings.Contains(text, "你好") {
		t.Fatalf("GBK was not decoded: %q", text)
	}
	if enc.Charset != "gbk" || enc.EOL != "crlf" {
		t.Fatalf("encoding = %+v", enc)
	}
	back, stored := encodeEditorText(text, enc)
	if stored != enc {
		t.Fatalf("encoding changed: %+v", stored)
	}
	if string(back) != string(gbk) {
		t.Fatalf("round trip = %q, want %q", back, gbk)
	}
}

func TestEditorSavesBackAsGBKWithCRLF(t *testing.T) {
	a, store, sc := newTestApp(t)
	if err := store.AddScriptFile(sc.ID, "gbk.sh", ""); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.ScriptDir(sc), "gbk.sh")
	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("echo 你好\r\necho done\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, gbk, 0o644); err != nil {
		t.Fatal(err)
	}
	a.openScriptFile(sc, "gbk.sh")
	st := a.fileEditor(sc, "gbk.sh")
	if !strings.Contains(st.Buffer.String(), "你好") {
		t.Fatalf("buffer = %q", st.Buffer.String())
	}
	st.Buffer.Set("echo 世界\necho ok\n")
	a.saveEditor(sc)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "\r\n") {
		t.Fatalf("CRLF was not preserved: %q", raw)
	}
	decoded, _, err := transform.String(simplifiedchinese.GBK.NewDecoder(), string(raw))
	if err != nil || !strings.Contains(decoded, "世界") {
		t.Fatalf("not GBK or wrong content: %q err=%v", decoded, err)
	}
}
