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
	if !strings.HasPrefix(lines[linePods], "\u2502 Search...") {
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
	g := geom(model(sample(3)), 170, 40)
	if g.nsBox.w >= g.podBox.w {
		t.Errorf("the namespace frame must be the narrower one: %d vs %d", g.nsBox.w, g.podBox.w)
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
	if !strings.Contains(lines[lineStatus], "critical") || !strings.Contains(lines[lineStatus], "issues regarding") {
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
	if !strings.Contains(strings.Join(lines, "\n"), "No pod matches nothing-matches.") {
		t.Errorf("empty state: %q", strings.Join(lines[rowTop:rowTop+8], "\n"))
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

	if !strings.Contains(lines[linePods], "Search...") {
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
	if strings.Contains(typed[linePods], podPlaceholder) {
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
	if !strings.Contains(strings.Join(lines, "\n"), "No resources found in this namespace.") {
		t.Errorf("empty state: %q", strings.Join(lines[rowTop:rowTop+6], "\n"))
	}
	if !strings.Contains(lines[lineStatus], "Nothing is wrong in this namespace.") {
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
	column := columnOf(t, lines[rowTop+1], "12 +2")
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

		if strings.TrimSpace(clock) != "16:58:08" {
			t.Errorf("width %d: the top line holds the clock alone: %q", width, clock)
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
	g := geom(m, width, 30)

	block := "Facing 6 critical issues regarding cpu and memory."
	start := strings.Index(status, block)
	if start < 0 {
		t.Fatalf("status line: %q", status)
	}
	left, right := start, (width-1)-(start+len(block))
	if left < right-1 || left > right+1 {
		t.Errorf("the status group is not centred, %d left and %d right", left, right)
	}

	cells, w, _ := screen.GetContents()
	if fg, _, _ := cells[lineStatus*w+g.critical.x].Style.Decompose(); fg != tcell.ColorRed {
		t.Errorf("critical must be red, got %v", fg)
	}
	if g.warning.w != 0 || strings.Contains(status, "warning") {
		t.Errorf("with nothing warning the word stays away: %q", status)
	}

	warned := troubled()
	warnedLines, warnedScreen := draw(t, width, 30, warned)
	warnedCells, ww, _ := warnedScreen.GetContents()
	gw := geom(warned, width, 30)
	if !strings.Contains(warnedLines[lineStatus], "1 warning") {
		t.Errorf("a warning must be counted: %q", warnedLines[lineStatus])
	}
	if fg, _, _ := warnedCells[lineStatus*ww+gw.warning.x].Style.Decompose(); fg != warnColor {
		t.Errorf("warning keeps its amber colour, got %v", fg)
	}
	if fg, _, _ := cells[lineStatus*w+start].Style.Decompose(); fg != tcell.ColorGray {
		t.Errorf("the word Facing stays quiet, got %v", fg)
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

	m := model(rows)
	lines, _ := draw(t, 170, 30, m)
	g := geom(m, 170, 30)
	xs := columnXs(g.widths)
	row := []rune(lines[rowTop])

	for _, c := range []struct {
		key   string
		value string
	}{{"restarts", "7"}, {"ooms", "3"}} {
		index := -1
		for i, col := range g.cols {
			if col.key == c.key {
				index = i
			}
		}
		if index < 0 {
			t.Fatalf("column %q is not on screen", c.key)
		}

		field := string(row[xs[index] : xs[index]+g.widths[index]])
		if strings.TrimSpace(field) != c.value {
			t.Fatalf("column %q holds %q", c.key, field)
		}
		left := strings.Index(field, c.value)
		right := g.widths[index] - left - len(c.value)
		if left < right-1 || left > right+1 {
			t.Errorf("%q is not centred in its column: %q (%d left, %d right)", c.value, field, left, right)
		}
	}
}

func troubled() Model {
	rows := sample(5)
	rows[0].Severity = kube.Bad
	rows[1].Worst, rows[1].CPUPct, rows[1].MemPct = 95, 95, 40
	rows[2].Worst, rows[2].CPUPct, rows[2].MemPct = 80, 20, 80
	rows[3].Worst, rows[3].CPUPct, rows[3].MemPct = 10, 10, 10
	rows[4].Worst, rows[4].CPUPct, rows[4].MemPct = -1, -1, -1
	return model(rows)
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
	if !strings.Contains(strings.Join(lines, "\n"), "No pod is critical by status, cpu or memory.") {
		t.Errorf("empty state: %q", strings.Join(lines[rowTop:rowTop+8], "\n"))
	}
}

func TestDimensionsAreDrawnAndClickable(t *testing.T) {
	m := troubled()
	lines, _ := draw(t, 170, 30, m)

	if !strings.Contains(lines[lineStatus], "regarding status, cpu and memory.") {
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
	if target, _ := Hit(m, 170, 30, g.dimStatus.x+g.dimStatus.w, lineStatus); target != HitNone {
		t.Errorf("the comma is not clickable: %v", target)
	}
}

func TestDimensionRecountsAndRefilters(t *testing.T) {
	rows := sample(4)
	rows[0].Severity, rows[0].CPUPct, rows[0].MemPct, rows[0].Worst = kube.Bad, 5, 5, 5
	rows[1].Severity, rows[1].CPUPct, rows[1].MemPct, rows[1].Worst = kube.Good, 95, 5, 95
	rows[2].Severity, rows[2].CPUPct, rows[2].MemPct, rows[2].Worst = kube.Good, 5, 80, 80
	rows[3].Severity, rows[3].CPUPct, rows[3].MemPct, rows[3].Worst = kube.Good, 5, 5, 5

	m := model(rows)
	if crit, warn := m.Counts(); crit != 2 || warn != 1 {
		t.Fatalf("without a dimension the counters take the worst of every dimension: %d/%d", crit, warn)
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
	if !strings.Contains(lines[lineStatus], "Facing 1 warning issue regarding status, cpu and memory.") {
		t.Errorf("counters must follow the dimension: %q", lines[lineStatus])
	}
}

func TestSelectedDimensionIsHighlighted(t *testing.T) {
	m := troubled()
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
		{kube.DimStatus, "No pod is critical by status."},
		{kube.DimCPU, "No pod is critical by cpu."},
		{kube.DimMemory, "No pod is critical by memory."},
	} {
		m := model(rows)
		m.Dimension = c.dimension
		m.Level = kube.LevelCritical
		ApplyFilter(&m)

		lines, _ := draw(t, 170, 30, m)
		if !strings.Contains(strings.Join(lines, "\n"), c.want) {
			t.Errorf("empty state: %q, want %q", strings.Join(lines[rowTop:rowTop+8], "\n"), c.want)
		}
	}
}

func TestWarningColourIsAmber(t *testing.T) {
	rows := sample(2)
	rows[1].CPUPct, rows[1].MemPct, rows[1].Worst = 80, 10, 80

	m := model(rows)
	lines, screen := draw(t, 170, 30, m)
	cells, w, _ := screen.GetContents()

	column := columnOf(t, lines[lineHeader], "%LIM")
	fg, _, _ := cells[(rowTop+1)*w+column+len("%LIM")-1].Style.Decompose()
	if fg != warnColor {
		t.Errorf("a warning percentage must use the same amber, got %v", fg)
	}
	if fg == tcell.ColorYellow {
		t.Error("the plain yellow must be gone")
	}
}

func TestDimensionWordsStandOutFromTheirLabel(t *testing.T) {
	m := troubled()
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
	g = geom(m, 170, 30)
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

func TestUptimeSitsUnderTheClockAndStaysCentred(t *testing.T) {
	for _, c := range []struct {
		since time.Duration
		want  string
	}{
		{9 * time.Second, "(9s)"},
		{3*time.Minute + 12*time.Second, "(3m 12s)"},
		{2*time.Hour + 5*time.Minute, "(2h 5m)"},
		{50 * time.Hour, "(2d 2h)"},
		{20 * 24 * time.Hour, "(2w 6d)"},
	} {
		m := model(sample(2))
		m.Now = time.Date(2026, 9, 26, 17, 0, 0, 0, time.Local)
		m.Started = m.Now.Add(-c.since)

		width := 171
		lines, screen := draw(t, width, 30, m)
		total := width - 1

		if strings.TrimSpace(lines[lineUptime]) != c.want {
			t.Fatalf("uptime line: %q, want %q", lines[lineUptime], c.want)
		}

		indent := len(lines[lineUptime]) - len(strings.TrimLeft(lines[lineUptime], " "))
		right := total - len(lines[lineUptime])
		if indent < right-1 || indent > right+1 {
			t.Errorf("%s is not centred: %d left, %d right", c.want, indent, right)
		}

		clockMiddle := strings.Index(lines[lineClock], "17:00:00") + len("17:00:00")/2
		uptimeMiddle := indent + len(strings.TrimSpace(lines[lineUptime]))/2
		if clockMiddle < uptimeMiddle-1 || clockMiddle > uptimeMiddle+1 {
			t.Errorf("%s does not sit under the clock: %d vs %d", c.want, uptimeMiddle, clockMiddle)
		}

		cells, w, _ := screen.GetContents()
		if fg, _, _ := cells[lineUptime*w+indent].Style.Decompose(); fg != tcell.ColorGray {
			t.Errorf("the timer must stay quiet, got %v", fg)
		}
	}
}

func TestUptimeHiddenWithoutAStart(t *testing.T) {
	m := model(sample(2))
	m.Now = time.Date(2026, 9, 23, 17, 0, 0, 0, time.Local)

	lines, _ := draw(t, 170, 30, m)
	if strings.Contains(lines[lineClock]+lines[lineUptime], "(") {
		t.Errorf("no start time, no timer: %q / %q", lines[lineClock], lines[lineUptime])
	}
}

func TestPodFrameMatchesThePodColumn(t *testing.T) {
	for _, width := range []int{110, 160, 210, 260} {
		m := model(sample(3))
		lines, _ := draw(t, width, 30, m)
		g := geom(m, width, 30)

		frame := len([]rune(lines[podBoxTop]))
		if frame != g.widths[0] {
			t.Errorf("width %d: the frame is %d wide, the POD column is %d", width, frame, g.widths[0])
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

func TestActionsStackInsideThePodColumn(t *testing.T) {
	m := model(sample(5))
	m.Expanded = m.Rows[1].Name

	lines, _ := draw(t, 170, 30, m)
	g := geom(m, 170, 30)
	xs := columnXs(g.widths)

	if !strings.Contains(lines[rowTop+1], "pod-01") {
		t.Fatalf("the pod stays where it was: %q", lines[rowTop+1])
	}

	for i, want := range []string{"[ Inspect ]", "[ Restart ]", "[ Terminate ]"} {
		row := []rune(lines[rowTop+2+i])
		cell := string(row[xs[0] : xs[0]+g.widths[0]])
		if !strings.Contains(cell, want) {
			t.Errorf("line %d must hold %q: %q", i, want, cell)
		}
		for _, x := range g.dividers() {
			if row[x] != '\u2502' {
				t.Errorf("line %d must keep the grid at %d", i, x)
			}
		}
		if rest := string(row[xs[1]:]); strings.Contains(rest, "[") {
			t.Errorf("the buttons must stay inside the POD column: %q", rest)
		}
	}

	if !strings.Contains(lines[rowTop+5], "pod-02") {
		t.Errorf("the rest of the table shifts down by three: %q", lines[rowTop+5])
	}
}

func TestActionsHitTesting(t *testing.T) {
	m := model(sample(4))
	m.Expanded = m.Rows[0].Name
	g := geom(m, 170, 30)
	xs := columnXs(g.widths)

	for i, want := range []Target{HitActionInspect, HitActionRestart, HitActionTerminate} {
		if target, index := Hit(m, 170, 30, xs[0]+4, rowTop+1+i); target != want || index != 0 {
			t.Errorf("line %d: got %v %d, want %v", i, target, index, want)
		}
	}
	if target, _ := Hit(m, 170, 30, xs[2]+1, rowTop+1); target != HitNone {
		t.Errorf("outside the POD column nothing happens: %v", target)
	}
}

func TestConfirmationTakesTheSameThreeLines(t *testing.T) {
	m := model(sample(3))
	m.Expanded = m.Rows[0].Name
	m.Confirm = ActionTerminate

	lines, _ := draw(t, 170, 30, m)
	g := geom(m, 170, 30)
	xs := columnXs(g.widths)

	if !strings.Contains(lines[rowTop+1], confirmQuestion) {
		t.Errorf("the question opens the block: %q", lines[rowTop+1])
	}
	if !strings.Contains(lines[rowTop+2], "[ Yes ]") {
		t.Errorf("Yes belongs on its own line: %q", lines[rowTop+2])
	}
	if !strings.Contains(lines[rowTop+3], "[ Cancel ]") {
		t.Errorf("Cancel belongs on its own line: %q", lines[rowTop+3])
	}
	if strings.Contains(strings.Join(lines, "\n"), "[ Inspect ]") {
		t.Error("the buttons must step aside while the question is up")
	}

	if target, _ := Hit(m, 170, 30, xs[0]+4, rowTop+2); target != HitConfirmYes {
		t.Errorf("click on Yes: %v", target)
	}
	if target, _ := Hit(m, 170, 30, xs[0]+4, rowTop+3); target != HitConfirmCancel {
		t.Errorf("click on Cancel: %v", target)
	}
	if target, _ := Hit(m, 170, 30, xs[0]+4, rowTop+1); target != HitNone {
		t.Errorf("the question itself is not a button: %v", target)
	}
	_ = g
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
	if !strings.Contains(top, "config") || !strings.Contains(top, "api-worker-1") {
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
		if got, _ := HitInspect(m, 140, 28, x, 26); got != c.target {
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

	if got, _ := HitInspect(m, 140, 28, g.search.x+2, g.search.y); got != HitLogSearch {
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

func columnOf(t *testing.T, line, needle string) int {
	t.Helper()
	at := strings.Index(line, needle)
	if at < 0 {
		t.Fatalf("%q not found in %q", needle, line)
	}
	return len([]rune(line[:at]))
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
		"  QoS class          BestEffort",
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

func TestWorkloadConfigColoursWhatMatters(t *testing.T) {
	m := inspecting()
	m.Kind = kube.KindDeployment
	m.Inspect.Default = []string{
		"DEPLOYMENT",
		"  name               api",
		"  ready              2 of 3",
		"  updated            3",
		"  QoS class          BestEffort",
		"  strategy           RollingUpdate",
		"  selector           app=api",
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

	line, y := row("  ready")
	if fg, _, _ := styleAt(t, screen, line, "2 of 3", y).Decompose(); fg != warnColor {
		t.Errorf("a partly ready workload must stand out, got %v", fg)
	}
	line, y = row("BestEffort")
	if fg, _, _ := styleAt(t, screen, line, "BestEffort", y).Decompose(); fg != warnColor {
		t.Errorf("BestEffort deserves a warning colour, got %v", fg)
	}
	line, y = row("RollingUpdate")
	if fg, _, _ := styleAt(t, screen, line, "RollingUpdate", y).Decompose(); fg != tcell.ColorDefault {
		t.Errorf("the strategy stays plain, got %v", fg)
	}
	line, y = row("  selector")
	if fg, _, _ := styleAt(t, screen, line, "selector", y).Decompose(); fg != tcell.ColorGray {
		t.Errorf("labels stay dim, got %v", fg)
	}
}

func TestReadyFieldFollowsTheNumbers(t *testing.T) {
	cases := []struct {
		text string
		want tcell.Color
	}{
		{"3 of 3", tcell.ColorGreen},
		{"1 of 3", warnColor},
		{"0 of 3", tcell.ColorRed},
		{"0 of 0", tcell.ColorGray},
	}

	for _, c := range cases {
		if fg, _, _ := readyStyle(c.text).Decompose(); fg != c.want {
			t.Errorf("%q: %v, want %v", c.text, fg, c.want)
		}
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

func TestInspectMarksHiddenLinesOnTheFrame(t *testing.T) {
	m := inspecting()
	long := make([]string, 0, 60)
	for i := 0; i < 60; i++ {
		long = append(long, fmt.Sprintf("  field %02d          value", i))
	}
	m.Inspect.Default = long

	g := inspectGeom(140, 28)
	lines, _ := draw(t, 140, 28, m)

	bottom := lines[g.left.y+g.left.h-1]
	if !strings.Contains(bottom, "more") {
		t.Errorf("the bottom frame must say how much is left: %q", bottom)
	}
	if strings.Contains(strings.Join(lines, "\n"), "more below") {
		t.Error("the marker must not eat a line of content any more")
	}

	last := lines[g.left.y+g.room]
	if !strings.Contains(last, "field") {
		t.Errorf("every row of the pane must show content: %q", last)
	}

	m.Inspect.Offset = 10
	scrolled, _ := draw(t, 140, 28, m)
	if !strings.Contains(scrolled[g.left.y], "more") {
		t.Errorf("the top frame must say what is above: %q", scrolled[g.left.y])
	}
}

func TestMaxInspectOffsetCountsWrappedLines(t *testing.T) {
	m := inspecting()
	m.Inspect.Format = FormatTextual
	m.Inspect.Textual = []string{strings.Repeat("word ", 500)}

	limit := MaxInspectOffset(m, 140, 28)
	if limit <= 0 {
		t.Fatal("a wrapped paragraph must be scrollable")
	}

	g := inspectGeom(140, 28)
	if want := len(wrap(m.Inspect.Textual, g.textArea)) - g.room; limit != want {
		t.Errorf("limit %d, want %d", limit, want)
	}

	short := inspecting()
	short.Inspect.Default = []string{"POD", "  name    api"}
	if got := MaxInspectOffset(short, 140, 28); got != 0 {
		t.Errorf("content that fits must not scroll, got %d", got)
	}
}

func TestTableIsDrawnAsAGrid(t *testing.T) {
	m := model(sample(3))
	lines, _ := draw(t, 170, 30, m)
	g := geom(m, 170, 30)

	top := []rune(lines[tableTop])
	if top[0] != '┌' || top[g.total-1] != '┐' {
		t.Fatalf("top border: %q", lines[tableTop])
	}
	rule := []rune(lines[lineRule])
	if rule[0] != '├' || rule[g.total-1] != '┤' {
		t.Fatalf("header separator: %q", lines[lineRule])
	}
	bottom := []rune(lines[27])
	if bottom[0] != '└' || bottom[g.total-1] != '┘' {
		t.Fatalf("bottom border: %q", lines[27])
	}

	for _, x := range g.dividers() {
		if top[x] != '┬' {
			t.Errorf("column divider missing from the top border at %d", x)
		}
		if rule[x] != '┼' {
			t.Errorf("column divider missing from the separator at %d", x)
		}
		if bottom[x] != '┴' {
			t.Errorf("column divider missing from the bottom border at %d", x)
		}
		if row := []rune(lines[rowTop]); row[x] != '│' {
			t.Errorf("column divider missing from a data row at %d", x)
		}
	}

	row := []rune(lines[rowTop])
	if row[0] != '│' || row[g.total-1] != '│' {
		t.Errorf("rows must sit inside the frame: %q", lines[rowTop])
	}
}

func TestSortableColumnsOfferBothArrows(t *testing.T) {
	m := model(sample(3))
	lines, _ := draw(t, 200, 30, m)
	g := geom(m, 200, 30)
	xs := columnXs(g.widths)
	header := []rune(lines[lineHeader])

	for i, c := range g.cols {
		cell := string(header[xs[i] : xs[i]+g.widths[i]])
		switch {
		case c.key == "name":
			if !strings.Contains(cell, sortAsc) || strings.Contains(cell, sortBoth) {
				t.Errorf("with nothing else sorting, POD shows its direction: %q", cell)
			}
		case c.sortable:
			if !strings.Contains(cell, sortBoth) {
				t.Errorf("column %q must offer sorting: %q", c.key, cell)
			}
		default:
			if strings.Contains(cell, sortBoth) {
				t.Errorf("column %q must not offer sorting: %q", c.key, cell)
			}
		}
	}

	for _, c := range g.cols {
		if c.key == "exit" && c.sortable {
			t.Error("EXIT must stay unsortable")
		}
	}
}

func TestNameArrowFollowsItsOwnOrder(t *testing.T) {
	m := model(sample(3))
	g := geom(m, 200, 30)
	xs := columnXs(g.widths)

	cell := func(m Model) string {
		lines, _ := draw(t, 200, 30, m)
		row := []rune(lines[lineHeader])
		return string(row[xs[0] : xs[0]+g.widths[0]])
	}

	if got := cell(m); !strings.Contains(got, "POD "+sortAsc) {
		t.Errorf("alphabetical by default: %q", got)
	}

	m.NameOrder = kube.OrderDesc
	if got := cell(m); !strings.Contains(got, "POD "+sortDesc) {
		t.Errorf("against the alphabet: %q", got)
	}

	m.SortKey, m.SortOrder = "cpu", kube.OrderDesc
	if got := cell(m); strings.Contains(got, sortAsc) || strings.Contains(got, sortDesc) {
		t.Errorf("while a column sorts, POD hides its arrow too: %q", got)
	}

	m.SortKey, m.SortOrder = "", kube.OrderNone
	if got := cell(m); !strings.Contains(got, "POD "+sortDesc) {
		t.Errorf("dropping the column sort brings it back: %q", got)
	}
}

func TestNameOrderReordersTheTable(t *testing.T) {
	m := model(sample(4))
	ApplyFilter(&m)
	if !strings.HasPrefix(m.Rows[0].Name, "pod-00") {
		t.Fatalf("default order: %q", m.Rows[0].Name)
	}

	m.NameOrder = kube.OrderDesc
	ApplyFilter(&m)
	if !strings.HasPrefix(m.Rows[0].Name, "pod-03") {
		t.Fatalf("reversed order: %q", m.Rows[0].Name)
	}
}

func TestColumnSortHidesEveryOtherArrow(t *testing.T) {
	m := model(sample(3))
	m.SortKey, m.SortOrder = "mem", kube.OrderDesc

	lines, _ := draw(t, 200, 30, m)
	header := lines[lineHeader]

	if strings.Count(header, sortDesc) != 1 || strings.Contains(header, sortAsc) {
		t.Errorf("exactly one arrow, on the sorted column: %q", header)
	}
	if strings.Contains(header, sortBoth) {
		t.Errorf("the offers step aside as well: %q", header)
	}
}

func TestSortedColumnKeepsTheOnlyArrow(t *testing.T) {
	m := model(sample(3))
	m.SortKey, m.SortOrder = "cpu", kube.OrderDesc
	ApplyFilter(&m)

	lines, _ := draw(t, 200, 30, m)
	header := lines[lineHeader]

	if strings.Contains(header, sortBoth) {
		t.Errorf("while one column sorts, the others hide their arrows: %q", header)
	}
	if strings.Count(header, sortDesc) != 1 {
		t.Errorf("exactly one arrow must remain: %q", header)
	}
	at := strings.Index(header, "CPU")
	if !strings.Contains(header[at:at+8], sortDesc) {
		t.Errorf("the arrow belongs to the sorted column: %q", header)
	}

	m.SortOrder = kube.OrderAsc
	lines, _ = draw(t, 200, 30, m)
	if !strings.Contains(lines[lineHeader], sortAsc) || strings.Contains(lines[lineHeader], sortDesc) {
		t.Errorf("ascending must flip the arrow: %q", lines[lineHeader])
	}

	m.SortKey, m.SortOrder = "", kube.OrderNone
	lines, _ = draw(t, 200, 30, m)
	if strings.Count(lines[lineHeader], sortBoth) < 5 {
		t.Errorf("dropping the sort brings every arrow back: %q", lines[lineHeader])
	}
}

func TestHitFindsSortableHeaders(t *testing.T) {
	m := model(sample(3))
	g := geom(m, 200, 30)
	xs := columnXs(g.widths)

	for i, c := range g.cols {
		target, index := Hit(m, 200, 30, xs[i]+1, lineHeader)
		if !c.sortable {
			if target != HitNone {
				t.Errorf("column %q must not react: %v", c.key, target)
			}
			continue
		}
		if target != HitColumn || index != i {
			t.Errorf("column %q: got %v %d", c.key, target, index)
		}
	}
}

func TestSortingReordersTheRows(t *testing.T) {
	rows := sample(3)
	rows[0].CPU, rows[1].CPU, rows[2].CPU = 100, 900, 500

	m := model(rows)
	m.SortKey, m.SortOrder = "cpu", kube.OrderDesc
	ApplyFilter(&m)
	if m.Rows[0].CPU != 900 || m.Rows[2].CPU != 100 {
		t.Errorf("descending: %v", []float64{m.Rows[0].CPU, m.Rows[1].CPU, m.Rows[2].CPU})
	}

	m.SortOrder = kube.OrderAsc
	ApplyFilter(&m)
	if m.Rows[0].CPU != 100 || m.Rows[2].CPU != 900 {
		t.Errorf("ascending: %v", []float64{m.Rows[0].CPU, m.Rows[1].CPU, m.Rows[2].CPU})
	}
}

func TestNextOrderCycles(t *testing.T) {
	order := kube.OrderNone
	order = NextOrder(order)
	if order != kube.OrderDesc {
		t.Fatalf("first click sorts from the largest: %v", order)
	}
	order = NextOrder(order)
	if order != kube.OrderAsc {
		t.Fatalf("second click flips it: %v", order)
	}
	order = NextOrder(order)
	if order != kube.OrderNone {
		t.Fatalf("third click drops it: %v", order)
	}
}

func TestMarqueeHoldsThenWalksAndComesBack(t *testing.T) {
	overflow := 3
	want := []int{0, 0, 0, 0, 0, 0, 1, 2, 3, 3, 3, 3, 3, 3, 3, 2, 1, 0, 0, 0}

	for tick, expected := range want {
		if got := marqueeOffset(tick, overflow); got != expected {
			t.Fatalf("tick %d: offset %d, want %d", tick, got, expected)
		}
	}

	for tick := 0; tick < 50; tick++ {
		if got := marqueeOffset(tick, 0); got != 0 {
			t.Fatalf("a name that fits never moves, tick %d gave %d", tick, got)
		}
	}
}

func TestMarqueeTextWalksThroughTheName(t *testing.T) {
	name := "nestapp-backend-67fb4b65c4-f6vsc"
	width := 20

	first := marqueeText(name, width, 0)
	if first != name[:width] {
		t.Fatalf("it must start at the beginning: %q", first)
	}

	seen := map[string]bool{}
	for tick := 0; tick < 60; tick++ {
		window := marqueeText(name, width, tick)
		if len([]rune(window)) != width {
			t.Fatalf("tick %d: window %q is %d wide", tick, window, len([]rune(window)))
		}
		if !strings.Contains(name, window) {
			t.Fatalf("tick %d: %q is not part of the name", tick, window)
		}
		seen[window] = true
	}

	if !seen[name[len(name)-width:]] {
		t.Error("the end of the name must be reached")
	}
	if len(seen) < 5 {
		t.Errorf("the name must actually travel, saw %d windows", len(seen))
	}

	short := marqueeText("api-1", width, 7)
	if short != "api-1" {
		t.Errorf("short names stay put: %q", short)
	}
}

func TestOnlyTheChosenPodScrolls(t *testing.T) {
	m := model(sample(3))
	if NeedsMarquee(m, 100, 30) {
		t.Error("nothing must scroll while no pod is chosen")
	}

	m.Expanded = m.Rows[0].Name
	if !NeedsMarquee(m, 100, 30) {
		t.Error("a chosen pod with a long name must scroll")
	}

	short := model([]kube.Row{{Name: "api-1", CPUPct: -1, MemPct: -1, Worst: -1}})
	short.Expanded = "api-1"
	if NeedsMarquee(short, 200, 30) {
		t.Error("a name that fits must not keep the screen busy")
	}
}

func TestOnlyTheChosenNameMovesOnScreen(t *testing.T) {
	m := model(sample(3))
	m.Expanded = m.Rows[1].Name
	m.Tick = marqueePause + 4

	lines, _ := draw(t, 100, 30, m)
	g := geom(m, 100, 30)
	xs := columnXs(g.widths)

	cell := func(y int) string {
		row := []rune(lines[y])
		return string(row[xs[0] : xs[0]+g.widths[0]])
	}

	if !strings.HasPrefix(strings.TrimSpace(cell(rowTop)), "pod-00") {
		t.Errorf("an untouched name stays at its beginning: %q", cell(rowTop))
	}
	moved := strings.TrimSpace(cell(rowTop + 1))
	if strings.HasPrefix(moved, "pod-01") {
		t.Errorf("the chosen name must have moved: %q", moved)
	}
	if !strings.Contains(m.Rows[1].Name, moved) {
		t.Errorf("the window must stay inside the name: %q", moved)
	}
}

func TestDrawScrollsTheNameInPlace(t *testing.T) {
	rows := []kube.Row{{Name: "nestapp-backend-67fb4b65c4-f6vsc-with-a-very-long-tail", CPUPct: -1, MemPct: -1, Worst: -1}}
	m := model(rows)
	m.Expanded = rows[0].Name

	g := geom(m, 120, 30)
	first, _ := draw(t, 120, 30, m)

	m.Tick = marqueePause + 4
	later, _ := draw(t, 120, 30, m)

	cell := func(lines []string) string {
		row := []rune(lines[rowTop])
		xs := columnXs(g.widths)
		return string(row[xs[0] : xs[0]+g.widths[0]])
	}

	if cell(first) == cell(later) {
		t.Fatalf("the name must move: %q", cell(first))
	}
	if !strings.Contains(rows[0].Name, strings.TrimSpace(cell(later))) {
		t.Errorf("the window must stay inside the name: %q", cell(later))
	}
	if len([]rune(cell(later))) != g.widths[0] {
		t.Errorf("the cell keeps its width: %q", cell(later))
	}
}

func TestEveryCellIsCentred(t *testing.T) {
	rows := sample(2)
	rows[0].Status = "Running"
	rows[0].Restarts, rows[0].OOMs = 7, 3

	m := model(rows)
	lines, _ := draw(t, 170, 30, m)
	g := geom(m, 170, 30)
	xs := columnXs(g.widths)

	centred := func(y int, label string) {
		t.Helper()
		row := []rune(lines[y])
		for i, c := range g.cols {
			if c.key == "name" && y != lineHeader {
				continue
			}
			cell := string(row[xs[i] : xs[i]+g.widths[i]])
			text := strings.TrimSpace(cell)
			if text == "" {
				continue
			}
			left := strings.Index(cell, text)
			right := g.widths[i] - left - len([]rune(text))
			if left < right-1 || left > right+1 {
				t.Errorf("%s: %q in column %q is not centred (%d left, %d right)",
					label, text, c.key, left, right)
			}
		}
	}

	centred(lineHeader, "header")
	centred(rowTop, "row")
}

func TestNamesRestOnTheLeftEdgeOfTheirColumn(t *testing.T) {
	for _, kind := range kube.Kinds() {
		rows := sample(2)
		rows[0].Name = "api"

		m := model(rows)
		m.Kind = kind
		lines, _ := draw(t, 170, 30, m)
		g := geom(m, 170, 30)
		xs := columnXs(g.widths)

		index := -1
		for i, c := range g.cols {
			if c.key == "name" {
				index = i
			}
		}
		if index < 0 {
			t.Fatalf("%s: the table has no name column", kind)
		}

		cell := []rune(lines[rowTop])[xs[index] : xs[index]+g.widths[index]]
		if !strings.HasPrefix(string(cell), "api ") {
			t.Errorf("%s: the name must start at the left edge: %q", kind, string(cell))
		}
	}
}

func TestHeaderKeepsItsArrowWhileCentred(t *testing.T) {
	m := model(sample(2))
	m.SortKey, m.SortOrder = "mem", kube.OrderDesc

	lines, _ := draw(t, 170, 30, m)
	g := geom(m, 170, 30)
	xs := columnXs(g.widths)
	row := []rune(lines[lineHeader])

	for i, c := range g.cols {
		if c.key != "mem" {
			continue
		}
		cell := string(row[xs[i] : xs[i]+g.widths[i]])
		text := strings.TrimSpace(cell)
		if text != "MEM "+sortDesc {
			t.Fatalf("header cell: %q", cell)
		}
		left := strings.Index(cell, text)
		right := g.widths[i] - left - len([]rune(text))
		if left < right-1 || left > right+1 {
			t.Errorf("the sorted header is not centred: %q", cell)
		}
	}
}

func TestConfirmationAsksTheSameQuestionForBothActions(t *testing.T) {
	for _, kind := range []kube.Kind{kube.KindPod, kube.KindDeployment} {
		for _, action := range []Action{ActionRestart, ActionTerminate} {
			m := model(sample(2))
			m.Kind = kind
			m.Expanded = m.Rows[0].Name
			m.Confirm = action

			lines, _ := draw(t, 170, 30, m)
			if !strings.Contains(lines[rowTop+1], "Are you sure?") {
				t.Errorf("%v %v: %q", kind, action, lines[rowTop+1])
			}
			if strings.Contains(lines[rowTop+1], "pod-00") {
				t.Errorf("the name is already on the row above: %q", lines[rowTop+1])
			}
		}
	}
}

func TestKindBoxShowsAndOffersTheModes(t *testing.T) {
	m := model(sample(2))
	lines, _ := draw(t, 170, 30, m)
	g := geom(m, 170, 30)

	box := lines[g.kindBox.y+1]
	if !strings.Contains(box, "Pods") {
		t.Errorf("the box must name the current mode: %q", box)
	}
	if !strings.HasPrefix(strings.TrimSpace(lines[g.kindBox.y]), "\u250c") {
		t.Errorf("the mode must sit in a frame: %q", lines[g.kindBox.y])
	}
	if g.kindBox.y+2 >= lineStatus {
		t.Error("the frame must close above the counters line")
	}

	if target, _ := Hit(m, 170, 30, g.kindBox.x+2, g.kindBox.y+1); target != HitKindBox {
		t.Errorf("click on the mode box: %v", target)
	}

	m.Focus = FocusKind
	listed := m.Options()
	want := []string{"Pods", "Deployments", "ReplicaSets", "DaemonSets", "StatefulSets"}
	if len(listed) != len(want) {
		t.Fatalf("the menu must offer every kind: %v", listed)
	}
	for i := range want {
		if listed[i] != want[i] {
			t.Errorf("menu order: %v", listed)
		}
	}

	open, _ := draw(t, 170, 30, m)
	body := strings.Join(open[g.kindBox.y:g.kindBox.y+9], "\n")
	for _, name := range want {
		if !strings.Contains(body, name) {
			t.Errorf("the open menu misses %q:\n%s", name, body)
		}
	}
}

func TestWorkloadColumnsReplaceThePodOnes(t *testing.T) {
	rows := sample(2)
	rows[0].Created = time.Now().Add(-50 * time.Hour)
	rows[0].LastRestart = time.Now().Add(-90 * time.Minute)

	m := model(rows)
	m.Kind = kube.KindDeployment
	lines, _ := draw(t, 200, 30, m)
	header := lines[lineHeader]

	if !strings.Contains(header, "DEPLOYMENT") {
		t.Errorf("the name column must follow the mode: %q", header)
	}
	for _, want := range []string{"CREATED", "LAST POD RESTART"} {
		if !strings.Contains(header, want) {
			t.Errorf("header misses %q: %q", want, header)
		}
	}
	if strings.Contains(header, "EXIT") {
		t.Errorf("EXIT belongs to pods only: %q", header)
	}
	if strings.Count(header, "LAST RESTART") != 0 && !strings.Contains(header, "LAST POD RESTART") {
		t.Errorf("LAST RESTART must be replaced: %q", header)
	}

	row := lines[rowTop]
	if !strings.Contains(row, "2d2h ago") {
		t.Errorf("created must read as an age: %q", row)
	}
	if !strings.Contains(row, "1h30m ago (at ") {
		t.Errorf("the last pod restart keeps the pod format: %q", row)
	}
}

func TestLogPaneNamesItsPod(t *testing.T) {
	m := inspecting()
	m.Inspect.LogPod = "api-abc-1"
	m.Inspect.LogPods = []string{"api-abc-1", "api-abc-2"}

	lines, _ := draw(t, 140, 28, m)
	if !strings.Contains(lines[inspectTop], "api-abc-1 \u21c5") {
		t.Errorf("the pane must name the pod it streams: %q", lines[inspectTop])
	}
	if !strings.Contains(lines[inspectTop], sortBoth) {
		t.Errorf("with several pods it must look pickable: %q", lines[inspectTop])
	}

	g := inspectGeomFor(140, 28, len(m.Inspect.LogPods))
	if target, _ := HitInspect(m, 140, 28, g.podPick.x+2, g.podPick.y); target != HitLogPod {
		t.Errorf("click on the pane title: %v", target)
	}

	m.Inspect.PodPicker = true
	picker, _ := draw(t, 140, 28, m)
	body := strings.Join(picker[inspectTop:inspectTop+5], "\n")
	for _, name := range m.Inspect.LogPods {
		if !strings.Contains(body, name) {
			t.Errorf("the picker misses %q:\n%s", name, body)
		}
	}
	if target, index := HitInspect(m, 140, 28, g.podList.x+3, g.podList.y+2); target != HitLogPodItem || index != 1 {
		t.Errorf("click on the second pod: %v %d", target, index)
	}
}

func TestSinglePodPaneStaysPlain(t *testing.T) {
	m := inspecting()
	m.Inspect.LogPod = "api-worker-1"
	m.Inspect.LogPods = []string{"api-worker-1"}

	lines, _ := draw(t, 140, 28, m)
	if strings.Contains(lines[inspectTop], sortBoth) {
		t.Errorf("one pod means nothing to pick: %q", lines[inspectTop])
	}
}

func TestLongLogLinesWrapInsteadOfBeingCut(t *testing.T) {
	m := inspecting()
	tail := "the quick brown fox jumps over the lazy dog and keeps running well past the edge of this pane"
	m.Inspect.Logs = []LogLine{{Time: "18:00:01", Text: tail}}

	lines, _ := draw(t, 140, 28, m)
	g := inspectGeomFor(140, 28, 0)

	first, second := "", ""
	for i, line := range lines {
		if strings.Contains(line, "the quick brown fox") {
			first, second = line, lines[i+1]
			break
		}
	}
	if first == "" {
		t.Fatal("the log line is not on screen")
	}

	cut := func(line string) string {
		return string([]rune(line)[g.logs.x : g.logs.x+g.logs.w])
	}
	joined := strings.Join(strings.Fields(cut(first)+" "+cut(second)), " ")
	if !strings.Contains(joined, tail) {
		t.Errorf("the whole line must survive the wrap:\n%q\n%q", first, second)
	}

	pane := []rune(second)[g.logs.x : g.logs.x+g.logs.w]
	if !strings.HasPrefix(string(pane), strings.Repeat(" ", len("18:00:01 "))) {
		t.Errorf("the tail must sit under the message, not under the clock: %q", string(pane))
	}
}

func TestWrappedLogLinesCountAsRowsForScrolling(t *testing.T) {
	m := inspecting()
	long := strings.Repeat("word ", 400)
	m.Inspect.Logs = []LogLine{{Time: "18:00:01", Text: long}}

	if MaxLogOffset(m, 140, 28) == 0 {
		t.Fatal("one very long line must still be scrollable")
	}

	m.Inspect.Logs = []LogLine{{Time: "18:00:01", Text: "short"}}
	if got := MaxLogOffset(m, 140, 28); got != 0 {
		t.Errorf("a single short line has nothing to scroll: %d", got)
	}
}

func TestWrappedLogLinesKeepTheirHighlight(t *testing.T) {
	m := inspecting()
	m.Inspect.Logs = []LogLine{{
		Time: "18:00:01",
		Text: strings.Repeat("filler ", 12) + "needle at the end of a long line",
	}}
	m.Inspect.LogQuery = "needle"

	lines, screen := draw(t, 140, 28, m)
	for i, line := range lines {
		if !strings.Contains(line, "needle") {
			continue
		}
		if _, bg, _ := styleAt(t, screen, line, "needle", i).Decompose(); bg != warnColor {
			t.Errorf("the match must stay highlighted after the wrap, got %v", bg)
		}
		return
	}
	t.Fatal("the match is not on screen")
}

func TestKindBoxKeepsItsWidthWhateverIsChosen(t *testing.T) {
	widths := make(map[int]kube.Kind, len(kube.Kinds()))
	for _, kind := range kube.Kinds() {
		m := model(sample(2))
		m.Kind = kind
		widths[geom(m, 170, 30).kindBox.w] = kind
	}
	if len(widths) != 1 {
		t.Fatalf("the box must not resize with the name: %v", widths)
	}

	m := model(sample(2))
	m.Focus = FocusKind
	g := geom(m, 170, 30)
	if g.dropdown.w != g.kindBox.w {
		t.Errorf("the open menu must match the box: %d against %d", g.dropdown.w, g.kindBox.w)
	}
	if g.dropdown.x != g.kindBox.x {
		t.Errorf("the menu must hang under the box: %d against %d", g.dropdown.x, g.kindBox.x)
	}
}

func TestEmptyTableHoldsANoteAboveIt(t *testing.T) {
	m := model(nil)
	m.Kind = kube.KindDeployment

	lines, _ := draw(t, 170, 30, m)
	g := geom(m, 170, 30)

	top := -1
	for i := rowTop; i < rowTop+g.room; i++ {
		if strings.Contains(lines[i], "Oops!") {
			top = i
			break
		}
	}
	if top < 0 {
		t.Fatalf("the note must float inside the table:\n%s", strings.Join(lines[rowTop:rowTop+g.room], "\n"))
	}
	if !strings.Contains(lines[top+1], "No resources found in this namespace.") {
		t.Errorf("the second line: %q", lines[top+1])
	}

	frame := []rune(lines[top-1])
	if !strings.Contains(string(frame), "┌") || !strings.Contains(lines[top+2], "└") {
		t.Errorf("the note must be framed:\n%q\n%q", lines[top-1], lines[top+2])
	}

	left := strings.Index(lines[top-1], "┌")
	if column := strings.Index(lines[top-1], "│"); column >= 0 && column > left {
		t.Errorf("the frame must cover the column dividers: %q", lines[top-1])
	}

	middle := rowTop + g.room/2
	if top < middle-3 || top > middle+1 {
		t.Errorf("the note must rest in the middle of the table: row %d of %d..%d", top, rowTop, rowTop+g.room)
	}
}

func TestEmptyNoteSpeaksOfTheChosenKind(t *testing.T) {
	m := model(sample(3))
	m.Kind = kube.KindStatefulSet
	m.PodQuery = "nothing-matches"
	ApplyFilter(&m)

	body := strings.Join(mustDraw(t, m), "\n")
	if !strings.Contains(body, "No statefulset matches nothing-matches.") {
		t.Errorf("the note must name the kind: %s", body)
	}

	m = model(nil)
	m.Kind = kube.KindDaemonSet
	m.Loaded = false
	if body := strings.Join(mustDraw(t, m), "\n"); !strings.Contains(body, "Asking the cluster for daemonsets...") {
		t.Errorf("the wait must name the kind: %s", body)
	}
}

func mustDraw(t *testing.T, m Model) []string {
	t.Helper()
	lines, _ := draw(t, 170, 30, m)
	return lines[rowTop : rowTop+geom(m, 170, 30).room]
}

func TestStatusLineFollowsWhatIsWrong(t *testing.T) {
	calm := model(sample(3))
	for i := range calm.All {
		calm.All[i].Severity, calm.All[i].CPUPct, calm.All[i].MemPct, calm.All[i].Worst = kube.Good, 10, 10, 10
	}
	ApplyFilter(&calm)

	lines, _ := draw(t, 170, 30, calm)
	if !strings.Contains(lines[lineStatus], "Nothing is wrong in this namespace.") {
		t.Errorf("a calm namespace: %q", lines[lineStatus])
	}
	if g := geom(calm, 170, 30); g.critical.w != 0 || g.dimCPU.w != 0 {
		t.Error("nothing that is not wrong may be clicked")
	}

	m := troubled()
	lines, _ = draw(t, 170, 30, m)
	if !strings.Contains(lines[lineStatus], "Facing 2 critical and 1 warning issues regarding status, cpu and memory.") {
		t.Errorf("a troubled namespace: %q", lines[lineStatus])
	}

	g := geom(m, 170, 30)
	if target, _ := Hit(m, 170, 30, g.critical.x+1, lineStatus); target != HitCritical {
		t.Errorf("the count must stay clickable: %v", target)
	}
	if target, _ := Hit(m, 170, 30, g.dimMemory.x+1, lineStatus); target != HitDimMemory {
		t.Errorf("the dimension must stay clickable: %v", target)
	}
}

func TestChosenFilterStaysOnTheLineAtZero(t *testing.T) {
	m := troubled()
	m.Level = kube.LevelWarning
	m.Dimension = kube.DimStatus
	ApplyFilter(&m)

	lines, _ := draw(t, 170, 30, m)
	if !strings.Contains(lines[lineStatus], "0 warning") {
		t.Fatalf("a chosen level must stay visible at zero: %q", lines[lineStatus])
	}

	g := geom(m, 170, 30)
	if target, _ := Hit(m, 170, 30, g.warning.x+1, lineStatus); target != HitWarning {
		t.Errorf("and clickable, to let it go: %v", target)
	}
}
