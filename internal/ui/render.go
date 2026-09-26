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
	gap          = 3
	minName      = 20
	lineClock    = 0
	lineUptime   = 1
	lineTitle    = 2
	nsBoxTop     = 3
	lineNs       = 4
	lineStatus   = 6
	podBoxTop    = 8
	linePods     = 9
	tableTop     = 11
	lineHeader   = 12
	lineRule     = 13
	rowTop       = 14
	overhead     = 17
	kubeInnerMin = 30
	dropMax      = 8
	sortBoth     = "\u21c5"
	sortDesc     = "\u2193"
	sortAsc      = "\u2191"
)

type Focus int

const (
	FocusTable Focus = iota
	FocusPods
	FocusNamespace
	FocusKubeconfig
	FocusKind
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
	HitPodName
	HitKindBox
	HitColumn
	HitActionInspect
	HitActionRestart
	HitActionTerminate
	HitConfirmYes
	HitConfirmCancel
	HitFormatDefault
	HitFormatTextual
	HitFormatYAML
	HitLogSearch
	HitLogPod
	HitLogPodItem
	HitLogPane
	HitConfigPane
	HitDropdown
	HitRow
)

const (
	warnColor      = tcell.Color220
	highlightColor = tcell.Color248
	inkColor       = tcell.Color16
)

var (
	styleBase     = tcell.StyleDefault
	styleBold     = tcell.StyleDefault.Bold(true)
	styleDim      = tcell.StyleDefault.Foreground(tcell.ColorGray)
	styleGood     = tcell.StyleDefault.Foreground(tcell.ColorGreen)
	styleWarn     = tcell.StyleDefault.Foreground(warnColor)
	styleBad      = tcell.StyleDefault.Foreground(tcell.ColorRed).Bold(true)
	styleAccent   = tcell.StyleDefault.Foreground(tcell.ColorTeal)
	styleInput    = tcell.StyleDefault.Foreground(tcell.ColorWhite)
	stylePlace    = tcell.StyleDefault.Foreground(tcell.ColorGray).Italic(true)
	styleFocused  = tcell.StyleDefault.Foreground(tcell.ColorTeal).Bold(true)
	styleCursor   = tcell.StyleDefault.Reverse(true)
	styleKube     = tcell.StyleDefault.Foreground(tcell.ColorTeal).Underline(true)
	styleChoice   = tcell.StyleDefault.Foreground(tcell.Color231).Bold(true)
	styleDropdown = tcell.StyleDefault.Foreground(tcell.ColorWhite)
	styleChosen   = tcell.StyleDefault.Background(highlightColor).Foreground(inkColor)
	styleMatch    = tcell.StyleDefault.Background(warnColor).Foreground(inkColor)
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
	Tick           int
	Kind           kube.Kind
	SortKey        string
	SortOrder      kube.Order
	NameOrder      kube.Order
	Expanded       string
	Confirm        Action
	Screen         Screen
	Loaded         bool
	Inspect        Inspection
	Now            time.Time
}

type Screen int

const (
	ScreenTable Screen = iota
	ScreenInspect
)

type Action int

const (
	ActionNone Action = iota
	ActionRestart
	ActionTerminate
)

type slot struct {
	row    int
	action int
}

const actionLines = 3

func visibleSlots(m Model, offset, room int) []slot {
	out := make([]slot, 0, room)
	for i := offset; i < len(m.Rows) && len(out) < room; i++ {
		out = append(out, slot{row: i, action: -1})
		if m.Rows[i].Name != m.Expanded {
			continue
		}
		for line := 0; line < actionLines && len(out) < room; line++ {
			out = append(out, slot{row: i, action: line})
		}
	}
	return out
}

type rect struct {
	x, y, w, h int
}

