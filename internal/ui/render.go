package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/miraxmetov/ktop/internal/kube"
)

const (
	gap          = 2
	minName      = 20
	lineClock    = 0
	lineTitle    = 1
	nsBoxTop     = 2
	lineNs       = 3
	lineStatus   = 5
	podBoxTop    = 6
	linePods     = 7
	lineHeader   = 9
	lineRule     = 10
	rowTop       = 11
	overhead     = 13
	kubeInnerMin = 30
	dropMax      = 8
)

type Focus int

const (
	FocusTable Focus = iota
	FocusPods
	FocusNamespace
	FocusKubeconfig
)

type Target int

const (
	HitNone Target = iota
	HitNamespaceInput
	HitPodInput
	HitKubeconfig
	HitCritical
	HitWarning
	HitDimStatus
	HitDimCPU
	HitDimMemory
	HitDropdown
	HitRow
)

const (
	warnColor     = tcell.Color220
	selectedColor = tcell.Color248
	inkColor      = tcell.Color16
)

var (
	styleBase     = tcell.StyleDefault
	styleBold     = tcell.StyleDefault.Bold(true)
	styleDim      = tcell.StyleDefault.Foreground(tcell.ColorGray)
	styleGood     = tcell.StyleDefault.Foreground(tcell.ColorGreen)
	styleWarn     = tcell.StyleDefault.Foreground(warnColor)
	styleBad      = tcell.StyleDefault.Foreground(tcell.ColorRed).Bold(true)
	styleAccent   = tcell.StyleDefault.Foreground(tcell.ColorTeal)
	styleTitle    = tcell.StyleDefault.Foreground(tcell.ColorWhite).Bold(true)
	styleInput    = tcell.StyleDefault.Foreground(tcell.ColorWhite)
	stylePlace    = tcell.StyleDefault.Foreground(tcell.ColorGray).Italic(true)
	styleFocused  = tcell.StyleDefault.Foreground(tcell.ColorTeal).Bold(true)
	styleCursor   = tcell.StyleDefault.Reverse(true)
	styleSelected = tcell.StyleDefault.Background(selectedColor).Foreground(inkColor)
	styleKube     = tcell.StyleDefault.Foreground(tcell.ColorTeal).Underline(true)
	styleChoice   = tcell.StyleDefault.Foreground(tcell.Color231).Bold(true)
	styleDropdown = tcell.StyleDefault.Foreground(tcell.ColorWhite)
)

type Model struct {
	Namespace      string
	Started        time.Time
	Context        string
	Kubeconfig     string
	PathQuery      string
	PathOptions    []string
	All            []kube.Row
	Rows           []kube.Row
	PodQuery       string
	NamespaceQuery string
	Namespaces     []string
	NamespaceNote  string
	Focus          Focus
	Level          kube.Level
	Dimension      kube.Dimension
	Choice         int
	Note           string
	Err            string
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
	m.Rows = kube.FilterLevel(kube.Filter(m.All, m.PodQuery), m.Level, m.Dimension)
}

func (m *Model) Counts() (int, int) {
	return kube.Count(m.All, m.Dimension)
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
	case FocusKubeconfig:
		return m.PathOptions
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
	key    string
	title  string
	width  int
	right  bool
	center bool
	tinted bool
	value  func(kube.Row) (string, tcell.Style)
}

