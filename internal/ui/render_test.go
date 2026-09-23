package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/miraxmetov/ktop/internal/kube"
)

func sample(n int) []kube.Row {
	rows := make([]kube.Row, 0, n)
	for i := 0; i < n; i++ {
		code := int32(137)
		rows = append(rows, kube.Row{
			Name:        fmt.Sprintf("pod-%02d-deployment-with-a-long-suffix", i),
			Status:      "Running",
			Severity:    kube.Good,
			CPU:         940,
			CPULimit:    1000,
			Mem:         1840,
			MemLimit:    1024,
			HasCPU:      true,
			HasMem:      true,
			Restarts:    i,
			OOMs:        i % 2,
			ExitCode:    &code,
			ExitReason:  "OOMKilled",
			LastRestart: time.Now().Add(-4 * time.Minute),
			CPUPct:      94,
			MemPct:      179.7,
			Worst:       179.7,
			Problem:     true,
		})
	}
	return rows
}

func model(rows []kube.Row) Model {
	m := Model{
		Namespace: "production",
		All:       rows,
		Interval:  2 * time.Second,
		Now:       time.Now(),
	}
	ApplyFilter(&m)
	return m
}

func draw(t *testing.T, width, height int, m Model) ([]string, tcell.SimulationScreen) {
	t.Helper()
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("init screen: %v", err)
	}
	screen.SetSize(width, height)
	Draw(screen, m)

	cells, w, h := screen.GetContents()
	lines := make([]string, 0, h)
	for y := 0; y < h; y++ {
		var b strings.Builder
		for x := 0; x < w; x++ {
			runes := cells[y*w+x].Runes
			if len(runes) == 0 {
				b.WriteRune(' ')
				continue
			}
			b.WriteRune(runes[0])
		}
		lines = append(lines, strings.TrimRight(b.String(), " "))
	}
	return lines, screen
}

func TestDrawHeaderShowsNamespaceAndSearchBars(t *testing.T) {
	lines, _ := draw(t, 170, 40, model(sample(3)))

	if lines[lineTitle] != "Current namespace: production" {
		t.Errorf("namespace line: %q", lines[lineTitle])
	}
	if lines[lineNs] != "Search for namespaces..." {
		t.Errorf("namespace search line: %q", lines[lineNs])
	}
	if lines[linePods] != "Search for pods..." {
		t.Errorf("pod search line: %q", lines[linePods])
	}
	if strings.Contains(lines[lineNs]+lines[linePods], "[") {
		t.Error("search fields must not be drawn with brackets")
	}
}

func TestPlaceholdersAreItalic(t *testing.T) {
	_, screen := draw(t, 170, 40, model(sample(2)))
	cells, w, _ := screen.GetContents()

	for _, y := range []int{lineNs, linePods} {
		_, _, attrs := cells[y*w].Style.Decompose()
		if attrs&tcell.AttrItalic == 0 {
			t.Errorf("placeholder on line %d must be italic", y)
		}
	}

	m := model(sample(2))
	m.PodQuery = "api"
	ApplyFilter(&m)
	_, typed := draw(t, 170, 40, m)
	cells, w, _ = typed.GetContents()
	if _, _, attrs := cells[linePods*w].Style.Decompose(); attrs&tcell.AttrItalic != 0 {
		t.Error("typed text must not be italic")
	}
}

func TestDrawWideShowsEveryColumn(t *testing.T) {
	lines, _ := draw(t, 170, 40, model(sample(3)))
	header := lines[lineHeader]

	for _, title := range []string{"POD", "STATUS", "CPU", "%LIM", "MEM", "REST", "OOM", "EXIT", "LAST RESTART"} {
		if !strings.Contains(header, title) {
			t.Errorf("header missing %q: %q", title, header)
		}
	}
	if !strings.Contains(lines[lineStatus], "critical") || !strings.Contains(lines[lineStatus], "warning") {
		t.Errorf("status line: %q", lines[lineStatus])
	}
	if !strings.Contains(lines[rowTop], "pod-00") || !strings.Contains(lines[rowTop], "137 (OOMKilled)") {
		t.Errorf("first row: %q", lines[rowTop])
	}
	if !strings.Contains(lines[39], "q quit") {
		t.Errorf("footer: %q", lines[39])
	}
}

