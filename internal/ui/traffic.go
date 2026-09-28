package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/miraxmetov/ktop/internal/kube"
)

type Link struct {
	Text string
	URL  string
}

type Gauge struct {
	Percent float64
	Label   string
	Note    string
}

type Browse struct {
	Name    string
	Notes   []string
	Links   []Link
	Gauge   *Gauge
	Default []string
	Textual []string
	Yaml    []string
	Format  Format
	Offset  int
	Choice  int
	Loading bool
	Err     string
}

func (b *Browse) Lines() []string {
	switch b.Format {
	case FormatTextual:
		return b.Textual
	case FormatYAML:
		return b.Yaml
	}
	return b.Default
}

func browsing(m Model) bool {
	return browsingKind(m.Kind)
}

func browsingKind(kind kube.Kind) bool {
	switch kind.Group() {
	case kube.GroupTraffic, kube.GroupStorage:
		return true
	}
	return false
}

const (
	gaugeInner  = 8
	gaugeRows   = 4
	gaugeWidth  = gaugeInner + 2
	gaugeHeight = gaugeRows + 3
)

type browseGeometry struct {
	list     rect
	pane     rect
	names    rect
	body     rect
	dflt     rect
	textual  rect
	yaml     rect
	copy     rect
	room     int
	lines    int
	textArea int
}

func browseGeom(g geometry, height int) browseGeometry {
	bottom := height - 3
	paneHeight := bottom - tableTop + 1
	if paneHeight < 5 {
		paneHeight = 5
	}

	listWidth := g.total / 4
	if listWidth < 18 {
		listWidth = 18
	}
	if listWidth > g.total-24 {
		listWidth = max(18, g.total-24)
	}
	paneWidth := g.total - listWidth - 1

	list := rect{x: 0, y: tableTop, w: listWidth, h: paneHeight}
	pane := rect{x: listWidth + 1, y: tableTop, w: paneWidth, h: paneHeight}

	buttons := len(defaultLabel) + 2 + len(textualLabel) + 2 + len(yamlLabel) + 3 + len(copyBodyLabel)
	buttonsX := pane.x + max(0, (pane.w-buttons)/2)
	buttonsY := height - 2

	return browseGeometry{
		list:     list,
		pane:     pane,
		names:    rect{x: list.x + 1, y: list.y + 1, w: list.w - 2, h: paneHeight - 2},
		body:     rect{x: pane.x + 2, y: pane.y + 1, w: pane.w - 4, h: paneHeight - 2},
		dflt:     rect{x: buttonsX, y: buttonsY, w: len(defaultLabel), h: 1},
		textual:  rect{x: buttonsX + len(defaultLabel) + 2, y: buttonsY, w: len(textualLabel), h: 1},
		yaml:     rect{x: buttonsX + len(defaultLabel) + 2 + len(textualLabel) + 2, y: buttonsY, w: len(yamlLabel), h: 1},
		copy:     rect{x: buttonsX + len(defaultLabel) + 2 + len(textualLabel) + 2 + len(yamlLabel) + 3, y: buttonsY, w: len(copyBodyLabel), h: 1},
		room:     paneHeight - 2,
		lines:    paneHeight - 2,
		textArea: pane.w - 4,
	}
}

func BrowseRoom(m Model, width, height int) int {
	return browseGeom(geom(m, width, height), height).room
}

func MaxBrowseOffset(m Model, width, height int) int {
	b := browseGeom(geom(m, width, height), height)
	hidden := len(browseBody(m, bodyWidth(m, b))) - b.lines
	if hidden < 0 {
		return 0
	}
	return hidden
}

func browseBody(m Model, width int) []string {
	out := make([]string, 0, 32)
	if m.Browse.Err != "" {
		return append(out, m.Browse.Err)
	}
	if m.Browse.Loading {
		return append(out, "loading "+m.Browse.Name+"...")
	}

	if len(m.Browse.Notes) > 0 {
		out = append(out, m.Browse.Notes...)
		out = append(out, "")
	}
	for _, link := range m.Browse.Links {
		out = append(out, linkLine(link))
	}
	if len(m.Browse.Links) > 0 {
		out = append(out, "")
	}
	return append(out, wrap(m.Browse.Lines(), width)...)
}

func linkLine(link Link) string {
	return "  " + openLabel + "  " + link.URL
}

