package clip

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyPrefersTheClipboardTool(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "copied")

	script := "#!/bin/sh\ncat > " + out + "\n"
	name := commands()[0][0]
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := Copy("api-worker-1"); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(body) != "api-worker-1" {
		t.Errorf("copied %q", body)
	}
}

func TestCopyRefusesNothing(t *testing.T) {
	if err := Copy(""); err == nil {
		t.Error("an empty copy must say so")
	}
}