func TestDrawFiltersPodsByQuery(t *testing.T) {
	m := model(sample(10))
	m.PodQuery = "pod-07"
	ApplyFilter(&m)

	lines, _ := draw(t, 170, 40, m)

	if len(m.Rows) != 1 {
		t.Fatalf("want 1 matching row, got %d", len(m.Rows))
	}
	if !strings.Contains(lines[linePods], "pod-07") {
		t.Errorf("query not shown: %q", lines[linePods])
	}
	if !strings.Contains(lines[linePods], "1/10") {
		t.Errorf("match counter missing: %q", lines[linePods])
	}
	if !strings.Contains(lines[rowTop], "pod-07") {
		t.Errorf("filtered row: %q", lines[rowTop])
	}
	if strings.Contains(lines[rowTop+1], "pod-") {
		t.Errorf("only the match must be drawn: %q", lines[rowTop+1])
	}
	if !strings.Contains(lines[lineStatus], "10 critical") {
		t.Errorf("counters must describe the whole namespace: %q", lines[lineStatus])
	}
}

func TestDrawFilterWithoutMatches(t *testing.T) {
	m := model(sample(5))
	m.PodQuery = "nothing-matches"
	ApplyFilter(&m)

	lines, _ := draw(t, 170, 40, m)
	if !strings.Contains(lines[rowTop], "no pod matches nothing-matches") {
		t.Errorf("empty state: %q", lines[rowTop])
	}
}

func TestDrawNamespaceSearchShowsMatches(t *testing.T) {
	m := model(sample(2))
	m.Focus = FocusNamespace
	m.NamespaceQuery = "prod"
	m.Namespaces = []string{"default", "prod-blue", "production", "kube-system"}

	lines, _ := draw(t, 170, 40, m)
	if !strings.Contains(lines[lineNs], "prod") {
		t.Errorf("query not shown: %q", lines[lineNs])
	}
	if !strings.Contains(lines[lineNs+2], "prod-blue") {
		t.Errorf("dropdown must list the first match: %q", lines[lineNs+2])
	}
	if !strings.Contains(lines[lineNs+3], "production") {
		t.Errorf("dropdown must list every match: %q", lines[lineNs+3])
	}
	if strings.Contains(strings.Join(lines[lineNs+1:lineNs+5], " "), "kube-system") {
		t.Error("dropdown must not list namespaces that do not match")
	}
	if !strings.Contains(lines[39], "Enter apply") {
		t.Errorf("footer must explain the input mode: %q", lines[39])
	}
}

func TestDrawBlankLineBetweenSearchAndTable(t *testing.T) {
	lines, _ := draw(t, 170, 40, model(sample(3)))
	if strings.TrimSpace(lines[linePods+1]) != "" {
		t.Errorf("line between the pod search and the table must be empty: %q", lines[linePods+1])
	}
	if !strings.Contains(lines[lineHeader], "POD") {
		t.Errorf("header must follow the blank line: %q", lines[lineHeader])
	}
}

func TestDrawInputBoxFormat(t *testing.T) {
	lines, _ := draw(t, 170, 40, model(sample(3)))

	if lines[linePods] != "Search for pods..." {
		t.Errorf("pod placeholder: %q", lines[linePods])
	}
	if lines[lineNs] != "Search for namespaces..." {
		t.Errorf("namespace placeholder: %q", lines[lineNs])
	}

	m := model(sample(3))
	m.PodQuery = "api"
	ApplyFilter(&m)
	typed, _ := draw(t, 170, 40, m)
	if !strings.HasPrefix(typed[linePods], "api") {
		t.Errorf("typed query must replace the placeholder: %q", typed[linePods])
	}
	if strings.Contains(typed[linePods], "Search for pods") {
		t.Errorf("placeholder must disappear once typing starts: %q", typed[linePods])
	}
}

func TestDrawPodDropdownListsMatches(t *testing.T) {
	m := model(sample(4))
	m.Focus = FocusPods
	m.PodQuery = "pod-0"
	ApplyFilter(&m)

	lines, _ := draw(t, 170, 40, m)
	listed := strings.Join(lines[linePods+1:linePods+6], "\n")
	for _, want := range []string{"pod-00", "pod-01", "pod-02", "pod-03"} {
		if !strings.Contains(listed, want) {
			t.Errorf("dropdown missing %q:\n%s", want, listed)
		}
	}
}