func browseHead(m Model) int {
	head := len(m.Browse.Notes)
	if head > 0 {
		head++
	}
	if len(m.Browse.Links) > 0 {
		head += len(m.Browse.Links) + 1
	}
	return head
}

func drawBrowse(s tcell.Screen, m Model, g geometry, height int) {
	b := browseGeom(g, height)

	drawPane(s, b.list, kindWord(m.Kind))
	drawPane(s, b.pane, or(m.Browse.Name, "nothing chosen"))

	drawBrowseNames(s, m, b)
	drawBrowseBody(s, m, b)
	drawGauge(s, m, b)

	puts(s, b.dflt.x, b.dflt.y, 0, false, defaultLabel, pickStyle(styleChoice, m.Browse.Format == FormatDefault))
	puts(s, b.textual.x, b.textual.y, 0, false, textualLabel, pickStyle(styleChoice, m.Browse.Format == FormatTextual))
	puts(s, b.yaml.x, b.yaml.y, 0, false, yamlLabel, pickStyle(styleChoice, m.Browse.Format == FormatYAML))
	copyText, copyStyle := copyLabelFor(m)
	puts(s, b.copy.x, b.copy.y, 0, false, copyText, copyStyle)
}

func drawBrowseNames(s tcell.Screen, m Model, b browseGeometry) {
	if len(m.Rows) == 0 {
		puts(s, b.names.x+1, b.names.y, b.names.w-1, false, "nothing here", styleDim)
		return
	}

	offset := browseOffset(m, b.room)
	for i := 0; i < b.room && offset+i < len(m.Rows); i++ {
		row := m.Rows[offset+i]
		style := severityStyle(row.Severity)
		if offset+i == m.Browse.Choice {
			style = style.Reverse(true).Bold(true)
		}
		puts(s, b.names.x, b.names.y+i, b.names.w, false, " "+truncate(row.Name, b.names.w-1), style)
	}

	if above := offset; above > 0 {
		puts(s, b.list.x+2, b.list.y, 0, false, " "+arrowUp+itoa(above)+" more ", styleDim)
	}
	if below := len(m.Rows) - offset - b.room; below > 0 {
		puts(s, b.list.x+2, b.list.y+b.list.h-1, 0, false, " "+arrowDown+itoa(below)+" more ", styleDim)
	}
}

func browseOffset(m Model, room int) int {
	offset := m.Offset
	if m.Browse.Choice < offset {
		offset = m.Browse.Choice
	}
	if m.Browse.Choice >= offset+room {
		offset = m.Browse.Choice - room + 1
	}
	if offset > len(m.Rows)-room {
		offset = len(m.Rows) - room
	}
	if offset < 0 {
		offset = 0
	}
	return offset
}

func drawBrowseBody(s tcell.Screen, m Model, b browseGeometry) {
	if len(m.Rows) == 0 {
		puts(s, b.body.x, b.body.y, b.textArea, false, "nothing to read", styleDim)
		return
	}
	if m.Browse.Err != "" {
		puts(s, b.body.x, b.body.y, b.textArea, false, truncate(m.Browse.Err, b.textArea), styleBad)
		return
	}

	lines := browseBody(m, bodyWidth(m, b))
	offset := m.Browse.Offset
	if offset > len(lines)-b.lines {
		offset = len(lines) - b.lines
	}
	if offset < 0 {
		offset = 0
	}

	notes := len(m.Browse.Notes)
	firstLink := notes
	if notes > 0 {
		firstLink++
	}

	for i := 0; i < b.lines && offset+i < len(lines); i++ {
		index := offset + i
		line := lines[index]

		room := b.textArea
		if i < gaugeHeight {
			room = bodyWidth(m, b)
		}

		switch {
		case index < notes:
			putSegments(s, b.body.x, b.body.y+i, room, noteSegments(line))
		case index >= firstLink && index < firstLink+len(m.Browse.Links):
			putSegments(s, b.body.x, b.body.y+i, room, linkSegments(line))
		default:
			putSegments(s, b.body.x, b.body.y+i, room, configSegments(line, m.Browse.Format))
		}
	}

	if above := offset; above > 0 {
		puts(s, b.pane.x+2, b.pane.y, 0, false, " "+arrowUp+itoa(above)+" more ", styleDim)
	}
	if below := len(lines) - offset - b.lines; below > 0 {
		puts(s, b.pane.x+2, b.pane.y+b.pane.h-1, 0, false, " "+arrowDown+itoa(below)+" more ", styleDim)
	}
}

const noteColumn = 17

