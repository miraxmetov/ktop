package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"kube", "kube/clusters", "other"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	for _, file := range []string{"kube/config", "kube/prod.yaml", "kube/staging.yaml", "other/notes.txt"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte("x"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return root
}

func names(options []string, root string) []string {
	out := make([]string, 0, len(options))
	for _, option := range options {
		out = append(out, strings.TrimPrefix(option, root+"/"))
	}
	return out
}

func TestCompleteListsDirectoryContents(t *testing.T) {
	root := tree(t)

	got := names(Complete(filepath.Join(root, "kube")+"/"), root)
	want := []string{"kube/clusters/", "kube/config", "kube/prod.yaml", "kube/staging.yaml"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v (directories first, then alphabetical)", got, want)
		}
	}
}

func TestCompleteFiltersByPrefix(t *testing.T) {
	root := tree(t)

	got := names(Complete(filepath.Join(root, "kube", "pro")), root)
	if len(got) != 1 || got[0] != "kube/prod.yaml" {
		t.Fatalf("got %v", got)
	}

	if got := Complete(filepath.Join(root, "kube", "PRO")); len(got) != 1 {
		t.Fatalf("matching must ignore case, got %v", got)
	}
	if got := Complete(filepath.Join(root, "kube", "zzz")); len(got) != 0 {
		t.Fatalf("no match must return nothing, got %v", got)
	}
}

func TestCompleteMarksDirectories(t *testing.T) {
	root := tree(t)

	for _, option := range Complete(filepath.Join(root, "kube") + "/") {
		if strings.HasSuffix(option, "clusters/") != IsDir(option) {
			t.Errorf("IsDir disagrees with the listing for %q", option)
		}
	}
}

func TestCompleteOnMissingDirectory(t *testing.T) {
	if got := Complete("/definitely/not/here/"); got != nil {
		t.Errorf("unreadable directory must return nothing, got %v", got)
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got := Expand("~/.kube/config"); got != filepath.Join(home, ".kube", "config") {
		t.Errorf("Expand(~) = %q", got)
	}
	if got := Expand("/etc/kube"); got != "/etc/kube" {
		t.Errorf("absolute paths must pass through, got %q", got)
	}
	if got := Expand(""); got != "" {
		t.Errorf("empty stays empty, got %q", got)
	}
}

func TestDefaultPrefersKubeconfigEnv(t *testing.T) {
	t.Setenv("KUBECONFIG", "/tmp/one.yaml:/tmp/two.yaml")
	if got := Default(); got != "/tmp/one.yaml" {
		t.Errorf("Default() = %q, want the first entry of KUBECONFIG", got)
	}

	t.Setenv("KUBECONFIG", "")
	home, _ := os.UserHomeDir()
	if got := Default(); got != filepath.Join(home, ".kube", "config") {
		t.Errorf("Default() = %q", got)
	}
}