func TestDrawEmptyQueryDropdownListsEverything(t *testing.T) {
	m := model(sample(3))
	m.Focus = FocusPods

	lines, _ := draw(t, 170, 40, m)
	listed := strings.Join(lines[linePods+1:linePods+6], "\n")
	for _, want := range []string{"pod-00", "pod-01", "pod-02"} {
		if !strings.Contains(listed, want) {
			t.Errorf("empty query must list every pod, missing %q:\n%s", want, listed)
		}
	}

	ns := model(sample(1))
	ns.Focus = FocusNamespace
	ns.Namespaces = []string{"default", "production", "staging"}
	nsLines, _ := draw(t, 170, 40, ns)
	listedNs := strings.Join(nsLines[lineNs+1:lineNs+6], "\n")
	for _, want := range []string{"default", "production", "staging"} {
		if !strings.Contains(listedNs, want) {
			t.Errorf("empty query must list every namespace, missing %q:\n%s", want, listedNs)
		}
	}
}

func TestDrawDropdownReportsHiddenItems(t *testing.T) {
	m := model(sample(30))
	m.Focus = FocusPods

	lines, _ := draw(t, 170, 40, m)
	box := strings.Join(lines[linePods+1:linePods+12], "\n")
	if !strings.Contains(box, "more") {
		t.Errorf("dropdown must report hidden items:\n%s", box)
	}
}

func TestHitFindsInputsRowsAndDropdown(t *testing.T) {
	m := model(sample(5))

	if target, _ := Hit(m, 170, 40, 3, lineNs); target != HitNamespaceInput {
		t.Errorf("click on the namespace box: %v", target)
	}
	if target, _ := Hit(m, 170, 40, 3, linePods); target != HitPodInput {
		t.Errorf("click on the pod box: %v", target)
	}
	if target, index := Hit(m, 170, 40, 5, rowTop+2); target != HitRow || index != 2 {
		t.Errorf("click on a row: %v %d", target, index)
	}
	if target, _ := Hit(m, 170, 40, 5, lineTitle); target != HitNone {
		t.Errorf("click on the title must do nothing: %v", target)
	}

	m.Offset = 3
	if _, index := Hit(m, 170, 40, 5, rowTop); index != 3 {
		t.Errorf("scrolled table must map clicks through the offset, got %d", index)
	}

	open := model(sample(5))
	open.Focus = FocusNamespace
	open.Namespaces = []string{"default", "production", "staging"}
	if target, index := Hit(open, 170, 40, 3, lineNs+3); target != HitDropdown || index != 1 {
		t.Errorf("click on a dropdown item: %v %d", target, index)
	}
}

func TestRestartTextSpellsOutTheTime(t *testing.T) {
	now := time.Date(2026, 9, 22, 13, 19, 9, 0, time.Local)
	then := now.Add(-(2*time.Hour + 36*time.Minute))

	if got := RestartText(then, now); got != "2h36m ago (at 10:43:09)" {
		t.Errorf("RestartText = %q", got)
	}
	if got := RestartText(time.Time{}, now); got != "-" {
		t.Errorf("zero time: %q", got)
	}
}

func TestDrawRowShowsRestartText(t *testing.T) {
	rows := sample(1)
	rows[0].LastRestart = time.Now().Add(-2*time.Hour - 36*time.Minute)

	lines, _ := draw(t, 170, 40, model(rows))
	if !strings.Contains(lines[rowTop], "2h36m ago (at ") {
		t.Errorf("row must spell out the restart time: %q", lines[rowTop])
	}
}

func TestDrawNamespaceSearchWithoutPermission(t *testing.T) {
	m := model(sample(2))
	m.Focus = FocusNamespace
	m.NamespaceQuery = "prod"
	m.NamespaceNote = "no permission to list namespaces, type a name and press Enter"

	lines, _ := draw(t, 170, 40, m)
	if !strings.Contains(lines[lineNs], "no permission to list namespaces") {
		t.Errorf("note must be visible: %q", lines[lineNs])
	}
}

