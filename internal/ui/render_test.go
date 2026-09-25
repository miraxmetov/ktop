package ui

import (
	"fmt"
	"os"
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

	if !strings.HasPrefix(lines[lineTitle], "Using production namespace") {
		t.Errorf("namespace line: %q", lines[lineTitle])
	}
	if !strings.HasPrefix(lines[lineNs], "\u2502 Search namespaces...") {
		t.Errorf("namespace search line: %q", lines[lineNs])
	}
	if !strings.HasPrefix(lines[linePods], "\u2502 Search for pods...") {
		t.Errorf("pod search line: %q", lines[linePods])
	}
	for _, y := range []int{nsBoxTop, podBoxTop} {
		if !strings.HasPrefix(lines[y], "\u250c") || !strings.HasSuffix(lines[y], "\u2510") {
			t.Errorf("line %d must be the top of a frame: %q", y, lines[y])
		}
	}
	for _, y := range []int{nsBoxTop + 2, podBoxTop + 2} {
		if !strings.HasPrefix(lines[y], "\u2514") || !strings.HasSuffix(lines[y], "\u2518") {
			t.Errorf("line %d must be the bottom of a frame: %q", y, lines[y])
		}
	}
	if len(lines[lineNs]) >= len(lines[linePods]) {
		t.Errorf("the namespace frame must be the narrower one: %d vs %d",
			len(lines[lineNs]), len(lines[linePods]))
	}
}

