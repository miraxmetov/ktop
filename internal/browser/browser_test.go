package browser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenHandsTheLinkToTheDesktop(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "opened")
	script := "#!/bin/sh\necho \"$1\" > " + out + "\n"

	for _, name := range openers() {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := Open("https://shop.example.com/api"); err != nil {
		t.Fatalf("Open: %v", err)
	}

	body := ""
	for i := 0; i < 100 && body == ""; i++ {
		if raw, err := os.ReadFile(out); err == nil {
			body = strings.TrimSpace(string(raw))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if body != "https://shop.example.com/api" {
		t.Errorf("opened %q", body)
	}
}

func TestOpenRefusesWhatIsNotALink(t *testing.T) {
	if err := Open("file:///etc/passwd"); err == nil {
		t.Error("only http links may be opened")
	}
}