func TestDrawNarrowDropsColumnsAndFits(t *testing.T) {
	for _, width := range []int{24, 40, 60, 80, 100, 120, 170, 240} {
		lines, _ := draw(t, width, 30, model(sample(5)))
		for i, line := range lines {
			if len([]rune(line)) > width {
				t.Fatalf("width %d: line %d overflows: %q", width, i, line)
			}
		}
		if !strings.Contains(lines[lineHeader], "POD") {
			t.Errorf("width %d: pod column dropped: %q", width, lines[lineHeader])
		}
		if !strings.Contains(lines[rowTop], "pod-00") {
			t.Errorf("width %d: first row missing: %q", width, lines[rowTop])
		}
	}

	narrow, _ := draw(t, 80, 30, model(sample(5)))
	if strings.Contains(narrow[lineHeader], "LAST RESTART") {
		t.Errorf("80 columns must drop LAST RESTART: %q", narrow[lineHeader])
	}
	if !strings.Contains(narrow[lineHeader], "%LIM") {
		t.Errorf("80 columns must keep %%LIM: %q", narrow[lineHeader])
	}
}

func TestDrawScrollsAndMarksCursor(t *testing.T) {
	m := model(sample(40))
	m.Offset = 10
	m.Cursor = 12

	lines, screen := draw(t, 170, 22, m)

	if !strings.Contains(lines[rowTop], "pod-10") {
		t.Errorf("first visible row should be pod-10: %q", lines[rowTop])
	}
	room := Visible(22)
	last := rowTop + room - 1
	if !strings.Contains(lines[last], fmt.Sprintf("pod-%02d", 10+room-1)) {
		t.Errorf("last visible row: %q", lines[last])
	}
	if !strings.Contains(lines[21], "more") {
		t.Errorf("footer should report hidden rows: %q", lines[21])
	}

	cells, w, _ := screen.GetContents()
	cursorY := rowTop + (12 - 10)
	_, _, attrs := cells[cursorY*w].Style.Decompose()
	if attrs&tcell.AttrReverse == 0 {
		t.Errorf("cursor row %d is not highlighted", cursorY)
	}
	_, _, plain := cells[(cursorY+1)*w].Style.Decompose()
	if plain&tcell.AttrReverse != 0 {
		t.Error("non-cursor row must not be highlighted")
	}
}

func TestDrawErrorAndNote(t *testing.T) {
	m := model(nil)
	m.Err = "no permission to list pods in namespace production"
	lines, _ := draw(t, 120, 20, m)
	if !strings.Contains(lines[lineStatus], "no permission to list pods") {
		t.Errorf("error not shown: %q", lines[lineStatus])
	}

	m = model(sample(2))
	m.Note = "metrics-server unavailable, CPU and MEM hidden"
	lines, _ = draw(t, 120, 20, m)
	if !strings.Contains(lines[lineStatus], "metrics-server unavailable") {
		t.Errorf("note not shown: %q", lines[lineStatus])
	}
}

func TestDrawEmptyNamespace(t *testing.T) {
	lines, _ := draw(t, 120, 20, model(nil))
	if !strings.Contains(lines[rowTop], "no pods in this namespace") {
		t.Errorf("empty state: %q", lines[rowTop])
	}
	if !strings.Contains(lines[lineStatus], "0 critical") {
		t.Errorf("counters with no rows: %q", lines[lineStatus])
	}
}

func TestSelectedName(t *testing.T) {
	m := model(sample(3))
	m.Cursor = 2
	if got := m.SelectedName(); !strings.HasPrefix(got, "pod-02") {
		t.Errorf("SelectedName = %q", got)
	}
	m.Cursor = 99
	if got := m.SelectedName(); got != "" {
		t.Errorf("out of range cursor must give an empty name, got %q", got)
	}
}

func TestExitText(t *testing.T) {
	code := func(v int32) *int32 { return &v }
	cases := []struct {
		code   *int32
		reason string
		want   string
	}{
		{nil, "", "-"},
		{code(0), "Completed", "0 (Completed)"},
		{code(137), "OOMKilled", "137 (OOMKilled)"},
		{code(143), "Error", "143 (SIGTERM)"},
		{code(1), "Error", "1 (Error)"},
		{code(127), "", "127 (NotFound)"},
		{code(200), "", "200 (Error)"},
	}
	for _, c := range cases {
		if got := ExitText(c.code, c.reason); got != c.want {
			t.Errorf("ExitText(%v, %q) = %q, want %q", c.code, c.reason, got, c.want)
		}
	}
}

