package ui

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/miraxmetov/ktop/internal/kube"
)

const (
	gap         = 2
	minName     = 20
	maxName     = 56
	lineTitle   = 0
	lineNs      = 1
	lineStatus  = 3
	linePods    = 5
	lineHeader  = 7
	lineRule    = 8
	rowTop      = 9
	overhead    = 11
	nsInnerMin  = 24
	podInnerMin = 30
	dropMax     = 8
)

type Focus int

const (
	FocusTable Focus = iota
	FocusPods
	FocusNamespace
)

type Target int

const (
	HitNone Target = iota
	HitNamespaceInput
	HitPodInput
	HitDropdown
	HitRow
)

var (
	styleBase     = tcell.StyleDefault
	styleBold     = tcell.StyleDefault.Bold(true)
	styleDim      = tcell.StyleDefault.Foreground(tcell.ColorGray)
	styleGood     = tcell.StyleDefault.Foreground(tcell.ColorGreen)
	styleWarn     = tcell.StyleDefault.Foreground(tcell.ColorYellow)
	styleBad      = tcell.StyleDefault.Foreground(tcell.ColorRed).Bold(true)
	styleAccent   = tcell.StyleDefault.Foreground(tcell.ColorTeal)
	styleTitle    = tcell.StyleDefault.Foreground(tcell.ColorWhite).Bold(true)
	styleInput    = tcell.StyleDefault.Foreground(tcell.ColorWhite)
	stylePlace    = tcell.StyleDefault.Foreground(tcell.ColorGray).Italic(true)
	styleFocused  = tcell.StyleDefault.Foreground(tcell.ColorTeal).Bold(true)
	styleCursor   = tcell.StyleDefault.Reverse(true)
	styleDropdown = tcell.StyleDefault.Foreground(tcell.ColorWhite)
)

type Model struct {
	Namespace      string
	Context        string
	All            []kube.Row
	Rows           []kube.Row
	PodQuery       string
	NamespaceQuery string
	Namespaces     []string
	NamespaceNote  string
	Focus          Focus
	Choice         int
	Note           string
	Err            string
	Interval       time.Duration
	Offset         int
	Cursor         int
	Now            time.Time
}

type rect struct {
	x, y, w, h int
}

