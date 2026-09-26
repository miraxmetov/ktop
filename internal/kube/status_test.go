package kube

import "testing"

func TestStatusTextNamesOnlyWhatIsWrong(t *testing.T) {
	status := Row{Severity: Bad, CPUPct: -1, MemPct: -1, Worst: -1}
	cpu := Row{Severity: Good, CPUPct: 95, MemPct: 10, Worst: 95}
	memory := Row{Severity: Good, CPUPct: 10, MemPct: 80, Worst: 80}
	calm := Row{Severity: Good, CPUPct: 10, MemPct: 10, Worst: 10}

	cases := []struct {
		name string
		rows []Row
		want string
	}{
		{"nothing", []Row{calm}, "Nothing is wrong in this namespace."},
		{"two dimensions", []Row{status, memory, calm},
			"Facing 1 critical and 1 warning issues regarding status and memory."},
		{"every dimension", []Row{status, cpu, memory},
			"Facing 2 critical and 1 warning issues regarding status, cpu and memory."},
		{"one warning", []Row{memory, calm}, "Facing 1 warning issue regarding memory."},
		{"one dimension", []Row{cpu, calm}, "Facing 1 critical issue regarding cpu."},
	}

	for _, c := range cases {
		if got := StatusText(c.rows, LevelAll, DimAll); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestStatusTextFollowsTheChosenDimension(t *testing.T) {
	rows := []Row{
		{Severity: Bad, CPUPct: 10, MemPct: 10, Worst: 10},
		{Severity: Good, CPUPct: 95, MemPct: 10, Worst: 95},
	}

	if got := StatusText(rows, LevelAll, DimStatus); got != "Facing 1 critical issue regarding status and cpu." {
		t.Errorf("by status: %q", got)
	}
	if got := StatusText(rows, LevelAll, DimMemory); got != "Facing no issues regarding status, cpu and memory." {
		t.Errorf("a chosen dimension stays on the line even when it is quiet: %q", got)
	}
}

func TestStatusTextKeepsAChosenLevelVisible(t *testing.T) {
	rows := []Row{{Severity: Good, CPUPct: 95, MemPct: 10, Worst: 95}}

	if got := StatusText(rows, LevelWarning, DimAll); got != "Facing 1 critical and 0 warning issues regarding cpu." {
		t.Errorf("the chosen level must stay clickable: %q", got)
	}
}

func TestTroubledDimensionsKeepTheirOrder(t *testing.T) {
	rows := []Row{
		{Severity: Good, CPUPct: 10, MemPct: 80, Worst: 80},
		{Severity: Bad, CPUPct: 10, MemPct: 10, Worst: 10},
	}

	got := TroubledDimensions(rows)
	if len(got) != 2 || got[0] != DimStatus || got[1] != DimMemory {
		t.Errorf("dimensions: %v", got)
	}
}
