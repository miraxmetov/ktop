package kube

import (
	"fmt"
	"sort"
)

type StatusKind int

const (
	StatusPlain StatusKind = iota
	StatusCritical
	StatusWarning
	StatusDimension
)

type StatusPart struct {
	Text      string
	Kind      StatusKind
	Dimension Dimension
}

func StatusLine(rows []Row, level Level, dimension Dimension) []StatusPart {
	crit, warn := Count(rows, dimension)
	dims := TroubledDimensions(rows)
	if dimension != DimAll && !holds(dims, dimension) {
		dims = append(dims, dimension)
		sortDimensions(dims)
	}

	showCrit := crit > 0 || level == LevelCritical
	showWarn := warn > 0 || level == LevelWarning
	if !showCrit && !showWarn && len(dims) == 0 {
		return []StatusPart{{Text: "Nothing is wrong in this namespace."}}
	}

	parts := []StatusPart{{Text: "Facing "}}
	if showCrit {
		parts = append(parts, StatusPart{Text: fmt.Sprintf("%d critical", crit), Kind: StatusCritical})
	}
	if showCrit && showWarn {
		parts = append(parts, StatusPart{Text: " and "})
	}
	if showWarn {
		parts = append(parts, StatusPart{Text: fmt.Sprintf("%d warning", warn), Kind: StatusWarning})
	}
	if !showCrit && !showWarn {
		parts = append(parts, StatusPart{Text: "no"})
	}

	issue := " issues regarding "
	if showCrit != showWarn && crit+warn == 1 {
		issue = " issue regarding "
	}
	parts = append(parts, StatusPart{Text: issue})

	for i, d := range dims {
		switch {
		case i > 0 && i == len(dims)-1:
			parts = append(parts, StatusPart{Text: " and "})
		case i > 0:
			parts = append(parts, StatusPart{Text: ", "})
		}
		parts = append(parts, StatusPart{Text: d.String(), Kind: StatusDimension, Dimension: d})
	}
	return append(parts, StatusPart{Text: "."})
}

func holds(dims []Dimension, want Dimension) bool {
	for _, d := range dims {
		if d == want {
			return true
		}
	}
	return false
}

func sortDimensions(dims []Dimension) {
	sort.Slice(dims, func(i, j int) bool { return dims[i] < dims[j] })
}

func StatusText(rows []Row, level Level, dimension Dimension) string {
	text := ""
	for _, part := range StatusLine(rows, level, dimension) {
		text += part.Text
	}
	return text
}

func TroubledDimensions(rows []Row) []Dimension {
	out := make([]Dimension, 0, 3)
	for _, d := range Dimensions() {
		if crit, warn := Count(rows, d); crit+warn > 0 {
			out = append(out, d)
		}
	}
	return out
}