func (r rect) contains(x, y int) bool {
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

func ApplyFilter(m *Model) {
	m.Rows = kube.Filter(m.All, m.PodQuery)
}

func (m *Model) NamespaceMatches() []string {
	return kube.MatchNamespaces(m.Namespaces, m.NamespaceQuery)
}

func (m *Model) PodMatches() []string {
	names := make([]string, 0, len(m.Rows))
	for _, r := range m.Rows {
		names = append(names, r.Name)
	}
	return names
}

func (m *Model) Options() []string {
	switch m.Focus {
	case FocusNamespace:
		return m.NamespaceMatches()
	case FocusPods:
		return m.PodMatches()
	}
	return nil
}

func (m *Model) SelectedName() string {
	if m.Cursor >= 0 && m.Cursor < len(m.Rows) {
		return m.Rows[m.Cursor].Name
	}
	return ""
}

func (m *Model) ClampChoice() {
	options := m.Options()
	if m.Choice >= len(options) {
		m.Choice = len(options) - 1
	}
	if m.Choice < 0 {
		m.Choice = 0
	}
}

type column struct {
	key   string
	title string
	width int
	right bool
	value func(kube.Row) (string, tcell.Style)
}

var columns = []column{
	{key: "name", title: "POD", width: 0, value: func(r kube.Row) (string, tcell.Style) {
		return r.Name, styleBase
	}},
	{key: "status", title: "STATUS", width: 18, value: func(r kube.Row) (string, tcell.Style) {
		return r.Status, severityStyle(r.Severity)
	}},
	{key: "cpu", title: "CPU", width: 8, right: true, value: func(r kube.Row) (string, tcell.Style) {
		if !r.HasCPU {
			return "-", styleDim
		}
		return fmt.Sprintf("%.0fm", r.CPU), styleBase
	}},
	{key: "cpu_pct", title: "%LIM", width: 6, right: true, value: func(r kube.Row) (string, tcell.Style) {
		return pctText(r.CPUPct), pctStyle(r.CPUPct)
	}},
	{key: "mem", title: "MEM", width: 9, right: true, value: func(r kube.Row) (string, tcell.Style) {
		if !r.HasMem {
			return "-", styleDim
		}
		return fmt.Sprintf("%.0fMi", r.Mem), styleBase
	}},
	{key: "mem_pct", title: "%LIM", width: 6, right: true, value: func(r kube.Row) (string, tcell.Style) {
		return pctText(r.MemPct), pctStyle(r.MemPct)
	}},
	{key: "restarts", title: "RESTART CTR", width: 11, right: true, value: func(r kube.Row) (string, tcell.Style) {
		if r.NewRestarts > 0 {
			return fmt.Sprintf("%d +%d", r.Restarts, r.NewRestarts), styleWarn
		}
		if r.Restarts == 0 {
			return "0", styleDim
		}
		return fmt.Sprintf("%d", r.Restarts), styleBold
	}},
	{key: "ooms", title: "OOM CTR", width: 7, right: true, value: func(r kube.Row) (string, tcell.Style) {
		if r.OOMs == 0 {
			return "0", styleDim
		}
		return fmt.Sprintf("%d", r.OOMs), styleBold
	}},
	{key: "exit", title: "EXIT", width: 16, value: func(r kube.Row) (string, tcell.Style) {
		text := ExitText(r.ExitCode, r.ExitReason)
		if r.ExitCode == nil || *r.ExitCode == 0 {
			return text, styleDim
		}
		return text, styleBold
	}},
	{key: "last_restart", title: "LAST RESTART", width: 24, value: func(r kube.Row) (string, tcell.Style) {
		if r.LastRestart.IsZero() {
			return "-", styleDim
		}
		return "", styleBold
	}},
}

var dropOrder = []string{"last_restart", "exit", "ooms", "cpu", "mem", "restarts", "mem_pct", "cpu_pct", "status"}

func severityStyle(s kube.Severity) tcell.Style {
	switch s {
	case kube.Good:
		return styleGood
	case kube.Warn:
		return styleWarn
	case kube.Bad:
		return styleBad
	}
	return styleDim
}

func pctText(p float64) string {
	if p < 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", p)
}

func pctStyle(p float64) tcell.Style {
	switch {
	case p < 0:
		return styleDim
	case p >= kube.CritPct:
		return styleBad
	case p >= kube.WarnPct:
		return styleWarn
	}
	return styleGood
}

func ExitText(code *int32, reason string) string {
	if code == nil {
		return "-"
	}
	c := int(*code)
	if reason != "" && reason != "Error" && reason != "ContainerStatusUnknown" {
		return fmt.Sprintf("%d (%s)", c, reason)
	}
	if c > 128 && c < 192 {
		if name, ok := signals[c-128]; ok {
			return fmt.Sprintf("%d (%s)", c, name)
		}
	}
	if name, ok := exitCodes[c]; ok {
		return fmt.Sprintf("%d (%s)", c, name)
	}
	return fmt.Sprintf("%d (Error)", c)
}

var signals = map[int]string{
	1: "SIGHUP", 2: "SIGINT", 3: "SIGQUIT", 4: "SIGILL", 6: "SIGABRT", 8: "SIGFPE",
	9: "SIGKILL", 11: "SIGSEGV", 13: "SIGPIPE", 14: "SIGALRM", 15: "SIGTERM", 24: "SIGXCPU",
}

var exitCodes = map[int]string{
	0: "Success", 1: "Error", 2: "Misuse", 125: "DockerFail",
	126: "NotExecutable", 127: "NotFound",
}

func Ago(d time.Duration) string {
	s := int(d.Seconds())
	if s < 0 {
		s = 0
	}
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm%ds", s/60, s%60)
	case s < 86400:
		return fmt.Sprintf("%dh%dm", s/3600, (s%3600)/60)
	}
	return fmt.Sprintf("%dd%dh", s/86400, (s%86400)/3600)
}

func RestartText(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return fmt.Sprintf("%s ago (at %s)", Ago(now.Sub(t)), t.Local().Format("15:04:05"))
}

