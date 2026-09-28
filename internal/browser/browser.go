package browser

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

func Open(url string) error {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return errors.New("only http and https links are opened")
	}

	for _, name := range openers() {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if err := exec.Command(path, url).Start(); err == nil {
			return nil
		}
	}
	return errors.New("no way to open a browser from here")
}

func openers() []string {
	if runtime.GOOS == "darwin" {
		return []string{"open"}
	}
	return []string{"xdg-open", "gio", "gnome-open", "wslview"}
}