func (r rect) contains(x, y int) bool {
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

func ApplyFilter(m *Model) {
	m.Rows = kube.FilterLevel(kube.Filter(m.All, m.PodQuery), m.Level, m.Dimension)
	kube.SortBy(m.Rows, m.SortKey, m.SortOrder, m.NameOrder)
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
	case FocusKind:
		names := make([]string, 0, len(kube.Kinds()))
		for _, kind := range kube.Kinds() {
			names = append(names, kind.String())
		}
		return names
	}
	return nil
}

func (m *Model) ExpandedName() string {
	return m.Expanded
}

func (m *Model) ExpandedIndex() int {
	for i, row := range m.Rows {
		if row.Name == m.Expanded {
			return i
		}
	}
	return -1
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
	key      string
	title    string
	width    int
	tinted   bool
	sortable bool
	value    func(kube.Row) (string, tcell.Style)
}

var columns = []column{
	{key: "name", title: "POD", width: 0, sortable: true, value: func(r kube.Row) (string, tcell.Style) {
		return r.Name, styleBase
	}},
	{key: "status", title: "STATUS", width: 18, tinted: true, sortable: true, value: func(r kube.Row) (string, tcell.Style) {
		return r.Status, severityStyle(r.Severity)
	}},
	{key: "cpu", title: "CPU", width: 8, sortable: true, value: func(r kube.Row) (string, tcell.Style) {
		if !r.HasCPU {
			return "-", styleDim
		}
		return fmt.Sprintf("%.0fm", r.CPU), styleBase
	}},
	{key: "cpu_pct", title: "%LIM", width: 6, tinted: true, sortable: true, value: func(r kube.Row) (string, tcell.Style) {
		return pctText(r.CPUPct), pctStyle(r.CPUPct)
	}},
	{key: "mem", title: "MEM", width: 9, sortable: true, value: func(r kube.Row) (string, tcell.Style) {
		if !r.HasMem {
			return "-", styleDim
		}
		return fmt.Sprintf("%.0fMi", r.Mem), styleBase
	}},
	{key: "mem_pct", title: "%LIM", width: 6, tinted: true, sortable: true, value: func(r kube.Row) (string, tcell.Style) {
		return pctText(r.MemPct), pctStyle(r.MemPct)
	}},
	{key: "restarts", title: "RESTART CTR", width: 13, sortable: true, value: func(r kube.Row) (string, tcell.Style) {
		if r.NewRestarts > 0 {
			return fmt.Sprintf("%d +%d", r.Restarts, r.NewRestarts), styleWarn
		}
		if r.Restarts == 0 {
			return "0", styleDim
		}
		return fmt.Sprintf("%d", r.Restarts), styleBold
	}},
	{key: "ooms", title: "OOM CTR", width: 9, sortable: true, value: func(r kube.Row) (string, tcell.Style) {
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
	{key: "last_restart", title: "LAST RESTART", width: 24, sortable: true, value: func(r kube.Row) (string, tcell.Style) {
		if r.LastRestart.IsZero() {
			return "-", styleDim
		}
		return "", styleBold
	}},
}

var workloadColumns = []column{
	{key: "created", title: "CREATED", width: 14, sortable: true, value: func(r kube.Row) (string, tcell.Style) {
		if r.Created.IsZero() {
			return "-", styleDim
		}
		return "", styleBase
	}},
	{key: "last_restart", title: "LAST POD RESTART", width: 24, sortable: true, value: func(r kube.Row) (string, tcell.Style) {
		if r.LastRestart.IsZero() {
			return "-", styleDim
		}
		return "", styleBold
	}},
}

func columnsFor(kind kube.Kind) []column {
	base := make([]column, 0, len(columns))
	for _, c := range columns {
		if kind != kube.KindPod && (c.key == "exit" || c.key == "last_restart") {
			continue
		}
		if c.key == "name" {
			c.title = kind.Column()
		}
		base = append(base, c)
	}
	if kind == kube.KindPod {
		return base
	}
	return append(base, workloadColumns...)
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
	kindBox   rect
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
	podPlaceholder  = "Search..."
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
	cols := columnsFor(m.Kind)

	for _, key := range dropOrder {
		if fixedWidth(cols)+minName < width {
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

	podBoxWidth := widths[0]
	if podBoxWidth < 26 {
		podBoxWidth = 26
	}
	nsInner := boxWidthFor(total, 6, 21, 30)
	if nsInner+4 > podBoxWidth-4 {
		nsInner = max(17, podBoxWidth-8)
	}
	g.nsBox = rect{x: 0, y: nsBoxTop, w: nsInner + 4, h: 3}
	g.podBox = rect{x: 0, y: podBoxTop, w: podBoxWidth, h: 3}
	g.nsInput = rect{x: 2, y: lineNs, w: nsInner, h: 1}
	g.podInput = rect{x: 2, y: linePods, w: podBoxWidth - 4, h: 1}

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

	kindWidth := 0
	for _, kind := range kube.Kinds() {
		if w := len([]rune(kind.String())) + 6; w > kindWidth {
			kindWidth = w
		}
	}
	g.kindBox = rect{x: max(0, (total-kindWidth)/2), y: nsBoxTop, w: kindWidth, h: 3}

	crit, warn := m.Counts()
	critText := fmt.Sprintf("%d critical", crit)
	warnText := fmt.Sprintf("%d warnings", warn)
	groupWidth := len(facingPrefix) + len(critText) + 3 + len(warnText) + len(dimensionPrefix) +
		len("status") + 3 + len("cpu") + 3 + len("memory")
	x := max(0, (total-groupWidth)/2) + len(facingPrefix)

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
		case FocusKind:
			anchor = g.kindBox
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
	return fixed + gap*(len(cols)-1) + 4
}

func columnXs(widths []int) []int {
	out := make([]int, len(widths))
	x := 2
	for i, w := range widths {
		out[i] = x
		x += w + gap
	}
	return out
}

func (g geometry) dividers() []int {
	xs := columnXs(g.widths)
	out := make([]int, 0, len(xs))
	for i := 0; i < len(xs)-1; i++ {
		out = append(out, xs[i]+g.widths[i]+1)
	}
	return out
}

func sortMark(m Model, c column) string {
	if !c.sortable {
		return ""
	}
	if c.key == "name" {
		if m.SortKey != "" && m.SortOrder != kube.OrderNone {
			return ""
		}
		if m.NameOrder == kube.OrderDesc {
			return sortDesc
		}
		return sortAsc
	}
	if m.SortKey == "" || m.SortOrder == kube.OrderNone {
		return sortBoth
	}
	if m.SortKey != c.key {
		return ""
	}
	if m.SortOrder == kube.OrderDesc {
		return sortDesc
	}
	return sortAsc
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
	Kind      Rect
	Critical  Rect
	Warning   Rect
	DimStatus Rect
	DimCPU    Rect
	DimMemory Rect
	Columns   map[string]Rect
}

func ColumnKeyAt(m Model, width, height, index int) string {
	g := geom(m, width, height)
	if index < 0 || index >= len(g.cols) {
		return ""
	}
	return g.cols[index].key
}

func NextOrder(order kube.Order) kube.Order {
	switch order {
	case kube.OrderNone:
		return kube.OrderDesc
	case kube.OrderDesc:
		return kube.OrderAsc
	}
	return kube.OrderNone
}

func Geometry(m Model, width, height int) Layout {
	g := geom(m, width, height)
	columns := make(map[string]Rect, len(g.cols))
	xs := columnXs(g.widths)
	for i, c := range g.cols {
		columns[c.key] = Rect{X: xs[i], Y: lineHeader, W: g.widths[i], H: 1}
	}

	return Layout{
		Columns:   columns,
		Kind:      Rect{X: g.kindBox.x, Y: g.kindBox.y, W: g.kindBox.w, H: g.kindBox.h},
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
	if g.kindBox.contains(x, y) {
		return HitKindBox, 0
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
	if y == lineHeader && x < g.total {
		xs := columnXs(g.widths)
		for i, c := range g.cols {
			if !c.sortable {
				continue
			}
			if x >= xs[i] && x < xs[i]+g.widths[i] {
				return HitColumn, i
			}
		}
		return HitNone, 0
	}

	if y >= rowTop && y < rowTop+g.room && x < g.total {
		offset := m.Offset
		if offset > len(m.Rows)-g.room {
			offset = len(m.Rows) - g.room
		}
		if offset < 0 {
			offset = 0
		}

		slots := visibleSlots(m, offset, g.room)
		index := y - rowTop
		if index < len(slots) {
			sl := slots[index]
			xs := columnXs(g.widths)
			inName := x >= xs[0] && x < xs[0]+g.widths[0]

			if sl.action < 0 {
				if inName {
					return HitPodName, sl.row
				}
				return HitRow, sl.row
			}
			if inName {
				return actionTarget(m, sl.action), sl.row
			}
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

const (
	inspectLabel    = "[ Inspect ]"
	restartLabel    = "[ Restart ]"
	terminateLabel  = "[ Terminate ]"
	yesLabel        = "[ Yes ]"
	confirmQuestion = "Are you sure?"
	cancelLabel     = "[ Cancel ]"
	actionIndent    = 2
	actionGap       = 2
)

func actionLabel(m Model, line int) (string, tcell.Style) {
	if m.Confirm != ActionNone {
		style := styleWarn
		if m.Confirm == ActionTerminate {
			style = styleBad
		}
		switch line {
		case 0:
			return confirmQuestion, style
		case 1:
			return yesLabel, style.Bold(true)
		}
		return cancelLabel, styleDim
	}

	switch line {
	case 0:
		return inspectLabel, styleChoice
	case 1:
		return restartLabel, styleWarn
	}
	return terminateLabel, styleBad
}

func actionTarget(m Model, line int) Target {
	if m.Confirm != ActionNone {
		switch line {
		case 1:
			return HitConfirmYes
		case 2:
			return HitConfirmCancel
		}
		return HitNone
	}

	switch line {
	case 0:
		return HitActionInspect
	case 1:
		return HitActionRestart
	}
	return HitActionTerminate
}

func drawActions(s tcell.Screen, m Model, g geometry, y, line int) {
	xs := columnXs(g.widths)
	label, style := actionLabel(m, line)
	puts(s, xs[0], y, g.widths[0], false, "  "+truncate(label, g.widths[0]-2), style)

	for i := 1; i < len(g.cols); i++ {
		puts(s, xs[i], y, g.widths[i], false, "", styleBase)
	}
}

func drawTableFrame(s tcell.Screen, g geometry, height int) {
	bottom := height - 3
	dividers := g.dividers()

	border := func(y int, left, fill, cross, right string) {
		puts(s, 0, y, 0, false, left, styleDim)
		for x := 1; x < g.total-1; x++ {
			puts(s, x, y, 0, false, fill, styleDim)
		}
		for _, x := range dividers {
			puts(s, x, y, 0, false, cross, styleDim)
		}
		puts(s, g.total-1, y, 0, false, right, styleDim)
	}

	border(tableTop, "\u250c", "\u2500", "\u252c", "\u2510")
	border(lineRule, "\u251c", "\u2500", "\u253c", "\u2524")
	border(bottom, "\u2514", "\u2500", "\u2534", "\u2518")

	for y := tableTop + 1; y < bottom; y++ {
		if y == lineRule {
			continue
		}
		puts(s, 0, y, 0, false, "\u2502", styleDim)
		puts(s, g.total-1, y, 0, false, "\u2502", styleDim)
		for _, x := range dividers {
			puts(s, x, y, 0, false, "\u2502", styleDim)
		}
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
			style = styleChosen
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
	if m.Screen == ScreenInspect {
		drawInspect(s, m)
		return
	}

	width, height := s.Size()
	g := geom(m, width, height)
	total := g.total
	now := m.Now
	if now.IsZero() {
		now = time.Now()
	}

	clock := now.Format("15:04:05")
	puts(s, max(0, (total-len(clock))/2), lineClock, 0, false, clock, styleAccent)

	if !m.Started.IsZero() {
		uptime := "(" + Uptime(now.Sub(m.Started)) + ")"
		puts(s, max(0, (total-len([]rune(uptime)))/2), lineUptime, 0, false, uptime, styleDim)
	}

	x := 0

	x = puts(s, 0, lineTitle, 0, false, "Using ", styleDim)
	x += puts(s, x, lineTitle, 0, false, m.Namespace, styleAccent.Bold(true))
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

	drawBox(s, g.kindBox, m.Focus == FocusKind)
	puts(s, g.kindBox.x+3, nsBoxTop+1, g.kindBox.w-6, false,
		centerText(m.Kind.String(), g.kindBox.w-6),
		pickStyle(styleChoice, m.Focus == FocusKind))

	puts(s, g.critical.x-len(facingPrefix), lineStatus, 0, false, facingPrefix, styleDim)

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
	puts(s, g.warning.x, lineStatus, 0, false, fmt.Sprintf("%d warnings", warn),
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

	drawTableFrame(s, g, height)

	xs := columnXs(g.widths)
	for i, c := range g.cols {
		header := c.title
		if mark := sortMark(m, c); mark != "" {
			header += " " + mark
		}
		header = centerText(header, g.widths[i])
		style := styleBold
		if m.SortKey == c.key && m.SortOrder != kube.OrderNone {
			style = styleBold.Foreground(tcell.ColorTeal)
		}
		puts(s, xs[i], lineHeader, g.widths[i], false, header, style)
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
		case !m.Loaded:
			empty = "asking the cluster for pods..."
		case m.Level != kube.LevelAll && len(m.All) > 0:
			empty = emptyLevelText(m.Level, m.Dimension)
		case m.PodQuery != "" && len(m.All) > 0:
			empty = "no pod matches " + m.PodQuery
		}
		puts(s, 2, rowTop, 0, false, empty, styleDim)
	}

	for i, sl := range visibleSlots(m, offset, g.room) {
		y := rowTop + i
		if sl.action >= 0 {
			drawActions(s, m, g, y, sl.action)
			continue
		}

		row := m.Rows[sl.row]
		for ci, c := range g.cols {
			text, style := c.value(row)
			if c.key == "last_restart" && !row.LastRestart.IsZero() {
				text = RestartText(row.LastRestart, now)
			}
			if c.key == "created" && !row.Created.IsZero() {
				text = Ago(now.Sub(row.Created)) + " ago"
			}
			if c.key == "name" {
				if row.Name == m.Expanded {
					text = marqueeText(text, g.widths[ci], m.Tick)
					style = styleChoice
				}
			}
			if c.key != "name" {
				text = centerText(text, g.widths[ci])
			}
			puts(s, xs[ci], y, g.widths[ci], false, text, style)
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

const (
	dimensionPrefix = " regarding "
	facingPrefix    = "Facing "
)

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

const marqueePause = 6

func marqueeOffset(tick, overflow int) int {
	if overflow <= 0 {
		return 0
	}
	cycle := 2*marqueePause + 2*overflow - 1
	at := tick % cycle
	if at < 0 {
		at += cycle
	}

	switch {
	case at < marqueePause:
		return 0
	case at < marqueePause+overflow:
		return at - marqueePause + 1
	case at < 2*marqueePause+overflow:
		return overflow
	}
	return overflow - (at - 2*marqueePause - overflow) - 1
}

func marqueeText(text string, width, tick int) string {
	runes := []rune(text)
	overflow := len(runes) - width
	if overflow <= 0 {
		return text
	}
	at := marqueeOffset(tick, overflow)
	return string(runes[at : at+width])
}

func NeedsMarquee(m Model, width, height int) bool {
	if m.Expanded == "" {
		return false
	}
	g := geom(m, width, height)
	return len([]rune(m.Expanded)) > g.widths[0]
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
