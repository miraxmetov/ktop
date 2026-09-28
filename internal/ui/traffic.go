package ui

import (
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/miraxmetov/ktop/internal/kube"
)

type Browse struct {
	Name    string
	Notes   []string
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
	return m.Kind.Group() == kube.GroupTraffic
}

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
	hidden := len(browseBody(m, b.textArea)) - b.lines
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
	return append(out, wrap(m.Browse.Lines(), width)...)
}

func drawBrowse(s tcell.Screen, m Model, g geometry, height int) {
	b := browseGeom(g, height)

	drawPane(s, b.list, kindWord(m.Kind))
	drawPane(s, b.pane, or(m.Browse.Name, "nothing chosen"))

	drawBrowseNames(s, m, b)
	drawBrowseBody(s, m, b)

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

	lines := browseBody(m, b.textArea)
	offset := m.Browse.Offset
	if offset > len(lines)-b.lines {
		offset = len(lines) - b.lines
	}
	if offset < 0 {
		offset = 0
	}

	notes := len(m.Browse.Notes)
	for i := 0; i < b.lines && offset+i < len(lines); i++ {
		index := offset + i
		line := lines[index]

		if index < notes {
			putSegments(s, b.body.x, b.body.y+i, b.textArea, noteSegments(line))
			continue
		}
		putSegments(s, b.body.x, b.body.y+i, b.textArea, configSegments(line, m.Browse.Format))
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
	style := styleBase

	switch strings.TrimSpace(label) {
	case "mind that":
		style = styleWarn
	case "endpoints", "holds", "address":
		style = phraseStyle(strings.TrimSpace(value))
	}
	return []segment{{label, styleDim}, {value, style}}
}

func hitBrowse(m Model, g geometry, height, x, y int) (Target, int) {
	b := browseGeom(g, height)

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
