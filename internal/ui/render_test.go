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
		Loaded:    true,
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
	if target, index := Hit(m, 170, 40, 5, rowTop+2); target != HitPodName || index != 2 {
		t.Errorf("click on a pod name: %v %d", target, index)
	}
	if target, _ := Hit(m, 170, 40, 120, rowTop+2); target != HitRow {
		t.Errorf("click outside the name column: %v", target)
	}
	if target, _ := Hit(m, 170, 40, 5, lineTitle); target != HitNone {
		t.Errorf("click on the title must do nothing: %v", target)
	}

	scrolled := model(sample(40))
	scrolled.Offset = 3
	if _, index := Hit(scrolled, 170, 40, 5, rowTop); index != 3 {
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

func TestNoRowIsEverShaded(t *testing.T) {
	m := model(sample(6))
	m.Expanded = m.Rows[2].Name

	_, screen := draw(t, 170, 30, m)
	cells, w, h := screen.GetContents()

	for y := rowTop; y < h-1; y++ {
		for x := 0; x < 80; x++ {
			if _, bg, _ := cells[y*w+x].Style.Decompose(); bg != tcell.ColorDefault {
				t.Fatalf("row %d column %d is painted, rows must stay plain", y, x)
			}
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

func TestActionRowOpensUnderThePod(t *testing.T) {
	m := model(sample(5))
	m.Expanded = m.Rows[1].Name

	lines, _ := draw(t, 170, 30, m)

	if !strings.Contains(lines[rowTop+1], "pod-01") {
		t.Fatalf("the pod stays where it was: %q", lines[rowTop+1])
	}
	actions := lines[rowTop+2]
	for _, want := range []string{"[ Inspect ]", "[ Restart ]", "[ Terminate ]"} {
		if !strings.Contains(actions, want) {
			t.Errorf("action row missing %q: %q", want, actions)
		}
	}
	if !strings.Contains(lines[rowTop+3], "pod-02") {
		t.Errorf("the rest of the table shifts down by one: %q", lines[rowTop+3])
	}
}

func TestActionRowHitTesting(t *testing.T) {
	m := model(sample(4))
	m.Expanded = m.Rows[0].Name

	lines, _ := draw(t, 170, 30, m)
	actions := lines[rowTop+1]

	cases := []struct {
		label  string
		target Target
	}{
		{"[ Inspect ]", HitActionInspect},
		{"[ Restart ]", HitActionRestart},
		{"[ Terminate ]", HitActionTerminate},
	}
	for _, c := range cases {
		x := strings.Index(actions, c.label) + 2
		if target, index := Hit(m, 170, 30, x, rowTop+1); target != c.target || index != 0 {
			t.Errorf("click on %s: got %v %d", c.label, target, index)
		}
	}
	if target, _ := Hit(m, 170, 30, 70, rowTop+1); target != HitNone {
		t.Errorf("empty space on the action row does nothing: %v", target)
	}
}

func TestConfirmationReplacesTheButtons(t *testing.T) {
	m := model(sample(3))
	m.Expanded = m.Rows[0].Name
	m.Confirm = ActionTerminate

	lines, _ := draw(t, 170, 30, m)
	row := lines[rowTop+1]

	if !strings.Contains(row, "Terminate "+m.Rows[0].Name+"?") {
		t.Errorf("the question must name the pod: %q", row)
	}
	for _, want := range []string{"[ Yes ]", "[ Cancel ]"} {
		if !strings.Contains(row, want) {
			t.Errorf("confirmation missing %q: %q", want, row)
		}
	}
	if strings.Contains(row, "[ Inspect ]") {
		t.Errorf("the buttons must step aside: %q", row)
	}

	yes := strings.Index(row, "[ Yes ]") + 2
	if target, _ := Hit(m, 170, 30, yes, rowTop+1); target != HitConfirmYes {
		t.Errorf("click on Yes: %v", target)
	}
	cancel := strings.Index(row, "[ Cancel ]") + 2
	if target, _ := Hit(m, 170, 30, cancel, rowTop+1); target != HitConfirmCancel {
		t.Errorf("click on Cancel: %v", target)
	}
}

func TestEmptyTableSaysWhyItIsEmpty(t *testing.T) {
	loading := model(nil)
	loading.Loaded = false
	lines, _ := draw(t, 170, 30, loading)
	if !strings.Contains(lines[rowTop], "asking the cluster for pods") {
		t.Errorf("before the first answer: %q", lines[rowTop])
	}

	loaded := model(nil)
	lines, _ = draw(t, 170, 30, loaded)
	if !strings.Contains(lines[rowTop], "no pods in this namespace") {
		t.Errorf("after the first answer: %q", lines[rowTop])
	}
}

func inspecting() Model {
	m := model(sample(2))
	m.Screen = ScreenInspect
	m.Inspect = Inspection{
		Pod:       "api-worker-1",
		Container: "app",
		Default:   []string{"POD", "  name               api-worker-1", "  namespace          production"},
		Textual:   []string{"Pod api-worker-1 lives in namespace production, created 3m ago and runs on node worker-02."},
		Yaml:      []string{"apiVersion: v1", "kind: Pod", "metadata:", "  name: api-worker-1"},
		Logs: []LogLine{
			{Time: "18:00:01", Text: "listening on :8000"},
			{Time: "18:00:02", Text: "GET /health 200"},
			{Time: "18:00:03", Text: "worker started"},
		},
	}
	return m
}

func TestInspectHasTwoFramedPanes(t *testing.T) {
	lines, _ := draw(t, 140, 28, inspecting())

	top := lines[inspectTop]
	if strings.Count(top, "┌") != 2 || strings.Count(top, "┐") != 2 {
		t.Fatalf("two frames must open on the same line: %q", top)
	}
	if !strings.Contains(top, "config") || !strings.Contains(top, "logs") {
		t.Errorf("the frames must be labelled: %q", top)
	}

	body := strings.Join(lines, "\n")
	if !strings.Contains(body, "name               api-worker-1") {
		t.Error("config belongs in the left pane")
	}
	if !strings.Contains(body, "listening on :8000") {
		t.Error("logs belong in the right pane")
	}

	g := inspectGeom(140, 28)
	configColumn := strings.Index(lines[inspectTop+1], "POD")
	if configColumn >= g.right.x {
		t.Errorf("config drifted into the log pane at column %d", configColumn)
	}
	logColumn := strings.Index(lines[inspectTop+1], "listening")
	if logColumn >= 0 && logColumn < g.right.x {
		t.Errorf("logs drifted into the config pane at column %d", logColumn)
	}
}

func TestInspectFormatButtons(t *testing.T) {
	m := inspecting()
	lines, _ := draw(t, 140, 28, m)
	buttons := lines[26]

	for _, want := range []string{"[ default ]", "[ textual ]", "[ yaml ]"} {
		if !strings.Contains(buttons, want) {
			t.Errorf("missing %q: %q", want, buttons)
		}
	}
	if strings.Contains(buttons, "readable") {
		t.Errorf("the old name must be gone: %q", buttons)
	}

	for _, c := range []struct {
		label  string
		target Target
	}{
		{"[ default ]", HitFormatDefault},
		{"[ textual ]", HitFormatTextual},
		{"[ yaml ]", HitFormatYAML},
	} {
		x := strings.Index(buttons, c.label) + 2
		if got := HitInspect(140, 28, x, 26); got != c.target {
			t.Errorf("click on %s: %v", c.label, got)
		}
	}
}

func TestInspectShowsEachFormat(t *testing.T) {
	m := inspecting()

	lines, _ := draw(t, 140, 28, m)
	if !strings.Contains(strings.Join(lines, "\n"), "name               api-worker-1") {
		t.Error("default format must be the structured one")
	}

	m.Inspect.Format = FormatTextual
	lines, _ = draw(t, 140, 28, m)
	if !strings.Contains(strings.Join(lines, "\n"), "Pod api-worker-1 lives in namespace") {
		t.Error("textual format must read as sentences")
	}

	m.Inspect.Format = FormatYAML
	lines, _ = draw(t, 140, 28, m)
	if !strings.Contains(strings.Join(lines, "\n"), "apiVersion: v1") {
		t.Error("yaml format must show the manifest")
	}
}

func TestInspectLogSearchLivesInsideTheLogPane(t *testing.T) {
	m := inspecting()
	g := inspectGeom(140, 28)

	lines, _ := draw(t, 140, 28, m)
	if !strings.Contains(lines[g.search.y], logPlaceholder) {
		t.Errorf("the search box must sit at the bottom of the pane: %q", lines[g.search.y])
	}
	separator := lines[g.right.y+g.right.h-3]
	if !strings.Contains(separator, "├") || !strings.Contains(separator, "┤") {
		t.Errorf("a line must fence the search off from the stream: %q", separator)
	}
	if g.search.y >= 28-2 {
		t.Error("the search box must stay inside the pane, not below it")
	}

	if got := HitInspect(140, 28, g.search.x+2, g.search.y); got != HitLogSearch {
		t.Errorf("click on the search box: %v", got)
	}

	m.Inspect.LogQuery = "health"
	filtered := m.Inspect.VisibleLogs()
	if len(filtered) != 1 || !strings.Contains(filtered[0].Text, "GET /health") {
		t.Errorf("the query must filter the stream, got %v", filtered)
	}

	lines, _ = draw(t, 140, 28, m)
	body := strings.Join(lines, "\n")
	if strings.Contains(body, "listening on :8000") {
		t.Error("lines that do not match must be hidden")
	}
	if !strings.Contains(body, "GET /health 200") {
		t.Error("matching lines stay")
	}
}

func TestInspectLogsWaitAndReportProblems(t *testing.T) {
	m := inspecting()
	m.Inspect.Logs = nil

	lines, _ := draw(t, 140, 28, m)
	if !strings.Contains(strings.Join(lines, "\n"), "waiting for output") {
		t.Error("an empty stream must say it is waiting")
	}

	m.Inspect.LogErr = "no permission to read the logs of api-worker-1"
	lines, _ = draw(t, 140, 28, m)
	if !strings.Contains(strings.Join(lines, "\n"), "no permission to read the logs") {
		t.Error("a broken stream must show the reason")
	}
}

func TestInspectWrapsLongLines(t *testing.T) {
	m := inspecting()
	m.Inspect.Format = FormatTextual
	m.Inspect.Textual = []string{strings.Repeat("word ", 60)}

	lines, _ := draw(t, 140, 28, m)
	g := inspectGeom(140, 28)

	filled := 0
	for i := 0; i < g.room; i++ {
		if strings.TrimSpace(lines[inspectTop+1+i]) != "" {
			filled++
		}
	}
	if filled < 2 {
		t.Error("a long sentence must be wrapped over several lines")
	}
	for i := 0; i < g.room; i++ {
		line := []rune(lines[inspectTop+1+i])
		if len(line) <= g.left.w-1 {
			continue
		}
		if line[g.left.w-1] != '\u2502' {
			t.Fatalf("wrapped text broke through the frame: %q", string(line))
		}
	}
}

func styleAt(t *testing.T, screen tcell.SimulationScreen, line string, needle string, y int) tcell.Style {
	t.Helper()
	at := strings.Index(line, needle)
	if at < 0 {
		t.Fatalf("%q not found in %q", needle, line)
	}
	column := len([]rune(line[:at]))
	cells, w, _ := screen.GetContents()
	return cells[y*w+column].Style
}

func TestConfigColoursWhatMatters(t *testing.T) {
	m := inspecting()
	m.Inspect.Default = []string{
		"POD",
		"  status             CrashLoopBackOff",
		"  qos class          BestEffort",
		"  restarts           4",
		"  node               worker-02",
		"",
		"CONDITIONS",
		"  Ready              False (ContainersNotReady)",
	}

	lines, screen := draw(t, 140, 28, m)
	row := func(needle string) (string, int) {
		for i, line := range lines {
			if strings.Contains(line, needle) {
				return line, i
			}
		}
		t.Fatalf("%q is not on screen", needle)
		return "", 0
	}

	line, y := row("CrashLoopBackOff")
	if fg, _, _ := styleAt(t, screen, line, "CrashLoopBackOff", y).Decompose(); fg != tcell.ColorRed {
		t.Errorf("a broken status must be red, got %v", fg)
	}
	line, y = row("BestEffort")
	if fg, _, _ := styleAt(t, screen, line, "BestEffort", y).Decompose(); fg != warnColor {
		t.Errorf("BestEffort deserves a warning colour, got %v", fg)
	}
	line, y = row("restarts")
	if fg, _, _ := styleAt(t, screen, line, "4", y).Decompose(); fg != warnColor {
		t.Errorf("restarts above zero must stand out, got %v", fg)
	}
	line, y = row("Ready  ")
	if fg, _, _ := styleAt(t, screen, line, "False", y).Decompose(); fg != warnColor {
		t.Errorf("a false condition must stand out, got %v", fg)
	}
	line, y = row("  node")
	if fg, _, _ := styleAt(t, screen, line, "node", y).Decompose(); fg != tcell.ColorGray {
		t.Errorf("labels stay dim, got %v", fg)
	}
}

func TestTextualColoursTheTellingWords(t *testing.T) {
	m := inspecting()
	m.Inspect.Format = FormatTextual
	m.Inspect.Textual = []string{
		"It is Running with 1 of 1 containers ready.",
		"Container app runs image, restarted 3 times, with no limits set.",
		"Worth a look: app was killed for using too much memory.",
	}

	lines, screen := draw(t, 140, 28, m)
	find := func(needle string) (string, int) {
		for i, line := range lines {
			if strings.Contains(line, needle) {
				return line, i
			}
		}
		t.Fatalf("%q is not on screen", needle)
		return "", 0
	}

	line, y := find("Running")
	if fg, _, _ := styleAt(t, screen, line, "Running", y).Decompose(); fg != tcell.ColorGreen {
		t.Errorf("Running must be green, got %v", fg)
	}
	line, y = find("no limits set")
	if fg, _, _ := styleAt(t, screen, line, "with no limits set", y).Decompose(); fg != warnColor {
		t.Errorf("missing limits must be amber, got %v", fg)
	}
	line, y = find("too much memory")
	if fg, _, _ := styleAt(t, screen, line, "killed for using too much memory", y).Decompose(); fg != tcell.ColorRed {
		t.Errorf("an OOM sentence must be red, got %v", fg)
	}
	line, y = find("Container app")
	if fg, _, _ := styleAt(t, screen, line, "Container app runs", y).Decompose(); fg != tcell.ColorDefault {
		t.Errorf("ordinary prose stays plain, got %v", fg)
	}
}

func TestYamlKeysAreColoured(t *testing.T) {
	m := inspecting()
	m.Inspect.Format = FormatYAML
	m.Inspect.Yaml = []string{"kind: Pod", "  phase: Running"}

	lines, screen := draw(t, 140, 28, m)
	for i, line := range lines {
		if strings.Contains(line, "kind: Pod") {
			if fg, _, _ := styleAt(t, screen, line, "kind:", i).Decompose(); fg != tcell.Color231 {
				t.Errorf("yaml keys must stand out, got %v", fg)
			}
		}
		if strings.Contains(line, "phase: Running") {
			if fg, _, _ := styleAt(t, screen, line, "Running", i).Decompose(); fg != tcell.ColorGreen {
				t.Errorf("a telling value keeps its colour, got %v", fg)
			}
		}
	}
}

func TestLogMatchesAreHighlighted(t *testing.T) {
	m := inspecting()
	m.Inspect.LogQuery = "health"

	lines, screen := draw(t, 140, 28, m)
	for i, line := range lines {
		if !strings.Contains(line, "GET /health") {
			continue
		}
		if _, bg, _ := styleAt(t, screen, line, "health", i).Decompose(); bg != warnColor {
			t.Errorf("the match must be highlighted, got background %v", bg)
		}
		if _, bg, _ := styleAt(t, screen, line, "GET ", i).Decompose(); bg == warnColor {
			t.Error("only the match may be highlighted")
		}
		return
	}
	t.Fatal("the matching line is not on screen")
}

func TestLogTimestampsAreDrawnQuietly(t *testing.T) {
	m := inspecting()

	lines, screen := draw(t, 140, 28, m)
	for i, line := range lines {
		if !strings.Contains(line, "worker started") {
			continue
		}
		if !strings.Contains(line, "18:00:03") {
			t.Fatalf("the line must carry its time: %q", line)
		}
		if fg, _, _ := styleAt(t, screen, line, "18:00:03", i).Decompose(); fg != tcell.ColorGray {
			t.Errorf("the time must stay quiet, got %v", fg)
		}
		return
	}
	t.Fatal("the log line is not on screen")
}