func TestPlaceholdersAreItalic(t *testing.T) {
	_, screen := draw(t, 170, 40, model(sample(2)))
	cells, w, _ := screen.GetContents()

	for _, y := range []int{lineNs, linePods} {
		_, _, attrs := cells[y*w+2].Style.Decompose()
		if attrs&tcell.AttrItalic == 0 {
			t.Errorf("placeholder on line %d must be italic", y)
		}
	}

	m := model(sample(2))
	m.PodQuery = "api"
	ApplyFilter(&m)
	_, typed := draw(t, 170, 40, m)
	cells, w, _ = typed.GetContents()
	if _, _, attrs := cells[linePods*w+2].Style.Decompose(); attrs&tcell.AttrItalic != 0 {
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
	if !strings.Contains(lines[39], "[Q] quit") {
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
	if !strings.Contains(lines[nsBoxTop+4], "prod-blue") {
		t.Errorf("dropdown must list the first match: %q", lines[nsBoxTop+4])
	}
	if !strings.Contains(lines[nsBoxTop+5], "production") {
		t.Errorf("dropdown must list every match: %q", lines[nsBoxTop+5])
	}
	if strings.Contains(strings.Join(lines[nsBoxTop+3:nsBoxTop+7], " "), "kube-system") {
		t.Error("dropdown must not list namespaces that do not match")
	}
	if !strings.Contains(lines[39], "[Enter] apply") {
		t.Errorf("footer must explain the input mode: %q", lines[39])
	}
}

func TestFrameBottomSeparatesSearchFromTable(t *testing.T) {
	lines, _ := draw(t, 170, 40, model(sample(3)))
	if !strings.HasPrefix(lines[podBoxTop+2], "\u2514") {
		t.Errorf("the pod frame must close right above the header: %q", lines[podBoxTop+2])
	}
	if !strings.Contains(lines[lineHeader], "POD") {
		t.Errorf("header must follow the frame: %q", lines[lineHeader])
	}
}

func TestDrawInputBoxFormat(t *testing.T) {
	lines, _ := draw(t, 170, 40, model(sample(3)))

	if !strings.Contains(lines[linePods], "Search for pods...") {
		t.Errorf("pod placeholder: %q", lines[linePods])
	}
	if !strings.Contains(lines[lineNs], "Search namespaces...") {
		t.Errorf("namespace placeholder: %q", lines[lineNs])
	}

	m := model(sample(3))
	m.PodQuery = "api"
	ApplyFilter(&m)
	typed, _ := draw(t, 170, 40, m)
	if !strings.Contains(typed[linePods], "\u2502 api") {
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
	listed := strings.Join(lines[podBoxTop+3:podBoxTop+9], "\n")
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
	listed := strings.Join(lines[podBoxTop+3:podBoxTop+8], "\n")
	for _, want := range []string{"pod-00", "pod-01", "pod-02"} {
		if !strings.Contains(listed, want) {
			t.Errorf("empty query must list every pod, missing %q:\n%s", want, listed)
		}
	}

	ns := model(sample(1))
	ns.Focus = FocusNamespace
	ns.Namespaces = []string{"default", "production", "staging"}
	nsLines, _ := draw(t, 170, 40, ns)
	listedNs := strings.Join(nsLines[nsBoxTop+3:nsBoxTop+8], "\n")
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
	box := strings.Join(lines[podBoxTop+3:podBoxTop+14], "\n")
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
	if target, index := Hit(open, 170, 40, 3, nsBoxTop+5); target != HitDropdown || index != 1 {
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
	for x := 0; x < 40; x++ {
		if _, bg, _ := cells[cursorY*w+x].Style.Decompose(); bg != selectedColor {
			t.Fatalf("selected row must be shaded across its full width, column %d has %v", x, bg)
		}
	}
	if _, bg, _ := cells[(cursorY+1)*w].Style.Decompose(); bg == selectedColor {
		t.Error("only the selected row may be shaded")
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

func TestDrawCounterColumns(t *testing.T) {
	lines, _ := draw(t, 170, 40, model(sample(3)))

	if !strings.Contains(lines[lineHeader], "RESTART CTR") {
		t.Errorf("restart column title: %q", lines[lineHeader])
	}
	if !strings.Contains(lines[lineHeader], "OOM CTR") {
		t.Errorf("oom column title: %q", lines[lineHeader])
	}
	if strings.Contains(strings.Join(lines, "\n"), "since ktop started") {
		t.Error("the counter hint line must be gone")
	}
	if !strings.Contains(lines[39], "[Q] quit") {
		t.Errorf("footer must stay on the last line: %q", lines[39])
	}
}

func TestDrawRestartCounterShowsTotalAndSessionDelta(t *testing.T) {
	rows := sample(3)
	rows[0].Restarts, rows[0].NewRestarts = 0, 0
	rows[1].Restarts, rows[1].NewRestarts = 12, 2
	rows[2].Restarts, rows[2].NewRestarts = 5, 0

	lines, screen := draw(t, 170, 40, model(rows))

	if !strings.Contains(lines[rowTop+1], "12 +2") {
		t.Errorf("a pod restarting right now must show total and delta: %q", lines[rowTop+1])
	}
	if !strings.Contains(lines[rowTop+2], " 5 ") {
		t.Errorf("an old restart shows the total alone: %q", lines[rowTop+2])
	}
	if strings.Contains(lines[rowTop+2], "+") {
		t.Errorf("no delta without new restarts: %q", lines[rowTop+2])
	}

	cells, w, _ := screen.GetContents()
	column := strings.Index(lines[rowTop+1], "12 +2")
	fg, _, _ := cells[(rowTop+1)*w+column].Style.Decompose()
	if fg != warnColor {
		t.Errorf("a live restart must stand out, got colour %v", fg)
	}
}

func TestFooterDropsTheRefreshKey(t *testing.T) {
	lines, _ := draw(t, 170, 40, model(sample(2)))
	footer := lines[39]

	for _, gone := range []string{"r refresh", "refreshes every"} {
		if strings.Contains(footer, gone) {
			t.Errorf("footer must not mention refreshing: %q", footer)
		}
	}

	m := model(sample(2))
	m.Focus = FocusPods
	open, _ := draw(t, 170, 40, m)
	if !strings.Contains(open[39], "[Tab] complete") {
		t.Errorf("input footer must advertise completion: %q", open[39])
	}
	if !strings.Contains(open[39], "[Esc] close") {
		t.Errorf("input footer must mention closing: %q", open[39])
	}
}

func TestDrawShowsKubeconfigPathOnTheTitleLine(t *testing.T) {
	m := model(sample(2))
	m.Kubeconfig = "/Users/somebody/.kube/prod.yaml"

	lines, screen := draw(t, 170, 40, m)
	title := lines[lineTitle]

	if !strings.HasPrefix(title, "Using production namespace") {
		t.Errorf("namespace must stay on the left: %q", title)
	}
	if !strings.HasSuffix(title, "/.kube/prod.yaml") {
		t.Errorf("kubeconfig path must sit on the right: %q", title)
	}

	cells, w, _ := screen.GetContents()
	column := strings.Index(title, "/Users")
	fg, _, attrs := cells[lineTitle*w+column].Style.Decompose()
	if fg != tcell.ColorTeal || attrs&tcell.AttrUnderline == 0 {
		t.Errorf("the path must look clickable, got colour %v attrs %v", fg, attrs)
	}
}

func TestShortPathUsesTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got := ShortPath(home + "/.kube/config"); got != "~/.kube/config" {
		t.Errorf("ShortPath = %q", got)
	}
	if got := ShortPath("/etc/kubeconfig"); got != "/etc/kubeconfig" {
		t.Errorf("ShortPath = %q", got)
	}
	if got := ShortPath(""); got != "no kubeconfig" {
		t.Errorf("ShortPath = %q", got)
	}
}

func TestDrawKubeconfigInputAndItsDropdown(t *testing.T) {
	m := model(sample(2))
	m.Kubeconfig = "/home/u/.kube/config"
	m.Focus = FocusKubeconfig
	m.PathQuery = "/home/u/.kube/"
	m.PathOptions = []string{"/home/u/.kube/cache/", "/home/u/.kube/config", "/home/u/.kube/prod.yaml"}

	lines, _ := draw(t, 170, 40, m)

	if !strings.Contains(lines[lineTitle], "/home/u/.kube/") {
		t.Errorf("typed path must be shown: %q", lines[lineTitle])
	}
	listed := strings.Join(lines[lineTitle+2:lineTitle+7], "\n")
	for _, want := range []string{"cache/", "config", "prod.yaml"} {
		if !strings.Contains(listed, want) {
			t.Errorf("dropdown missing %q:\n%s", want, listed)
		}
	}
	if strings.Contains(listed, "/home/u/.kube/config") {
		t.Errorf("the list must show names, not full paths:\n%s", listed)
	}
}

func TestHitFindsTheKubeconfigPath(t *testing.T) {
	m := model(sample(2))
	m.Kubeconfig = "/home/u/.kube/config"

	lines, _ := draw(t, 170, 40, m)
	column := strings.Index(lines[lineTitle], "/home")
	if column < 0 {
		t.Fatalf("path not drawn: %q", lines[lineTitle])
	}
	if target, _ := Hit(m, 170, 40, column+3, lineTitle); target != HitKubeconfig {
		t.Errorf("click on the path: %v", target)
	}
	if target, _ := Hit(m, 170, 40, 2, lineTitle); target != HitNone {
		t.Errorf("click on the namespace label must do nothing: %v", target)
	}
}

func TestTableFillsTheTerminalWidth(t *testing.T) {
	for _, width := range []int{120, 170, 220, 300} {
		lines, _ := draw(t, width, 30, model(sample(4)))

		rule := lines[lineRule]
		if len([]rune(rule)) < width-1 {
			t.Errorf("width %d: the table stops at %d columns", width, len([]rune(rule)))
		}
		if len([]rune(rule)) > width {
			t.Errorf("width %d: the table overflows to %d columns", width, len([]rune(rule)))
		}
	}
}

func TestSelectedRowIsShadedToTheScreenEdge(t *testing.T) {
	m := model(sample(4))
	m.Cursor = 1

	width := 220
	_, screen := draw(t, width, 30, m)
	cells, w, _ := screen.GetContents()
	y := rowTop + 1

	for x := 0; x < width-1; x++ {
		if _, bg, _ := cells[y*w+x].Style.Decompose(); bg != selectedColor {
			t.Fatalf("column %d of the selected row is not shaded", x)
		}
	}
}

func TestFooterSpansTheWholeWidth(t *testing.T) {
	for _, width := range []int{100, 150, 200, 260} {
		lines, _ := draw(t, width, 30, model(sample(4)))
		footer := lines[29]

		if got := len([]rune(footer)); got != width-1 {
			t.Errorf("width %d: footer is %d columns wide", width, got)
		}
		for _, want := range []string{"[/] search pods", "[N] namespace", "[C] kubeconfig", "click to focus", "[Q] quit"} {
			if !strings.Contains(footer, want) {
				t.Errorf("width %d: footer missing %q: %q", width, want, footer)
			}
		}
	}
}

func TestFooterSpacingIsEven(t *testing.T) {
	lines, _ := draw(t, 200, 30, model(sample(2)))
	footer := lines[29]

	if strings.Contains(footer, dot) {
		t.Errorf("items must be separated by space alone: %q", footer)
	}

	labels := []string{"[/] search pods", "[N] namespace", "[C] kubeconfig", "click to focus", "[Q] quit"}
	gaps := []int{}
	for i := 0; i < len(labels)-1; i++ {
		from := strings.Index(footer, labels[i]) + len(labels[i])
		to := strings.Index(footer, labels[i+1])
		if from < 0 || to < from {
			t.Fatalf("labels out of order in %q", footer)
		}
		gaps = append(gaps, to-from)
	}
	for _, gap := range gaps {
		if gap < gaps[0]-1 || gap > gaps[0]+1 {
			t.Errorf("gaps must be spread evenly, got %v", gaps)
		}
	}
	if gaps[0] < 5 {
		t.Errorf("a wide terminal must stretch the gaps, got %v", gaps)
	}
}

func TestFooterFallsBackOnNarrowTerminals(t *testing.T) {
	for _, width := range []int{30, 50, 70} {
		lines, _ := draw(t, width, 30, model(sample(2)))
		if got := len([]rune(lines[29])); got > width {
			t.Errorf("width %d: footer overflows to %d columns", width, got)
		}
	}
}

func TestInputFooterAlsoSpansTheWidth(t *testing.T) {
	m := model(sample(2))
	m.Focus = FocusPods

	lines, _ := draw(t, 180, 30, m)
	footer := lines[29]
	if got := len([]rune(footer)); got != 179 {
		t.Errorf("input footer is %d columns wide", got)
	}
	if !strings.Contains(footer, "[Tab] complete") || !strings.Contains(footer, "[Shift+Tab] switch field") {
		t.Errorf("input footer: %q", footer)
	}
}

func TestClockSitsCenteredOnItsOwnLine(t *testing.T) {
	m := model(sample(3))
	m.Now = time.Date(2026, 9, 23, 16, 58, 8, 0, time.Local)
	m.Started = m.Now.Add(-5 * time.Minute)

	for _, width := range []int{100, 170, 240} {
		lines, _ := draw(t, width, 30, m)
		clock := lines[lineClock]

		if strings.TrimSpace(clock) != "16:58:08  (5m 0s)" {
			t.Errorf("width %d: the top line must hold the clock and the timer: %q", width, clock)
		}
		left := len(clock) - len(strings.TrimLeft(clock, " "))
		right := (width - 1) - len(clock)
		if left < right-1 || left > right+1 {
			t.Errorf("width %d: clock is not centred, %d left and %d right", width, left, right)
		}
		if strings.Contains(lines[lineStatus], "16:58:08") {
			t.Errorf("the clock must not stay on the status line: %q", lines[lineStatus])
		}
	}
}

func TestStatusCountersAreCentredAndColoured(t *testing.T) {
	m := model(sample(6))
	width := 170

	lines, screen := draw(t, width, 30, m)
	status := lines[lineStatus]

	block := "6 critical / 0 warning   issues regarding status / cpu / memory"
	start := strings.Index(status, block)
	if start < 0 {
		t.Fatalf("status line: %q", status)
	}
	left, right := start, (width-1)-(start+len(block))
	if left < right-1 || left > right+1 {
		t.Errorf("the status group is not centred, %d left and %d right", left, right)
	}

	cells, w, _ := screen.GetContents()
	if fg, _, _ := cells[lineStatus*w+start].Style.Decompose(); fg != tcell.ColorRed {
		t.Errorf("critical must be red, got %v", fg)
	}
	warnColumn := start + strings.Index(block, "0 warning")
	if fg, _, _ := cells[lineStatus*w+warnColumn].Style.Decompose(); fg != warnColor {
		t.Errorf("warning must keep its amber colour even at zero, got %v", fg)
	}
}

func TestStatusMessageKeepsItsPlaceLeftOfTheCounters(t *testing.T) {
	m := model(sample(2))
	m.Note = "metrics-server unavailable, CPU and MEM hidden"

	lines, _ := draw(t, 170, 30, m)
	status := lines[lineStatus]

	if !strings.HasPrefix(status, "metrics-server unavailable") {
		t.Errorf("the note belongs on the left: %q", status)
	}
	if !strings.Contains(status, "critical") {
		t.Errorf("counters must stay visible: %q", status)
	}
}

func TestCounterColumnsAreCentred(t *testing.T) {
	rows := sample(1)
	rows[0].Restarts, rows[0].NewRestarts, rows[0].OOMs = 7, 0, 3

	lines, _ := draw(t, 170, 30, model(rows))
	header, row := lines[lineHeader], lines[rowTop]

	for _, c := range []struct {
		title string
		value string
	}{{"RESTART CTR", "7"}, {"OOM CTR", "3"}} {
		titleStart := strings.Index(header, c.title)
		if titleStart < 0 {
			t.Fatalf("column %q missing from %q", c.title, header)
		}
		width := len(c.title)
		field := row[titleStart : titleStart+width]
		value := strings.TrimSpace(field)
		if value != c.value {
			t.Fatalf("column %q holds %q", c.title, field)
		}
		left := strings.Index(field, c.value)
		right := width - left - len(c.value)
		if left < right-1 || left > right+1 {
			t.Errorf("%q is not centred in its column: %q (%d left, %d right)", c.value, field, left, right)
		}
	}
}

func levelled() Model {
	rows := sample(5)
	rows[0].Worst, rows[0].CPUPct, rows[0].MemPct = 95, 95, 40
	rows[1].Worst, rows[1].CPUPct, rows[1].MemPct = 92, 92, 30
	rows[2].Worst, rows[2].CPUPct, rows[2].MemPct = 80, 80, 20
	rows[3].Worst, rows[3].CPUPct, rows[3].MemPct = 10, 10, 10
	rows[4].Worst, rows[4].CPUPct, rows[4].MemPct = -1, -1, -1
	m := model(rows)
	return m
}

func TestLevelFilterShowsOnlyMatchingPods(t *testing.T) {
	m := levelled()
	if crit, warn := m.Counts(); crit != 2 || warn != 1 {
		t.Fatalf("counters: %d critical, %d warning", crit, warn)
	}

	m.Level = kube.LevelCritical
	ApplyFilter(&m)
	if len(m.Rows) != 2 {
		t.Fatalf("critical filter must keep 2 rows, got %d", len(m.Rows))
	}

	lines, _ := draw(t, 170, 30, m)
	if !strings.Contains(lines[rowTop], "pod-00") || !strings.Contains(lines[rowTop+1], "pod-01") {
		t.Errorf("filtered rows: %q / %q", lines[rowTop], lines[rowTop+1])
	}
	if strings.Contains(lines[rowTop+2], "pod-") {
		t.Errorf("only critical pods may be drawn: %q", lines[rowTop+2])
	}
	if !strings.Contains(lines[lineStatus], "2 critical") {
		t.Errorf("counters must keep describing the namespace: %q", lines[lineStatus])
	}

	m.Level = kube.LevelWarning
	ApplyFilter(&m)
	if len(m.Rows) != 1 || !strings.HasPrefix(m.Rows[0].Name, "pod-02") {
		t.Fatalf("warning filter: %v", m.Rows)
	}
}

func TestLevelFilterCombinesWithTheSearch(t *testing.T) {
	m := levelled()
	m.Level = kube.LevelCritical
	m.PodQuery = "pod-01"
	ApplyFilter(&m)

	if len(m.Rows) != 1 || !strings.HasPrefix(m.Rows[0].Name, "pod-01") {
		t.Fatalf("search and level must apply together, got %v", m.Rows)
	}
}

func TestCountersLookClickableAndMarkTheActiveFilter(t *testing.T) {
	m := levelled()
	_, screen := draw(t, 170, 30, m)
	cells, w, _ := screen.GetContents()
	g := geom(m, 170, 30)

	if _, _, attrs := cells[lineStatus*w+g.critical.x].Style.Decompose(); attrs&tcell.AttrReverse != 0 {
		t.Error("nothing may be highlighted by default")
	}
	if _, _, attrs := cells[lineStatus*w+g.critical.x].Style.Decompose(); attrs&tcell.AttrUnderline != 0 {
		t.Error("selection is shown by highlight, not by underline")
	}

	m.Level = kube.LevelWarning
	ApplyFilter(&m)
	_, active := draw(t, 170, 30, m)
	cells, w, _ = active.GetContents()
	if _, _, attrs := cells[lineStatus*w+g.warning.x].Style.Decompose(); attrs&tcell.AttrReverse == 0 {
		t.Error("the active filter must stand out")
	}
	if _, _, attrs := cells[lineStatus*w+g.critical.x].Style.Decompose(); attrs&tcell.AttrReverse != 0 {
		t.Error("only the active counter may be highlighted")
	}
}

func TestHitFindsTheCounters(t *testing.T) {
	m := levelled()
	g := geom(m, 170, 30)

	if target, _ := Hit(m, 170, 30, g.critical.x+2, lineStatus); target != HitCritical {
		t.Errorf("click on the critical counter: %v", target)
	}
	if target, _ := Hit(m, 170, 30, g.warning.x+2, lineStatus); target != HitWarning {
		t.Errorf("click on the warning counter: %v", target)
	}
	if target, _ := Hit(m, 170, 30, g.critical.x+g.critical.w+1, lineStatus); target != HitNone {
		t.Errorf("the separator between them is not clickable: %v", target)
	}
}

func TestEmptyLevelFilterExplainsItself(t *testing.T) {
	rows := sample(3)
	for i := range rows {
		rows[i].Worst, rows[i].CPUPct, rows[i].MemPct = 10, 10, 10
	}
	m := model(rows)
	m.Level = kube.LevelCritical
	ApplyFilter(&m)

	lines, _ := draw(t, 170, 30, m)
	if !strings.Contains(lines[rowTop], "no pod is critical by cpu or memory") {
		t.Errorf("empty state: %q", lines[rowTop])
	}
}

func TestDimensionsAreDrawnAndClickable(t *testing.T) {
	m := levelled()
	lines, _ := draw(t, 170, 30, m)

	if !strings.Contains(lines[lineStatus], "issues regarding status / cpu / memory") {
		t.Fatalf("status line: %q", lines[lineStatus])
	}

	g := geom(m, 170, 30)
	for _, c := range []struct {
		rect   rect
		target Target
	}{
		{g.dimStatus, HitDimStatus},
		{g.dimCPU, HitDimCPU},
		{g.dimMemory, HitDimMemory},
	} {
		if target, _ := Hit(m, 170, 30, c.rect.x+1, lineStatus); target != c.target {
			t.Errorf("click at %d: got %v, want %v", c.rect.x+1, target, c.target)
		}
	}
	if target, _ := Hit(m, 170, 30, g.dimStatus.x-2, lineStatus); target != HitNone {
		t.Errorf("the separator is not clickable: %v", target)
	}
}

func TestDimensionRecountsAndRefilters(t *testing.T) {
	rows := sample(4)
	rows[0].Severity, rows[0].CPUPct, rows[0].MemPct, rows[0].Worst = kube.Bad, 5, 5, 5
	rows[1].Severity, rows[1].CPUPct, rows[1].MemPct, rows[1].Worst = kube.Good, 95, 5, 95
	rows[2].Severity, rows[2].CPUPct, rows[2].MemPct, rows[2].Worst = kube.Good, 5, 80, 80
	rows[3].Severity, rows[3].CPUPct, rows[3].MemPct, rows[3].Worst = kube.Good, 5, 5, 5

	m := model(rows)
	if crit, warn := m.Counts(); crit != 1 || warn != 1 {
		t.Fatalf("without a dimension the counters use cpu or memory: %d/%d", crit, warn)
	}

	m.Dimension = kube.DimStatus
	if crit, warn := m.Counts(); crit != 1 || warn != 0 {
		t.Fatalf("by status: %d/%d", crit, warn)
	}
	m.Level = kube.LevelCritical
	ApplyFilter(&m)
	if len(m.Rows) != 1 || !strings.HasPrefix(m.Rows[0].Name, "pod-00") {
		t.Fatalf("status filter: %v", m.Rows)
	}

	m.Dimension = kube.DimMemory
	m.Level = kube.LevelWarning
	ApplyFilter(&m)
	if len(m.Rows) != 1 || !strings.HasPrefix(m.Rows[0].Name, "pod-02") {
		t.Fatalf("memory filter: %v", m.Rows)
	}

	lines, _ := draw(t, 170, 30, m)
	if !strings.Contains(lines[lineStatus], "0 critical / 1 warning") {
		t.Errorf("counters must follow the dimension: %q", lines[lineStatus])
	}
}

func TestSelectedDimensionIsHighlighted(t *testing.T) {
	m := levelled()
	m.Dimension = kube.DimCPU

	_, screen := draw(t, 170, 30, m)
	cells, w, _ := screen.GetContents()
	g := geom(m, 170, 30)

	if _, _, attrs := cells[lineStatus*w+g.dimCPU.x].Style.Decompose(); attrs&tcell.AttrReverse == 0 {
		t.Error("the chosen dimension must be highlighted")
	}
	for _, other := range []rect{g.dimStatus, g.dimMemory} {
		if _, _, attrs := cells[lineStatus*w+other.x].Style.Decompose(); attrs&tcell.AttrReverse != 0 {
			t.Error("only the chosen dimension may be highlighted")
		}
	}
}

func TestEmptyStateNamesTheDimension(t *testing.T) {
	rows := sample(3)
	for i := range rows {
		rows[i].Severity, rows[i].CPUPct, rows[i].MemPct, rows[i].Worst = kube.Good, 10, 10, 10
	}

	for _, c := range []struct {
		dimension kube.Dimension
		want      string
	}{
		{kube.DimStatus, "no pod is critical by status"},
		{kube.DimCPU, "no pod is critical by cpu"},
		{kube.DimMemory, "no pod is critical by memory"},
	} {
		m := model(rows)
		m.Dimension = c.dimension
		m.Level = kube.LevelCritical
		ApplyFilter(&m)

		lines, _ := draw(t, 170, 30, m)
		if !strings.Contains(lines[rowTop], c.want) {
			t.Errorf("empty state: %q, want %q", lines[rowTop], c.want)
		}
	}
}

func TestWarningColourIsAmber(t *testing.T) {
	rows := sample(2)
	rows[1].CPUPct, rows[1].MemPct, rows[1].Worst = 80, 10, 80

	m := model(rows)
	lines, screen := draw(t, 170, 30, m)
	cells, w, _ := screen.GetContents()

	header := "%LIM"
	column := strings.Index(lines[lineHeader], header)
	if column < 0 {
		t.Fatalf("column not found in %q", lines[lineHeader])
	}
	fg, _, _ := cells[(rowTop+1)*w+column+len(header)-1].Style.Decompose()
	if fg != warnColor {
		t.Errorf("a warning percentage must use the same amber, got %v", fg)
	}
	if fg == tcell.ColorYellow {
		t.Error("the plain yellow must be gone")
	}
}

func TestDimensionWordsStandOutFromTheirLabel(t *testing.T) {
	m := levelled()
	_, screen := draw(t, 170, 30, m)
	cells, w, _ := screen.GetContents()
	g := geom(m, 170, 30)

	for _, r := range []rect{g.dimStatus, g.dimCPU, g.dimMemory} {
		fg, _, attrs := cells[lineStatus*w+r.x].Style.Decompose()
		if fg != tcell.Color231 {
			t.Errorf("a clickable word must be pure white, got %v", fg)
		}
		if attrs&tcell.AttrBold == 0 {
			t.Error("a clickable word must be bold as well, themes map white loosely")
		}
	}

	prefix := g.warning.x + g.warning.w + 4
	if fg, _, _ := cells[lineStatus*w+prefix].Style.Decompose(); fg != tcell.ColorGray {
		t.Errorf("the words around them stay dim, got %v", fg)
	}

	m.Dimension = kube.DimCPU
	_, chosen := draw(t, 170, 30, m)
	cells, w, _ = chosen.GetContents()
	fg, _, attrs := cells[lineStatus*w+g.dimCPU.x].Style.Decompose()
	if fg != tcell.Color231 || attrs&tcell.AttrReverse == 0 {
		t.Errorf("the chosen word keeps its colour and gets highlighted, got %v %v", fg, attrs)
	}
}

func TestUptimeUnits(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{-time.Second, "0s"},
		{9 * time.Second, "9s"},
		{59 * time.Second, "59s"},
		{time.Minute, "1m 0s"},
		{3*time.Minute + 12*time.Second, "3m 12s"},
		{2*time.Hour + 5*time.Minute, "2h 5m"},
		{3*24*time.Hour + 4*time.Hour, "3d 4h"},
		{17 * 24 * time.Hour, "2w 3d"},
		{100 * 24 * time.Hour, "3mo 1w"},
		{400 * 24 * time.Hour, "1y 1mo"},
	}
	for _, c := range cases {
		if got := Uptime(c.d); got != c.want {
			t.Errorf("Uptime(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestUptimeIsDrawnBesideTheClock(t *testing.T) {
	m := model(sample(2))
	m.Now = time.Date(2026, 9, 23, 17, 0, 0, 0, time.Local)
	m.Started = m.Now.Add(-(2*time.Hour + 5*time.Minute))

	lines, screen := draw(t, 170, 30, m)
	if !strings.Contains(lines[lineClock], "17:00:00  (2h 5m)") {
		t.Errorf("top line: %q", lines[lineClock])
	}

	cells, w, _ := screen.GetContents()
	column := strings.Index(lines[lineClock], "(")
	if fg, _, _ := cells[lineClock*w+column].Style.Decompose(); fg != tcell.ColorGray {
		t.Errorf("the timer must be quieter than the clock, got %v", fg)
	}
}

func TestUptimeHiddenWithoutAStart(t *testing.T) {
	m := model(sample(2))
	m.Now = time.Date(2026, 9, 23, 17, 0, 0, 0, time.Local)

	lines, _ := draw(t, 170, 30, m)
	if strings.Contains(lines[lineClock], "(") {
		t.Errorf("no start time, no timer: %q", lines[lineClock])
	}
}

func TestSelectedRowKeepsItsMeaningInDarkInk(t *testing.T) {
	rows := sample(2)
	rows[0].Severity = kube.Bad
	rows[0].CPUPct, rows[0].MemPct, rows[0].Worst = 95, 10, 95

	m := model(rows)
	m.Cursor = 0

	lines, screen := draw(t, 170, 30, m)
	cells, w, _ := screen.GetContents()

	for x := 0; x < 60; x++ {
		fg, bg, _ := cells[rowTop*w+x].Style.Decompose()
		if bg != selectedColor {
			t.Fatalf("column %d of the selected row is not on the light bar", x)
		}
		if fg == tcell.ColorWhite || fg == warnColor {
			t.Fatalf("column %d keeps a light ink on a light bar", x)
		}
	}

	column := strings.Index(lines[lineHeader], "STATUS")
	if fg, _, _ := cells[rowTop*w+column].Style.Decompose(); fg != tcell.Color88 {
		t.Errorf("a broken pod must stay red, in a darker red: %v", fg)
	}
}

func TestSelectionInkPerColumn(t *testing.T) {
	rows := sample(2)
	rows[0].Severity = kube.Bad
	rows[0].Status = "CrashLoopBackOff"
	rows[0].CPUPct, rows[0].MemPct, rows[0].Worst = 95, 80, 95
	rows[0].Restarts, rows[0].NewRestarts, rows[0].OOMs = 12, 2, 3

	m := model(rows)
	m.Cursor = 0

	lines, screen := draw(t, 170, 30, m)
	cells, w, _ := screen.GetContents()
	header := lines[lineHeader]

	inkAt := func(column int) tcell.Color {
		fg, bg, _ := cells[rowTop*w+column].Style.Decompose()
		if bg != selectedColor {
			t.Fatalf("column %d is not on the bar", column)
		}
		return fg
	}

	black := []struct {
		name  string
		start int
	}{
		{"POD", 0},
		{"CPU", strings.Index(header, "CPU") + 2},
		{"MEM", strings.Index(header, "MEM") + 2},
		{"RESTART CTR", strings.Index(lines[rowTop], "12 +2")},
		{"OOM CTR", strings.Index(header, "OOM CTR") + 3},
		{"EXIT", strings.Index(header, "EXIT")},
		{"LAST RESTART", strings.Index(header, "LAST RESTART")},
	}
	for _, c := range black {
		if got := inkAt(c.start); got != inkColor {
			t.Errorf("%s must turn black on the bar, got %v", c.name, got)
		}
	}

	status := strings.Index(header, "STATUS")
	if got := inkAt(status); got != tcell.Color88 {
		t.Errorf("STATUS keeps its meaning in a dark red, got %v", got)
	}
	cpuPct := strings.Index(header, "%LIM") + 3
	if got := inkAt(cpuPct); got != tcell.Color88 {
		t.Errorf("a critical %%LIM stays red, got %v", got)
	}
	memPct := strings.LastIndex(header, "%LIM") + 3
	if got := inkAt(memPct); got != tcell.Color94 {
		t.Errorf("a warning %%LIM stays amber, got %v", got)
	}
}

func TestSelectionBarIsMuted(t *testing.T) {
	if selectedColor != tcell.Color248 {
		t.Errorf("the bar must stay a muted grey, got %v", selectedColor)
	}
	if inkColor != tcell.Color16 {
		t.Errorf("the ink must be black, got %v", inkColor)
	}
}

func TestNoRowIsShadedWithoutASelection(t *testing.T) {
	m := model(sample(4))
	m.Cursor = -1

	_, screen := draw(t, 170, 30, m)
	cells, w, h := screen.GetContents()

	for y := rowTop; y < h-1; y++ {
		for x := 0; x < 60; x++ {
			if _, bg, _ := cells[y*w+x].Style.Decompose(); bg == selectedColor {
				t.Fatalf("row %d is shaded although nothing is selected", y)
			}
		}
	}
}

func TestScrollingWithoutASelectionStillShowsRows(t *testing.T) {
	m := model(sample(40))
	m.Cursor = -1
	m.Offset = 5

	lines, _ := draw(t, 170, 30, m)
	if !strings.Contains(lines[rowTop], "pod-05") {
		t.Errorf("the view must follow the offset: %q", lines[rowTop])
	}
}

func TestPodFrameMatchesThePodColumn(t *testing.T) {
	for _, width := range []int{110, 160, 210, 260} {
		m := model(sample(3))
		lines, _ := draw(t, width, 30, m)

		frame := len([]rune(lines[podBoxTop]))
		column := strings.Index(lines[lineHeader], "STATUS") - gap
		if frame != column {
			t.Errorf("width %d: the frame is %d wide, the POD column is %d", width, frame, column)
		}
	}
}

func TestBlankLineBetweenStatusAndPodFrame(t *testing.T) {
	lines, _ := draw(t, 170, 30, model(sample(3)))

	if strings.TrimSpace(lines[lineStatus]) == "" {
		t.Fatalf("the counters must stay on their line: %q", lines[lineStatus])
	}
	if strings.TrimSpace(lines[lineStatus+1]) != "" {
		t.Errorf("a blank line must separate them from the pod frame: %q", lines[lineStatus+1])
	}
	if !strings.HasPrefix(lines[podBoxTop], "\u250c") {
		t.Errorf("the frame must follow the blank line: %q", lines[podBoxTop])
	}
}

func TestNamespaceUsesTheClockColour(t *testing.T) {
	m := model(sample(2))
	m.Now = time.Date(2026, 9, 24, 10, 30, 0, 0, time.Local)

	lines, screen := draw(t, 170, 30, m)
	cells, w, _ := screen.GetContents()

	clockColumn := strings.Index(lines[lineClock], "10:30:00")
	nameColumn := strings.Index(lines[lineTitle], "production")
	if clockColumn < 0 || nameColumn < 0 {
		t.Fatalf("clock %q, title %q", lines[lineClock], lines[lineTitle])
	}

	clockFg, _, _ := cells[lineClock*w+clockColumn].Style.Decompose()
	nameFg, _, attrs := cells[lineTitle*w+nameColumn].Style.Decompose()
	if nameFg != clockFg {
		t.Errorf("the namespace must share the clock colour: %v vs %v", nameFg, clockFg)
	}
	if attrs&tcell.AttrBold == 0 {
		t.Error("the namespace stays bold")
	}

	labelFg, _, _ := cells[lineTitle*w].Style.Decompose()
	if labelFg == nameFg {
		t.Error("the words around it stay dim")
	}
}
