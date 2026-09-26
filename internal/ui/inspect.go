package ui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
)

type Format int

const (
	FormatDefault Format = iota
	FormatTextual
	FormatYAML
)

type LogLine struct {
	Time string
	Text string
}

type Inspection struct {
	Pod       string
	Container string
	Default   []string
	Textual   []string
	Yaml      []string
	Format    Format
	Offset    int
	Err       string
	Loading   bool

	Logs      []LogLine
	LogQuery  string
	LogErr    string
	LogSearch bool
}

func (i *Inspection) Lines() []string {
	switch i.Format {
	case FormatTextual:
		return i.Textual
	case FormatYAML:
		return i.Yaml
	}
	return i.Default
}

func (i *Inspection) VisibleLogs() []LogLine {
	query := strings.ToLower(strings.TrimSpace(i.LogQuery))
	if query == "" {
		return i.Logs
	}
	out := make([]LogLine, 0, len(i.Logs))
	for _, line := range i.Logs {
		if strings.Contains(strings.ToLower(line.Text), query) {
			out = append(out, line)
		}
	}
	return out
}

const (
	inspectTop     = 2
	defaultLabel   = "[ default ]"
	textualLabel   = "[ textual ]"
	yamlLabel      = "[ yaml ]"
	logPlaceholder = "Search log stream..."
)

func InspectRoom(width, height int) int {
	return inspectGeom(width, height).room
}

func MaxInspectOffset(m Model, width, height int) int {
	g := inspectGeom(width, height)
	hidden := len(wrap(m.Inspect.Lines(), g.textArea)) - g.room
	if hidden < 0 {
		return 0
	}
	return hidden
}

type inspectGeometry struct {
	left     rect
	right    rect
	logs     rect
	search   rect
	dflt     rect
	textual  rect
	yaml     rect
	room     int
	logRoom  int
	textArea int
}

func inspectGeom(width, height int) inspectGeometry {
	paneTop := inspectTop
	paneHeight := height - paneTop - 2
	if paneHeight < 5 {
		paneHeight = 5
	}

	leftWidth := width / 2
	if leftWidth < 20 {
		leftWidth = 20
	}
	rightWidth := width - leftWidth - 1
	if rightWidth < 10 {
		rightWidth = 10
	}

	left := rect{x: 0, y: paneTop, w: leftWidth, h: paneHeight}
	right := rect{x: leftWidth + 1, y: paneTop, w: rightWidth, h: paneHeight}

	logRoom := paneHeight - 4
	if logRoom < 1 {
		logRoom = 1
	}

	buttons := len(defaultLabel) + 2 + len(textualLabel) + 2 + len(yamlLabel)
	buttonsX := max(0, (width-buttons)/2)
	buttonsY := height - 2

	return inspectGeometry{
		left:     left,
		right:    right,
		logs:     rect{x: right.x + 2, y: right.y + 1, w: right.w - 4, h: logRoom},
		search:   rect{x: right.x + 2, y: right.y + right.h - 2, w: right.w - 4, h: 1},
		dflt:     rect{x: buttonsX, y: buttonsY, w: len(defaultLabel), h: 1},
		textual:  rect{x: buttonsX + len(defaultLabel) + 2, y: buttonsY, w: len(textualLabel), h: 1},
		yaml:     rect{x: buttonsX + len(defaultLabel) + 2 + len(textualLabel) + 2, y: buttonsY, w: len(yamlLabel), h: 1},
		room:     paneHeight - 2,
		logRoom:  logRoom,
		textArea: left.w - 4,
	}
}

func drawPane(s tcell.Screen, box rect, title string) {
	line := repeat("─", box.w-2)
	puts(s, box.x, box.y, 0, false, "┌"+line+"┐", styleChoice)
	puts(s, box.x, box.y+box.h-1, 0, false, "└"+line+"┘", styleChoice)
	for y := box.y + 1; y < box.y+box.h-1; y++ {
		puts(s, box.x, y, 0, false, "│", styleChoice)
		puts(s, box.x+box.w-1, y, 0, false, "│", styleChoice)
	}
	if title != "" && box.w > len(title)+6 {
		puts(s, box.x+2, box.y, 0, false, " "+title+" ", styleDim)
	}
}

func wrap(lines []string, width int) []string {
	if width < 10 {
		width = 10
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		runes := []rune(line)
		if len(runes) <= width {
			out = append(out, line)
			continue
		}

		indent := ""
		if trimmed := strings.TrimLeft(line, " "); len(line)-len(trimmed) > 0 {
			indent = line[:len(line)-len(trimmed)]
		}
		current := ""
		for _, word := range strings.Fields(line) {
			candidate := word
			if current != "" {
				candidate = current + " " + word
			}
			if len([]rune(indent+candidate)) > width && current != "" {
				out = append(out, indent+current)
				current = word
				continue
			}
			current = candidate
		}
		if current != "" {
			out = append(out, indent+current)
		}
	}
	return out
}

