package paths

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxOptions = 200

func Expand(path string) string {
	if path == "" {
		return ""
	}
	if path == "~" {
		return home()
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home(), path[2:])
	}
	return path
}

func home() string {
	dir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return dir
}

func Default() string {
	if env := os.Getenv("KUBECONFIG"); env != "" {
		parts := strings.Split(env, string(os.PathListSeparator))
		if len(parts) > 0 && parts[0] != "" {
			return parts[0]
		}
	}
	return filepath.Join(home(), ".kube", "config")
}

func Complete(query string) []string {
	expanded := Expand(query)

	dir, prefix := filepath.Dir(expanded), filepath.Base(expanded)
	switch {
	case expanded == "":
		dir, prefix = startDir(), ""
	case strings.HasSuffix(query, string(os.PathSeparator)):
		dir, prefix = strings.TrimSuffix(expanded, string(os.PathSeparator)), ""
		if dir == "" {
			dir = string(os.PathSeparator)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	lower := strings.ToLower(prefix)
	options := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if prefix != "" && !strings.HasPrefix(strings.ToLower(name), lower) {
			continue
		}
		if prefix == "" && strings.HasPrefix(name, ".") && !strings.HasPrefix(dir, home()+"/.") {
			if name != ".kube" {
				continue
			}
		}
		full := filepath.Join(dir, name)
		if isDir(entry, full) {
			full += string(os.PathSeparator)
		}
		options = append(options, full)
		if len(options) >= maxOptions {
			break
		}
	}

	sort.Slice(options, func(i, j int) bool {
		a, b := options[i], options[j]
		aDir, bDir := strings.HasSuffix(a, string(os.PathSeparator)), strings.HasSuffix(b, string(os.PathSeparator))
		if aDir != bDir {
			return aDir
		}
		return a < b
	})
	return options
}

func startDir() string {
	kube := filepath.Join(home(), ".kube")
	if info, err := os.Stat(kube); err == nil && info.IsDir() {
		return kube
	}
	return home()
}

func isDir(entry os.DirEntry, full string) bool {
	if entry.IsDir() {
		return true
	}
	if entry.Type()&os.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(full)
	return err == nil && info.IsDir()
}

func IsDir(path string) bool {
	return strings.HasSuffix(path, string(os.PathSeparator))
}
