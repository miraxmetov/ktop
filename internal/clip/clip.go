package clip

import (
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func Copy(text string) error {
	if text == "" {
		return errors.New("nothing to copy")
	}

	if err := throughCommand(text); err == nil {
		return nil
	}
	return throughTerminal(text)
}

func throughCommand(text string) error {
	for _, candidate := range commands() {
		path, err := exec.LookPath(candidate[0])
		if err != nil {
			continue
		}

		cmd := exec.Command(path, candidate[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	return errors.New("no clipboard tool on this machine")
}

func commands() [][]string {
	if runtime.GOOS == "darwin" {
		return [][]string{{"pbcopy"}}
	}
	return [][]string{
		{"wl-copy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
	}
}

func throughTerminal(text string) error {
	body := base64.StdEncoding.EncodeToString([]byte(text))
	sequence := "\x1b]52;c;" + body + "\x07"
	if os.Getenv("TMUX") != "" {
		sequence = "\x1bPtmux;\x1b" + sequence + "\x1b\\"
	}

	out, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return errors.New("cannot reach the clipboard from here")
	}
	defer out.Close()

	if _, err := out.WriteString(sequence); err != nil {
		return errors.New("the terminal refused the copy")
	}
	return nil
}