type geometry struct {
	cols     []column
	widths   []int
	total    int
	nsInput  rect
	podInput rect
	dropdown rect
	options  []string
	room     int
}

const (
	podPlaceholder = "Search for pods..."
	nsPlaceholder  = "Search for namespaces..."
)

func inputWidth(text, hint string, minInner int) int {
	shown := text
	if shown == "" {
		shown = hint
	}
	width := len([]rune(shown)) + 1
	if width < minInner {
		width = minInner
	}
	return width
}

func geom(m Model, width, height int) geometry {
	cols := make([]column, len(columns))
	copy(cols, columns)

	for _, key := range dropOrder {
		if fixedWidth(cols)+minName <= width {
			break
		}
		filtered := cols[:0]
		for _, c := range cols {
			if c.key != key {
				filtered = append(filtered, c)
			}
		}
		cols = filtered
	}

	fixed := fixedWidth(cols)
	nameWidth := width - fixed - 1
	if nameWidth > maxName {
		nameWidth = maxName
	}
	if nameWidth < 8 {
		nameWidth = 8
	}

	widths := make([]int, len(cols))
	widths[0] = nameWidth
	for i := 1; i < len(cols); i++ {
		widths[i] = cols[i].width
	}

	total := nameWidth + fixed
	if total > width {
		total = width
	}

	g := geometry{cols: cols, widths: widths, total: total, room: Visible(height)}

	g.nsInput = rect{
		x: 0, y: lineNs, h: 1,
		w: inputWidth(m.NamespaceQuery, nsPlaceholder, nsInnerMin),
	}
	g.podInput = rect{
		x: 0, y: linePods, h: 1,
		w: inputWidth(m.PodQuery, podPlaceholder, podInnerMin),
	}

	if m.Focus != FocusTable {
		g.options = m.Options()
		anchor := g.nsInput
		if m.Focus == FocusPods {
			anchor = g.podInput
		}
		rows := len(g.options)
		if rows > dropMax {
			rows = dropMax
		}
		if rows < 1 {
			rows = 1
		}
		boxWidth := anchor.w
		for _, item := range g.options[:min(len(g.options), dropMax)] {
			if w := len([]rune(item)) + 4; w > boxWidth {
				boxWidth = w
			}
		}
		if boxWidth > width {
			boxWidth = width
		}
		available := height - anchor.y - 3
		if rows+2 > available {
			rows = available - 2
		}
		if rows < 1 {
			rows = 1
		}
		g.dropdown = rect{x: anchor.x, y: anchor.y + 1, w: boxWidth, h: rows + 2}
	}
	return g
}

func fixedWidth(cols []column) int {
	fixed := 0
	for _, c := range cols[1:] {
		fixed += c.width
	}
	return fixed + gap*(len(cols)-1)
}

func Visible(height int) int {
	room := height - overhead
	if room < 1 {
		return 1
	}
	return room
}

func Hit(m Model, width, height, x, y int) (Target, int) {
	g := geom(m, width, height)

	if g.dropdown.h > 0 && g.dropdown.contains(x, y) {
		index := y - g.dropdown.y - 1 + dropdownOffset(m, g)
		if index >= 0 && index < len(g.options) {
			return HitDropdown, index
		}
		return HitNone, 0
	}
	if g.nsInput.contains(x, y) {
		return HitNamespaceInput, 0
	}
	if g.podInput.contains(x, y) {
		return HitPodInput, 0
	}
	if y >= rowTop && y < rowTop+g.room && x < g.total {
		index := m.Offset + (y - rowTop)
		if index < len(m.Rows) {
			return HitRow, index
		}
	}
	return HitNone, 0
}

func dropdownOffset(m Model, g geometry) int {
	rows := g.dropdown.h - 2
	if m.Choice < rows {
		return 0
	}
	offset := m.Choice - rows + 1
	if offset > len(g.options)-rows {
		offset = len(g.options) - rows
	}
	if offset < 0 {
		offset = 0
	}
	return offset
}