func drawInspect(s tcell.Screen, m Model) {
	width, height := s.Size()
	g := inspectGeom(width, height)

	title := m.Inspect.Pod
	if title == "" {
		title = "pod"
	}
	puts(s, max(0, (width-len([]rune(title)))/2), 0, 0, false, title, styleAccent.Bold(true))

	drawPane(s, g.left, "config")
	drawPane(s, g.right, "logs"+containerSuffix(m))
	drawLogSeparator(s, g)

	drawConfig(s, m, g)
	drawLogs(s, m, g)

	puts(s, g.dflt.x, g.dflt.y, 0, false, defaultLabel, pickStyle(styleChoice, m.Inspect.Format == FormatDefault))
	puts(s, g.textual.x, g.textual.y, 0, false, textualLabel, pickStyle(styleChoice, m.Inspect.Format == FormatTextual))
	puts(s, g.yaml.x, g.yaml.y, 0, false, yamlLabel, pickStyle(styleChoice, m.Inspect.Format == FormatYAML))

	footer := "[" + arrowUp + arrowDown + "] scroll   [/] search logs   [Tab] switch format   [Esc] back to the table"
	if m.Inspect.LogSearch {
		footer = "type to filter the stream   [Esc] leave the search"
	}
	puts(s, max(0, (width-len([]rune(footer)))/2), height-1, 0, false, footer, styleDim)
	s.Show()
}

func containerSuffix(m Model) string {
	if m.Inspect.Container == "" {
		return ""
	}
	return ": " + m.Inspect.Container
}

func drawLogSeparator(s tcell.Screen, g inspectGeometry) {
	y := g.right.y + g.right.h - 3
	puts(s, g.right.x, y, 0, false, "├"+repeat("─", g.right.w-2)+"┤", styleChoice)
}

func drawConfig(s tcell.Screen, m Model, g inspectGeometry) {
	x, top := g.left.x+2, g.left.y+1

	switch {
	case m.Inspect.Err != "":
		puts(s, x, top, g.textArea, false, truncate(m.Inspect.Err, g.textArea), styleBad)
		return
	case m.Inspect.Loading:
		puts(s, x, top, g.textArea, false, "loading "+m.Inspect.Pod+"...", styleDim)
		return
	}

	lines := wrap(m.Inspect.Lines(), g.textArea)
	offset := m.Inspect.Offset
	if offset > len(lines)-g.room {
		offset = len(lines) - g.room
	}
	if offset < 0 {
		offset = 0
	}
	for i := 0; i < g.room && offset+i < len(lines); i++ {
		line := lines[offset+i]
		putSegments(s, x, top+i, g.textArea, configSegments(line, m.Inspect.Format))
	}

	if above := offset; above > 0 {
		puts(s, g.left.x+2, g.left.y, 0, false, " \u2191"+itoa(above)+" more ", styleDim)
	}
	if below := len(lines) - offset - g.room; below > 0 {
		puts(s, g.left.x+2, g.left.y+g.left.h-1, 0, false, " \u2193"+itoa(below)+" more ", styleDim)
	}
}

func drawLogs(s tcell.Screen, m Model, g inspectGeometry) {
	x, top := g.logs.x, g.logs.y

	if m.Inspect.LogErr != "" {
		puts(s, x, top, g.logs.w, false, truncate(m.Inspect.LogErr, g.logs.w), styleBad)
	} else {
		lines := m.Inspect.VisibleLogs()
		if len(lines) == 0 {
			message := "waiting for output..."
			if m.Inspect.LogQuery != "" {
				message = "nothing matches " + m.Inspect.LogQuery
			}
			puts(s, x, top, g.logs.w, false, message, styleDim)
		}
		start := len(lines) - g.logRoom
		if start < 0 {
			start = 0
		}
		for i := 0; start+i < len(lines) && i < g.logRoom; i++ {
			putSegments(s, x, top+i, g.logs.w, logSegments(lines[start+i], m.Inspect.LogQuery))
		}
	}

	shown, text, style := m.Inspect.LogQuery, m.Inspect.LogQuery, styleInput
	if shown == "" {
		text, style = logPlaceholder, stylePlace
	}
	runes := []rune(text)
	if len(runes) > g.search.w-1 {
		if shown == "" {
			runes = runes[:g.search.w-1]
		} else {
			runes = runes[len(runes)-(g.search.w-1):]
		}
	}
	written := puts(s, g.search.x, g.search.y, 0, false, string(runes), style)
	if m.Inspect.LogSearch {
		s.SetContent(g.search.x+written, g.search.y, ' ', nil, styleCursor)
		written++
	}
	for i := written; i < g.search.w; i++ {
		s.SetContent(g.search.x+i, g.search.y, ' ', nil, styleBase)
	}
}

type segment struct {
	text  string
	style tcell.Style
}

func putSegments(s tcell.Screen, x, y, width int, segments []segment) {
	written := 0
	for _, seg := range segments {
		if written >= width {
			break
		}
		text := truncate(seg.text, width-written)
		written += puts(s, x+written, y, 0, false, text, seg.style)
	}
	for i := written; i < width; i++ {
		s.SetContent(x+i, y, ' ', nil, styleBase)
	}
}