var columns = []column{
	{key: "name", title: "POD", width: 0, value: func(r kube.Row) (string, tcell.Style) {
		return r.Name, styleBase
	}},
	{key: "status", title: "STATUS", width: 18, tinted: true, value: func(r kube.Row) (string, tcell.Style) {
		return r.Status, severityStyle(r.Severity)
	}},
	{key: "cpu", title: "CPU", width: 8, right: true, value: func(r kube.Row) (string, tcell.Style) {
		if !r.HasCPU {
			return "-", styleDim
		}
		return fmt.Sprintf("%.0fm", r.CPU), styleBase
	}},
	{key: "cpu_pct", title: "%LIM", width: 6, right: true, tinted: true, value: func(r kube.Row) (string, tcell.Style) {
		return pctText(r.CPUPct), pctStyle(r.CPUPct)
	}},
	{key: "mem", title: "MEM", width: 9, right: true, value: func(r kube.Row) (string, tcell.Style) {
		if !r.HasMem {
			return "-", styleDim
		}
		return fmt.Sprintf("%.0fMi", r.Mem), styleBase
	}},
	{key: "mem_pct", title: "%LIM", width: 6, right: true, tinted: true, value: func(r kube.Row) (string, tcell.Style) {
		return pctText(r.MemPct), pctStyle(r.MemPct)
	}},
	{key: "restarts", title: "RESTART CTR", width: 11, center: true, value: func(r kube.Row) (string, tcell.Style) {
		if r.NewRestarts > 0 {
			return fmt.Sprintf("%d +%d", r.Restarts, r.NewRestarts), styleWarn
		}
		if r.Restarts == 0 {
			return "0", styleDim
		}
		return fmt.Sprintf("%d", r.Restarts), styleBold
	}},
	{key: "ooms", title: "OOM CTR", width: 7, center: true, value: func(r kube.Row) (string, tcell.Style) {
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

func Uptime(d time.Duration) string {
	seconds := int(d.Seconds())
	if seconds < 0 {
		seconds = 0
	}

	const (
		minute = 60
		hour   = 60 * minute
		day    = 24 * hour
		week   = 7 * day
		month  = 30 * day
		year   = 365 * day
	)

	switch {
	case seconds < minute:
		return fmt.Sprintf("%ds", seconds)
	case seconds < hour:
		return fmt.Sprintf("%dm %ds", seconds/minute, seconds%minute/1)
	case seconds < day:
		return fmt.Sprintf("%dh %dm", seconds/hour, seconds%hour/minute)
	case seconds < week:
		return fmt.Sprintf("%dd %dh", seconds/day, seconds%day/hour)
	case seconds < month:
		return fmt.Sprintf("%dw %dd", seconds/week, seconds%week/day)
	case seconds < year:
		return fmt.Sprintf("%dmo %dw", seconds/month, seconds%month/week)
	}
	return fmt.Sprintf("%dy %dmo", seconds/year, seconds%year/month)
}

func RestartText(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return fmt.Sprintf("%s ago (at %s)", Ago(now.Sub(t)), t.Local().Format("15:04:05"))
}

type geometry struct {
	cols      []column
	widths    []int
	total     int
	nsBox     rect
	podBox    rect
	nsInput   rect
	podInput  rect
	kubeInput rect
	critical  rect
	warning   rect
	dimStatus rect
	dimCPU    rect
	dimMemory rect
	dropdown  rect
	options   []string
	room      int
}

const (
	podPlaceholder  = "Search for pods..."
	nsPlaceholder   = "Search namespaces..."
	kubePlaceholder = "Path to kubeconfig..."
)

func ShortPath(path string) string {
	if path == "" {
		return "no kubeconfig"
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(path, home+"/") {
		return "~" + path[len(home):]
	}
	return path
}

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

func boxWidthFor(total, share, low, high int) int {
	width := total / share
	if width < low {
		width = low
	}
	if width > high {
		width = high
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

	nsInner := boxWidthFor(total, 6, 21, 30)
	podInner := boxWidthFor(total, 3, 26, 60)
	g.nsBox = rect{x: 0, y: nsBoxTop, w: nsInner + 4, h: 3}
	g.podBox = rect{x: 0, y: podBoxTop, w: podInner + 4, h: 3}
	g.nsInput = rect{x: 2, y: lineNs, w: nsInner, h: 1}
	g.podInput = rect{x: 2, y: linePods, w: podInner, h: 1}

	kubeWidth := len([]rune(ShortPath(m.Kubeconfig)))
	if m.Focus == FocusKubeconfig {
		kubeWidth = inputWidth(m.PathQuery, kubePlaceholder, kubeInnerMin)
	}
	kubeX := total - kubeWidth
	if minX := len([]rune("Using "+m.Namespace+" namespace")) + 2; kubeX < minX {
		kubeX = minX
	}
	if kubeX+kubeWidth > width {
		kubeX = max(0, width-kubeWidth)
	}
	g.kubeInput = rect{x: kubeX, y: lineTitle, w: kubeWidth, h: 1}

	crit, warn := m.Counts()
	critText := fmt.Sprintf("%d critical", crit)
	warnText := fmt.Sprintf("%d warning", warn)
	groupWidth := len(critText) + 3 + len(warnText) + len(dimensionPrefix) +
		len("status") + 3 + len("cpu") + 3 + len("memory")
	x := max(0, (total-groupWidth)/2)

	g.critical = rect{x: x, y: lineStatus, w: len(critText), h: 1}
	x += len(critText) + 3
	g.warning = rect{x: x, y: lineStatus, w: len(warnText), h: 1}
	x += len(warnText) + len(dimensionPrefix)
	g.dimStatus = rect{x: x, y: lineStatus, w: len("status"), h: 1}
	x += len("status") + 3
	g.dimCPU = rect{x: x, y: lineStatus, w: len("cpu"), h: 1}
	x += len("cpu") + 3
	g.dimMemory = rect{x: x, y: lineStatus, w: len("memory"), h: 1}

	if m.Focus != FocusTable {
		g.options = m.Options()
		anchor := g.nsBox
		switch m.Focus {
		case FocusPods:
			anchor = g.podBox
		case FocusKubeconfig:
			anchor = g.kubeInput
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
			if w := len([]rune(optionLabel(m.Focus, item))) + 4; w > boxWidth {
				boxWidth = w
			}
		}
		if boxWidth > width {
			boxWidth = width
		}
		boxX := anchor.x
		if boxX+boxWidth > width {
			boxX = max(0, width-boxWidth)
		}
		available := height - anchor.y - anchor.h - 2
		if rows+2 > available {
			rows = available - 2
		}
		if rows < 1 {
			rows = 1
		}
		g.dropdown = rect{x: boxX, y: anchor.y + anchor.h, w: boxWidth, h: rows + 2}
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

type Rect struct {
	X, Y, W, H int
}

type Layout struct {
	Critical  Rect
	Warning   Rect
	DimStatus Rect
	DimCPU    Rect
	DimMemory Rect
}

func Geometry(m Model, width, height int) Layout {
	g := geom(m, width, height)
	return Layout{
		Critical:  Rect{X: g.critical.x, Y: g.critical.y, W: g.critical.w, H: g.critical.h},
		Warning:   Rect{X: g.warning.x, Y: g.warning.y, W: g.warning.w, H: g.warning.h},
		DimStatus: Rect{X: g.dimStatus.x, Y: g.dimStatus.y, W: g.dimStatus.w, H: g.dimStatus.h},
		DimCPU:    Rect{X: g.dimCPU.x, Y: g.dimCPU.y, W: g.dimCPU.w, H: g.dimCPU.h},
		DimMemory: Rect{X: g.dimMemory.x, Y: g.dimMemory.y, W: g.dimMemory.w, H: g.dimMemory.h},
	}
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
	if g.nsBox.contains(x, y) {
		return HitNamespaceInput, 0
	}
	if g.podBox.contains(x, y) {
		return HitPodInput, 0
	}
	if g.kubeInput.contains(x, y) {
		return HitKubeconfig, 0
	}
	if g.critical.contains(x, y) {
		return HitCritical, 0
	}
	if g.warning.contains(x, y) {
		return HitWarning, 0
	}
	if g.dimStatus.contains(x, y) {
		return HitDimStatus, 0
	}
	if g.dimCPU.contains(x, y) {
		return HitDimCPU, 0
	}
	if g.dimMemory.contains(x, y) {
		return HitDimMemory, 0
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
			s.SetContent(x+i, y, ' ', nil, style)
		}
		x += pad
	}
	for i, r := range runes {
		s.SetContent(x+i, y, r, nil, style)
	}
	if !right {
		for i := 0; i < pad; i++ {
			s.SetContent(x+len(runes)+i, y, ' ', nil, style)
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
		if text == "" {
			runes = runes[:box.w-1]
		} else {
			runes = runes[len(runes)-(box.w-1):]
		}
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

func drawBox(s tcell.Screen, box rect, focused bool) {
	style := styleDim
	if focused {
		style = styleFocused
	}
	line := repeat("\u2500", box.w-2)
	puts(s, box.x, box.y, 0, false, "\u250c"+line+"\u2510", style)
	puts(s, box.x, box.y+box.h-1, 0, false, "\u2514"+line+"\u2518", style)
	for y := box.y + 1; y < box.y+box.h-1; y++ {
		puts(s, box.x, y, 0, false, "\u2502", style)
		puts(s, box.x+box.w-1, y, 0, false, "\u2502", style)
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
			style = styleSelected
		}
		puts(s, box.x, y, 0, false, "│", styleDim)
		text := ""
		if index < len(g.options) {
			text = " " + optionLabel(m.Focus, g.options[index])
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

func optionLabel(focus Focus, option string) string {
	if focus != FocusKubeconfig {
		return option
	}
	trimmed := strings.TrimSuffix(option, "/")
	if i := strings.LastIndex(trimmed, "/"); i >= 0 {
		return option[i+1:]
	}
	return option
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

	clock := now.Format("15:04:05")
	uptime := ""
	if !m.Started.IsZero() {
		uptime = "  (" + Uptime(now.Sub(m.Started)) + ")"
	}
	clockX := max(0, (total-len(clock)-len(uptime))/2)
	x := clockX + puts(s, clockX, lineClock, 0, false, clock, styleAccent)
	puts(s, x, lineClock, 0, false, uptime, styleDim)

	x = puts(s, 0, lineTitle, 0, false, "Using ", styleDim)
	x += puts(s, x, lineTitle, 0, false, m.Namespace, styleTitle)
	puts(s, x, lineTitle, 0, false, " namespace", styleDim)

	if m.Focus == FocusKubeconfig {
		drawInput(s, g.kubeInput, m.PathQuery, kubePlaceholder, true)
	} else {
		puts(s, g.kubeInput.x, lineTitle, 0, false, ShortPath(m.Kubeconfig), styleKube)
	}

	drawBox(s, g.nsBox, m.Focus == FocusNamespace)
	drawInput(s, g.nsInput, m.NamespaceQuery, nsPlaceholder, m.Focus == FocusNamespace)
	if m.Focus == FocusNamespace && m.NamespaceNote != "" {
		noteX := g.nsBox.x + g.nsBox.w + 2
		puts(s, noteX, lineNs, max(0, total-noteX), false, m.NamespaceNote, styleDim)
	}

	crit, warn := m.Counts()

	message, messageStyle := m.Note, styleWarn
	if m.Err != "" {
		message, messageStyle = m.Err, styleBad
	}
	if message != "" {
		puts(s, 0, lineStatus, 0, false, truncate(message, max(0, g.critical.x-2)), messageStyle)
	}

	puts(s, g.critical.x, lineStatus, 0, false, fmt.Sprintf("%d critical", crit),
		pickStyle(styleBad, m.Level == kube.LevelCritical))
	puts(s, g.critical.x+g.critical.w, lineStatus, 0, false, " / ", styleDim)
	puts(s, g.warning.x, lineStatus, 0, false, fmt.Sprintf("%d warning", warn),
		pickStyle(styleWarn, m.Level == kube.LevelWarning))

	puts(s, g.warning.x+g.warning.w, lineStatus, 0, false, dimensionPrefix, styleDim)
	puts(s, g.dimStatus.x, lineStatus, 0, false, "status", pickStyle(styleChoice, m.Dimension == kube.DimStatus))
	puts(s, g.dimStatus.x+g.dimStatus.w, lineStatus, 0, false, " / ", styleDim)
	puts(s, g.dimCPU.x, lineStatus, 0, false, "cpu", pickStyle(styleChoice, m.Dimension == kube.DimCPU))
	puts(s, g.dimCPU.x+g.dimCPU.w, lineStatus, 0, false, " / ", styleDim)
	puts(s, g.dimMemory.x, lineStatus, 0, false, "memory", pickStyle(styleChoice, m.Dimension == kube.DimMemory))

	drawBox(s, g.podBox, m.Focus == FocusPods)
	drawInput(s, g.podInput, m.PodQuery, podPlaceholder, m.Focus == FocusPods)
	if m.PodQuery != "" {
		puts(s, g.podBox.x+g.podBox.w+2, linePods, 0, false,
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
		switch {
		case m.Err != "":
			empty = ""
		case m.Level != kube.LevelAll && len(m.All) > 0:
			empty = emptyLevelText(m.Level, m.Dimension)
		case m.PodQuery != "" && len(m.All) > 0:
			empty = "no pod matches " + m.PodQuery
		}
		puts(s, 0, rowTop, 0, false, empty, styleDim)
	}

	for i := 0; i < g.room && offset+i < len(m.Rows); i++ {
		row := m.Rows[offset+i]
		y := rowTop + i
		selected := m.Cursor >= 0 && offset+i == m.Cursor
		if selected {
			for cx := 0; cx < total; cx++ {
				s.SetContent(cx, y, ' ', nil, styleSelected)
			}
		}
		x = 0
		for ci, c := range g.cols {
			text, style := c.value(row)
			if c.key == "last_restart" && !row.LastRestart.IsZero() {
				text = RestartText(row.LastRestart, now)
			}
			if c.center {
				text = centerText(text, g.widths[ci])
			}
			if selected {
				style = onSelection(style, c.tinted)
			}
			x += puts(s, x, y, g.widths[ci], c.right, text, style)
			if selected {
				for gp := 0; gp < gap && x+gp < total; gp++ {
					s.SetContent(x+gp, y, ' ', nil, styleSelected)
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
	items := []string{
		"[/] search pods",
		"[N] namespace",
		"[C] kubeconfig",
		"click to focus",
		"[Q] quit",
	}
	if m.Focus != FocusTable {
		items = []string{
			"[" + arrowUp + arrowDown + "] choose",
			"[Tab] complete",
			"[Enter] apply",
			"[Esc] close",
			"[Shift+Tab] switch field",
		}
	} else if hidden > 0 {
		items = append([]string{fmt.Sprintf("+%d more", hidden)}, items...)
	}
	puts(s, 0, height-1, 0, false, justify(items, total), styleDim)
	s.Show()
}

const (
	dot       = "·"
	arrowUp   = "↑"
	arrowDown = "↓"
)

func justify(items []string, width int) string {
	if len(items) == 0 || width <= 0 {
		return ""
	}
	if len(items) == 1 {
		return truncate(items[0], width)
	}

	length := 0
	for _, item := range items {
		length += len([]rune(item))
	}
	gaps := len(items) - 1
	spread := width - length
	if spread < gaps*2 {
		return truncate(strings.Join(items, "  "), width)
	}

	base, extra := spread/gaps, spread%gaps
	var line strings.Builder
	for i, item := range items {
		line.WriteString(item)
		if i == gaps {
			break
		}
		space := base
		if i < extra {
			space++
		}
		line.WriteString(strings.Repeat(" ", space))
	}
	return line.String()
}

const dimensionPrefix = "   issues regarding "

func onSelection(style tcell.Style, tinted bool) tcell.Style {
	fg, _, attrs := style.Decompose()
	ink := inkColor
	if tinted {
		switch fg {
		case tcell.ColorRed:
			ink = tcell.Color88
		case warnColor:
			ink = tcell.Color94
		case tcell.ColorGreen:
			ink = tcell.Color22
		case tcell.ColorGray:
			ink = tcell.Color238
		}
	}
	return tcell.StyleDefault.Background(selectedColor).Foreground(ink).Bold(attrs&tcell.AttrBold != 0)
}

func pickStyle(base tcell.Style, active bool) tcell.Style {
	if active {
		return base.Bold(true).Reverse(true)
	}
	return base
}

func emptyLevelText(level kube.Level, dimension kube.Dimension) string {
	what := "critical"
	if level == kube.LevelWarning {
		what = "warning"
	}
	switch dimension {
	case kube.DimStatus:
		return "no pod is " + what + " by status"
	case kube.DimCPU:
		return "no pod is " + what + " by cpu"
	case kube.DimMemory:
		return "no pod is " + what + " by memory"
	}
	return "no pod is " + what + " by cpu or memory"
}

func centerText(text string, width int) string {
	pad := width - len([]rune(text))
	if pad <= 0 {
		return text
	}
	return strings.Repeat(" ", pad/2) + text
}

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