func puts(s tcell.Screen, x, y, width int, right bool, text string, style tcell.Style) int {
	runes := []rune(text)
	if width > 0 && len(runes) > width {
		runes = runes[:width]
	}
	pad := 0
	if width > len(runes) {
		pad = width - len(runes)
	}
	if right {
		for i := 0; i < pad; i++ {
			s.SetContent(x+i, y, ' ', nil, styleBase)
		}
		x += pad
	}
	for i, r := range runes {
		s.SetContent(x+i, y, r, nil, style)
	}
	if !right {
		for i := 0; i < pad; i++ {
			s.SetContent(x+len(runes)+i, y, ' ', nil, styleBase)
		}
	}
	if width > 0 {
		return width
	}
	return len(runes)
}

func drawInput(s tcell.Screen, box rect, text, placeholder string, focused bool) {
	shown, style := text, styleInput
	if shown == "" {
		shown, style = placeholder, stylePlace
	}
	if focused && text != "" {
		style = styleFocused
	}

	runes := []rune(shown)
	if len(runes) > box.w-1 {
		runes = runes[len(runes)-(box.w-1):]
	}
	written := puts(s, box.x, box.y, 0, false, string(runes), style)

	if focused {
		s.SetContent(box.x+written, box.y, ' ', nil, styleCursor)
		written++
	}
	for i := written; i < box.w; i++ {
		s.SetContent(box.x+i, box.y, ' ', nil, styleBase)
	}
}