func noteSegments(line string) []segment {
	if len(line) <= noteColumn {
		return []segment{{line, styleDim}}
	}

	label, value := line[:noteColumn], line[noteColumn:]
	name := strings.TrimSpace(label)

	style := valueStyle(name, value)
	if name == "mind that" {
		style = styleWarn
	}
	return []segment{{label, styleDim}, {value, style}}
}

func gaugeFits(m Model, b browseGeometry) bool {
	return m.Browse.Gauge != nil && b.pane.w >= gaugeWidth+24 && b.pane.h >= gaugeHeight+2
}

func bodyWidth(m Model, b browseGeometry) int {
	if !gaugeFits(m, b) {
		return b.textArea
	}
	return max(20, b.textArea-gaugeWidth-2)
}

func drawGauge(s tcell.Screen, m Model, b browseGeometry) {
	if !gaugeFits(m, b) {
		return
	}
	gauge := m.Browse.Gauge

	x := b.pane.x + b.pane.w - gaugeWidth - 2
	y := b.pane.y + 1
	style := pctStyle(gauge.Percent)

	puts(s, x, y, 0, false, "\u250c"+repeat("\u2500", gaugeInner)+"\u2510", styleDim)
	for row := 0; row < gaugeRows; row++ {
		filled := fillOf(gauge.Percent, row)
		puts(s, x, y+1+row, 0, false, "\u2502", styleDim)
		puts(s, x+1, y+1+row, gaugeInner, false, repeat(filled, gaugeInner), style)
		puts(s, x+gaugeInner+1, y+1+row, 0, false, "\u2502", styleDim)
	}
	puts(s, x, y+gaugeRows+1, 0, false, "\u2514"+repeat("\u2500", gaugeInner)+"\u2518", styleDim)

	reading := "  -  "
	if gauge.Percent >= 0 {
		reading = fmt.Sprintf("%.0f%%", gauge.Percent)
	}
	puts(s, x, y+gaugeRows+2, gaugeWidth, false, centerText(reading, gaugeWidth), style.Bold(true))
}

func fillOf(percent float64, row int) string {
	if percent < 0 {
		return " "
	}

	step := 100.0 / gaugeRows
	bottom := float64(gaugeRows-1-row) * step
	share := (percent - bottom) / step

	switch {
	case share >= 0.85:
		return "\u2588"
	case share >= 0.55:
		return "\u2593"
	case share >= 0.25:
		return "\u2592"
	case share > 0:
		return "\u2591"
	}
	return " "
}

func linkSegments(line string) []segment {
	at := strings.Index(line, openLabel)
	if at < 0 {
		return []segment{{line, styleBase}}
	}

	rest := line[at+len(openLabel):]
	return []segment{
		{line[:at], styleDim},
		{openLabel, styleChoice},
		{rest, styleAccent},
	}
}

func browseLinkAt(m Model, b browseGeometry, x, y int) (int, bool) {
	if len(m.Browse.Links) == 0 || !b.body.contains(x, y) {
		return 0, false
	}

	lines := browseBody(m, bodyWidth(m, b))
	offset := m.Browse.Offset
	if offset > len(lines)-b.lines {
		offset = len(lines) - b.lines
	}
	if offset < 0 {
		offset = 0
	}

	index := offset + y - b.body.y
	first := len(m.Browse.Notes)
	if first > 0 {
		first++
	}
	if index < first || index >= first+len(m.Browse.Links) {
		return 0, false
	}

	column := x - b.body.x
	if column < 2 || column >= 2+len([]rune(openLabel)) {
		return 0, false
	}
	return index - first, true
}

func hitBrowse(m Model, g geometry, height, x, y int) (Target, int) {
	b := browseGeom(g, height)

	if index, found := browseLinkAt(m, b, x, y); found {
		return HitOpenLink, index
	}

	switch {
	case b.dflt.contains(x, y):
		return HitFormatDefault, 0
	case b.textual.contains(x, y):
		return HitFormatTextual, 0
	case b.yaml.contains(x, y):
		return HitFormatYAML, 0
	case b.copy.contains(x, y):
		return HitCopyBody, 0
	case b.names.contains(x, y):
		index := browseOffset(m, b.room) + y - b.names.y
		if index >= 0 && index < len(m.Rows) {
			return HitBrowseName, index
		}
		return HitNone, 0
	case b.pane.contains(x, y):
		return HitBrowsePane, 0
	}
	return HitNone, 0
}