func TestAgo(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{-time.Second, "0s"},
		{45 * time.Second, "45s"},
		{4*time.Minute + 12*time.Second, "4m12s"},
		{3*time.Hour + 5*time.Minute, "3h5m"},
		{50 * time.Hour, "2d2h"},
	}
	for _, c := range cases {
		if got := Ago(c.d); got != c.want {
			t.Errorf("Ago(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestDrawCounterColumnsAndHint(t *testing.T) {
	lines, screen := draw(t, 170, 40, model(sample(3)))

	if !strings.Contains(lines[lineHeader], "RESTART CTR") {
		t.Errorf("restart column title: %q", lines[lineHeader])
	}
	if !strings.Contains(lines[lineHeader], "OOM CTR") {
		t.Errorf("oom column title: %q", lines[lineHeader])
	}
	if strings.Contains(lines[lineHeader], " REST ") {
		t.Errorf("old restart title still drawn: %q", lines[lineHeader])
	}

	hint := lines[38]
	if !strings.Contains(hint, "since ktop started") {
		t.Errorf("counter hint above the footer: %q", hint)
	}
	if !strings.Contains(hint, "RESTART CTR") || !strings.Contains(hint, "OOM CTR") {
		t.Errorf("hint must name both counters: %q", hint)
	}

	cells, w, _ := screen.GetContents()
	fg, _, _ := cells[38*w].Style.Decompose()
	if fg != tcell.ColorGray {
		t.Errorf("hint must be dim gray, got %v", fg)
	}
	if !strings.Contains(lines[39], "q quit") {
		t.Errorf("footer must stay on the last line: %q", lines[39])
	}
}

func TestDrawRestartCounterShowsTotalAndSessionDelta(t *testing.T) {
	rows := sample(3)
	rows[0].Restarts, rows[0].NewRestarts = 12, 2
	rows[1].Restarts, rows[1].NewRestarts = 5, 0
	rows[2].Restarts, rows[2].NewRestarts = 0, 0

	lines, screen := draw(t, 170, 40, model(rows))

	if !strings.Contains(lines[rowTop], "12 +2") {
		t.Errorf("a pod restarting right now must show total and delta: %q", lines[rowTop])
	}
	if !strings.Contains(lines[rowTop+1], " 5 ") {
		t.Errorf("an old restart shows the total alone: %q", lines[rowTop+1])
	}
	if strings.Contains(lines[rowTop+1], "+") {
		t.Errorf("no delta without new restarts: %q", lines[rowTop+1])
	}

	cells, w, _ := screen.GetContents()
	column := strings.Index(lines[rowTop], "12 +2")
	fg, _, _ := cells[rowTop*w+column].Style.Decompose()
	if fg != tcell.ColorYellow {
		t.Errorf("a live restart must stand out, got colour %v", fg)
	}
}

func TestCounterHintExplainsBothNumbers(t *testing.T) {
	lines, _ := draw(t, 170, 40, model(sample(1)))
	hint := lines[38]

	for _, want := range []string{"RESTART CTR", "own total", "+N", "since ktop started", "OOM CTR"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint must mention %q: %q", want, hint)
		}
	}
}

func TestFooterDropsTheRefreshKey(t *testing.T) {
	lines, _ := draw(t, 170, 40, model(sample(2)))
	footer := lines[39]

	if strings.Contains(footer, "r refresh") {
		t.Errorf("the table refreshes on its own, the hint must be gone: %q", footer)
	}
	if !strings.Contains(footer, "refreshes every 2s") {
		t.Errorf("footer must still state the interval: %q", footer)
	}

	m := model(sample(2))
	m.Focus = FocusPods
	open, _ := draw(t, 170, 40, m)
	if !strings.Contains(open[39], "Tab complete") {
		t.Errorf("input footer must advertise completion: %q", open[39])
	}
	if !strings.Contains(open[39], "click away") {
		t.Errorf("input footer must mention closing by click: %q", open[39])
	}
}