func drawDropdown(s tcell.Screen, g geometry, m Model) {
	if g.dropdown.h == 0 {
		return
	}
	box := g.dropdown
	rows := box.h - 2
	offset := dropdownOffset(m, g)

	puts(s, box.x, box.y, 0, false, "┌"+repeat("─", box.w-2)+"┐", styleDim)
	for i := 0; i < rows; i++ {
		y := box.y + 1 + i
		index := offset + i
		style := styleDropdown
		if index == m.Choice {
			style = styleCursor
		}
		puts(s, box.x, y, 0, false, "│", styleDim)
		text := ""
		if index < len(g.options) {
			text = " " + g.options[index]
		} else if len(g.options) == 0 && i == 0 {
			text = " no match"
			style = styleDim
		}
		puts(s, box.x+1, y, box.w-2, false, text, style)
		puts(s, box.x+box.w-1, y, 0, false, "│", styleDim)
	}
	more := ""
	if hidden := len(g.options) - offset - rows; hidden > 0 {
		more = fmt.Sprintf(" +%d more ", hidden)
	}
	bottom := "└" + repeat("─", box.w-2) + "┘"
	puts(s, box.x, box.y+box.h-1, 0, false, bottom, styleDim)
	if more != "" && box.w > len([]rune(more))+4 {
		puts(s, box.x+2, box.y+box.h-1, 0, false, more, styleDim)
	}
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

func Draw(s tcell.Screen, m Model) {
	s.Clear()
	width, height := s.Size()
	g := geom(m, width, height)
	total := g.total
	now := m.Now
	if now.IsZero() {
		now = time.Now()
	}

	x := puts(s, 0, lineTitle, 0, false, "Current namespace: ", styleDim)
	puts(s, x, lineTitle, 0, false, m.Namespace, styleTitle)

	drawInput(s, g.nsInput, m.NamespaceQuery, nsPlaceholder, m.Focus == FocusNamespace)
	if m.Focus == FocusNamespace && m.NamespaceNote != "" {
		puts(s, g.nsInput.x+g.nsInput.w+2, lineNs, max(0, total-g.nsInput.x-g.nsInput.w-2),
			false, m.NamespaceNote, styleDim)
	}

	crit, warn := 0, 0
	for _, r := range m.All {
		switch {
		case r.Worst >= kube.CritPct:
			crit++
		case r.Worst >= kube.WarnPct:
			warn++
		}
	}

	x = 0
	critStyle, warnStyle := styleAccent, styleAccent
	if crit > 0 {
		critStyle = styleBad
	}
	if warn > 0 {
		warnStyle = styleWarn
	}
	x += puts(s, x, lineStatus, 0, false, fmt.Sprintf("%d critical", crit), critStyle)
	x += puts(s, x, lineStatus, 0, false, " / ", styleAccent)
	x += puts(s, x, lineStatus, 0, false, fmt.Sprintf("%d warning", warn), warnStyle)
	if m.Err != "" {
		x += puts(s, x, lineStatus, 0, false, "   ", styleBase)
		x += puts(s, x, lineStatus, 0, false, truncate(m.Err, max(0, total-x-10)), styleBad)
	} else if m.Note != "" {
		x += puts(s, x, lineStatus, 0, false, "   ", styleBase)
		x += puts(s, x, lineStatus, 0, false, truncate(m.Note, max(0, total-x-10)), styleWarn)
	}
	clock := now.Format("15:04:05")
	if total-len(clock) > x {
		puts(s, total-len(clock), lineStatus, 0, false, clock, styleAccent)
	}

	drawInput(s, g.podInput, m.PodQuery, podPlaceholder, m.Focus == FocusPods)
	if m.PodQuery != "" {
		puts(s, g.podInput.x+g.podInput.w+2, linePods, 0, false,
			fmt.Sprintf("%d/%d", len(m.Rows), len(m.All)), styleAccent)
	}

	x = 0
	for i, c := range g.cols {
		x += puts(s, x, lineHeader, g.widths[i], c.right, c.title, styleBold)
		x += gap
	}
	for i := 0; i < total; i++ {
		s.SetContent(i, lineRule, '-', nil, styleDim)
	}

	offset := m.Offset
	if offset > len(m.Rows)-g.room {
		offset = len(m.Rows) - g.room
	}
	if offset < 0 {
		offset = 0
	}

	if len(m.Rows) == 0 {
		empty := "no pods in this namespace"
		if m.PodQuery != "" && len(m.All) > 0 {
			empty = "no pod matches " + m.PodQuery
		} else if m.Err != "" {
			empty = ""
		}
		puts(s, 0, rowTop, 0, false, empty, styleDim)
	}

	for i := 0; i < g.room && offset+i < len(m.Rows); i++ {
		row := m.Rows[offset+i]
		y := rowTop + i
		selected := offset+i == m.Cursor && m.Focus == FocusTable
		if selected {
			for cx := 0; cx < total; cx++ {
				s.SetContent(cx, y, ' ', nil, styleCursor)
			}
		}
		x = 0
		for ci, c := range g.cols {
			text, style := c.value(row)
			if c.key == "last_restart" && !row.LastRestart.IsZero() {
				text = RestartText(row.LastRestart, now)
			}
			if selected {
				style = style.Reverse(true)
			}
			x += puts(s, x, y, g.widths[ci], c.right, text, style)
			if selected {
				for gp := 0; gp < gap && x+gp < total; gp++ {
					s.SetContent(x+gp, y, ' ', nil, styleCursor)
				}
			}
			x += gap
		}
	}

	drawDropdown(s, g, m)

	hidden := len(m.Rows) - offset - g.room
	if hidden < 0 {
		hidden = 0
	}
	footer := fmt.Sprintf("/ search pods  %s  n namespace  %s  click to focus  %s  q quit  %s  refreshes every %s",
		dot, dot, dot, dot, m.Interval.String())
	if m.Focus != FocusTable {
		footer = fmt.Sprintf("%s%s choose  %s  Tab complete  %s  Enter apply  %s  Esc or click away  %s  Shift+Tab switch field",
			arrowUp, arrowDown, dot, dot, dot, dot)
	} else if hidden > 0 {
		footer = fmt.Sprintf("+%d more  %s  %s", hidden, dot, footer)
	}
	puts(s, 0, height-2, 0, false, truncate(counterHint, total), styleDim)
	puts(s, 0, height-1, 0, false, truncate(footer, total), styleDim)
	s.Show()
}

const counterHint = "RESTART CTR is the pod's own total, +N is what happened since ktop started · OOM CTR counts only what ktop saw"

const (
	dot       = "·"
	arrowUp   = "↑"
	arrowDown = "↓"
)

func truncate(text string, width int) string {
	runes := []rune(text)
	if width <= 0 {
		return ""
	}
	if len(runes) <= width {
		return text
	}
	return string(runes[:width])
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