const valueColumn = 21

func configSegments(line string, format Format) []segment {
	switch format {
	case FormatYAML:
		return yamlSegments(line)
	case FormatTextual:
		return textualSegments(line)
	}
	return fieldSegments(line)
}

func fieldSegments(line string) []segment {
	if line == "" {
		return nil
	}
	if line[0] != ' ' {
		return []segment{{line, styleChoice}}
	}
	if len(line) <= valueColumn {
		return []segment{{line, styleDim}}
	}

	label := strings.TrimSpace(line[:valueColumn])
	value := line[valueColumn:]
	return []segment{
		{line[:valueColumn], styleDim},
		{value, valueStyle(label, value)},
	}
}

func valueStyle(label, value string) tcell.Style {
	trimmed := strings.TrimSpace(value)

	switch label {
	case "status", "state":
		return phraseStyle(trimmed)
	case "restarts":
		if trimmed != "0" {
			return styleWarn
		}
	case "limits", "requests":
		if trimmed == "not set" {
			return styleWarn
		}
	case "qos class":
		switch trimmed {
		case "Guaranteed":
			return styleGood
		case "BestEffort":
			return styleWarn
		}
	case "Ready", "ContainersReady", "PodScheduled", "Initialized", "PodReadyToStartContainers":
		if strings.HasPrefix(trimmed, "True") {
			return styleGood
		}
		return styleWarn
	case "name", "namespace", "node", "pod ip", "owner":
		return styleBase
	case "deleting since":
		return styleWarn
	}
	return styleBase
}

var phrases = []struct {
	text  string
	style tcell.Style
}{
	{"CrashLoopBackOff", styleBad},
	{"ImagePullBackOff", styleBad},
	{"ErrImagePull", styleBad},
	{"CreateContainerConfigError", styleBad},
	{"OOMKilled", styleBad},
	{"killed for using too much memory", styleBad},
	{"Worth a look", styleBad},
	{"terminated", styleBad},
	{"Terminating", styleWarn},
	{"NotReady", styleWarn},
	{"Pending", styleWarn},
	{"waiting", styleWarn},
	{"stuck in", styleWarn},
	{"with no limits set", styleWarn},
	{"not set", styleWarn},
	{"restarted", styleWarn},
	{"Running", styleGood},
	{"running", styleGood},
	{"ready", styleGood},
}

func phraseStyle(text string) tcell.Style {
	for _, phrase := range phrases {
		if strings.Contains(text, phrase.text) {
			return phrase.style
		}
	}
	return styleBase
}

func textualSegments(line string) []segment {
	if line == "" {
		return nil
	}

	out := make([]segment, 0, 4)
	rest := line
	for rest != "" {
		index, match := -1, phrases[0]
		for _, phrase := range phrases {
			if at := strings.Index(rest, phrase.text); at >= 0 && (index < 0 || at < index) {
				index, match = at, phrase
			}
		}
		if index < 0 {
			out = append(out, segment{rest, styleBase})
			break
		}
		if index > 0 {
			out = append(out, segment{rest[:index], styleBase})
		}
		out = append(out, segment{match.text, match.style})
		rest = rest[index+len(match.text):]
	}
	return out
}

func yamlSegments(line string) []segment {
	trimmed := strings.TrimLeft(line, " -")
	if trimmed == "" {
		return []segment{{line, styleBase}}
	}

	key, value, found := strings.Cut(line, ":")
	if !found {
		return []segment{{line, styleBase}}
	}
	return []segment{
		{key + ":", styleChoice},
		{value, phraseStyle(value)},
	}
}

func logSegments(line LogLine, query string) []segment {
	out := make([]segment, 0, 4)
	if line.Time != "" {
		out = append(out, segment{line.Time + " ", styleDim})
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return append(out, segment{line.Text, styleBase})
	}

	rest, lower := line.Text, strings.ToLower(query)
	for rest != "" {
		index := strings.Index(strings.ToLower(rest), lower)
		if index < 0 {
			out = append(out, segment{rest, styleBase})
			break
		}
		if index > 0 {
			out = append(out, segment{rest[:index], styleBase})
		}
		out = append(out, segment{rest[index : index+len(query)], styleMatch})
		rest = rest[index+len(query):]
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

func HitInspect(width, height, x, y int) Target {
	g := inspectGeom(width, height)

	switch {
	case g.dflt.contains(x, y):
		return HitFormatDefault
	case g.textual.contains(x, y):
		return HitFormatTextual
	case g.yaml.contains(x, y):
		return HitFormatYAML
	case y >= g.search.y-1 && y <= g.search.y+1 && x >= g.right.x && x < g.right.x+g.right.w:
		return HitLogSearch
	case g.right.contains(x, y):
		return HitLogPane
	case g.left.contains(x, y):
		return HitConfigPane
	}
	return HitNone
}
