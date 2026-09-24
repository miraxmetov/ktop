package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/miraxmetov/ktop/internal/kube"
	"github.com/miraxmetov/ktop/internal/ui"
)

func testApp(t *testing.T, names ...string) *app {
	t.Helper()
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("init screen: %v", err)
	}
	screen.SetSize(170, 40)

	client := kube.NewWithClients(
		k8sfake.NewSimpleClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "production"}}),
		metricsfake.NewSimpleClientset(),
	)

	rows := make([]kube.Row, 0, len(names))
	for _, name := range names {
		rows = append(rows, kube.Row{Name: name, Status: "Running", CPUPct: -1, MemPct: -1, Worst: -1})
	}

	a := &app{
		screen:     screen,
		client:     client,
		namespace:  "production",
		interval:   2 * time.Second,
		pods:       make(chan podResult, 4),
		namespaces: make(chan namespaceResult, 4),
		model: &ui.Model{
			Namespace: "production",
			All:       rows,
			Cursor:    noSelection,
		},
	}
	ui.ApplyFilter(a.model)
	return a
}

func frame(t *testing.T, a *app) []string {
	t.Helper()
	ui.Draw(a.screen, *a.model)
	sim, ok := a.screen.(tcell.SimulationScreen)
	if !ok {
		t.Fatal("simulation screen expected")
	}
	cells, w, h := sim.GetContents()
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
	return lines
}

func press(a *app, key tcell.Key, r rune) bool {
	quit := a.handleKey(tcell.NewEventKey(key, r, tcell.ModNone))
	a.clamp()
	return quit
}

func typeText(a *app, text string) {
	for _, r := range text {
		press(a, tcell.KeyRune, r)
	}
}

func TestPodSearchFiltersAndRestoresTable(t *testing.T) {
	a := testApp(t, "api-worker-1", "api-worker-2", "web-1", "cache-1")

	press(a, tcell.KeyRune, '/')
	if a.model.Focus != ui.FocusPods {
		t.Fatal("slash must focus the pod search")
	}

	typeText(a, "api")
	if a.model.PodQuery != "api" {
		t.Fatalf("query: %q", a.model.PodQuery)
	}
	if len(a.model.Rows) != 2 {
		t.Fatalf("want 2 matching rows, got %d", len(a.model.Rows))
	}

	press(a, tcell.KeyBackspace2, 0)
	if a.model.PodQuery != "ap" || len(a.model.Rows) != 2 {
		t.Fatalf("backspace: query %q, rows %d", a.model.PodQuery, len(a.model.Rows))
	}

	press(a, tcell.KeyEnter, 0)
	if a.model.Focus != ui.FocusTable {
		t.Fatal("Enter must return to the table")
	}
	if len(a.model.Rows) != 2 {
		t.Fatalf("filter must survive Enter, got %d rows", len(a.model.Rows))
	}
	if a.model.Cursor != 0 {
		t.Fatalf("Enter must land on the highlighted pod, got %d", a.model.Cursor)
	}

	press(a, tcell.KeyEscape, 0)
	if a.model.Cursor != noSelection {
		t.Fatalf("the first Esc drops the selection, got %d", a.model.Cursor)
	}

	press(a, tcell.KeyEscape, 0)
	if a.model.PodQuery != "" || len(a.model.Rows) != 4 {
		t.Fatalf("Esc must clear the filter: query %q, rows %d", a.model.PodQuery, len(a.model.Rows))
	}
}

func TestTypingInSearchDoesNotTriggerTableKeys(t *testing.T) {
	a := testApp(t, "queue-1", "quiet-2", "web-3")

	press(a, tcell.KeyRune, '/')
	if quit := press(a, tcell.KeyRune, 'q'); quit {
		t.Fatal("q inside the search box must not quit")
	}
	typeText(a, "ui")
	if a.model.PodQuery != "qui" {
		t.Fatalf("query: %q", a.model.PodQuery)
	}
	if len(a.model.Rows) != 1 || a.model.Rows[0].Name != "quiet-2" {
		t.Fatalf("rows: %v", a.model.Rows)
	}

	press(a, tcell.KeyEnter, 0)
	if quit := press(a, tcell.KeyRune, 'q'); !quit {
		t.Fatal("q must quit once the table has focus again")
	}
}

func TestNamespaceSearchSwitchesNamespace(t *testing.T) {
	a := testApp(t, "api-1", "web-1")
	a.model.Namespaces = []string{"default", "kube-system", "prod-blue", "production"}

	press(a, tcell.KeyRune, 'n')
	if a.model.Focus != ui.FocusNamespace {
		t.Fatal("n must focus the namespace search")
	}

	typeText(a, "prod")
	if got := a.model.NamespaceMatches(); len(got) != 2 {
		t.Fatalf("matches: %v", got)
	}

	press(a, tcell.KeyEnter, 0)
	if a.namespace != "prod-blue" || a.model.Namespace != "prod-blue" {
		t.Fatalf("namespace after Enter: %q", a.namespace)
	}
	if a.model.NamespaceQuery != "" || a.model.Focus != ui.FocusTable {
		t.Fatalf("input must reset: query %q focus %v", a.model.NamespaceQuery, a.model.Focus)
	}
	if len(a.model.All) != 0 || a.model.Cursor != noSelection {
		t.Fatal("rows of the previous namespace must be dropped")
	}

	select {
	case res := <-a.pods:
		if res.namespace != "prod-blue" {
			t.Fatalf("refresh was requested for %q", res.namespace)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("switching namespaces must trigger a refresh")
	}
}

func TestNamespaceSearchExactMatchWins(t *testing.T) {
	a := testApp(t, "api-1")
	a.model.Namespaces = []string{"prod", "prod-blue", "production"}

	press(a, tcell.KeyRune, 'n')
	typeText(a, "production")
	press(a, tcell.KeyEnter, 0)

	if a.namespace != "production" {
		t.Fatalf("exact match must win, got %q", a.namespace)
	}
}

func TestNamespaceSearchWithoutListPermission(t *testing.T) {
	a := testApp(t, "api-1")
	a.model.NamespaceNote = "no permission to list namespaces, type a name and press Enter"

	press(a, tcell.KeyRune, 'n')
	typeText(a, "staging")
	press(a, tcell.KeyEnter, 0)

	if a.namespace != "staging" {
		t.Fatalf("free text must still switch, got %q", a.namespace)
	}
}

func TestNamespaceSearchEscapeKeepsNamespace(t *testing.T) {
	a := testApp(t, "api-1")

	press(a, tcell.KeyRune, 'n')
	typeText(a, "kube")
	press(a, tcell.KeyEscape, 0)

	if a.namespace != "production" || a.model.NamespaceQuery != "" {
		t.Fatalf("Esc must cancel: namespace %q query %q", a.namespace, a.model.NamespaceQuery)
	}
	if a.model.Focus != ui.FocusTable {
		t.Fatal("Esc must return focus to the table")
	}
}

func TestRefreshKeepsCursorOnTheSamePod(t *testing.T) {
	a := testApp(t, "api-1", "api-2", "web-1")
	a.model.Cursor = 2
	if a.model.SelectedName() != "web-1" {
		t.Fatalf("selected %q", a.model.SelectedName())
	}

	a.model.All = []kube.Row{
		{Name: "api-3", CPUPct: -1, MemPct: -1, Worst: -1},
		{Name: "web-1", CPUPct: -1, MemPct: -1, Worst: -1},
	}
	a.reselect()

	if a.model.SelectedName() != "web-1" {
		t.Fatalf("cursor must follow the pod name, selected %q", a.model.SelectedName())
	}

	a.model.All = []kube.Row{{Name: "api-3", CPUPct: -1, MemPct: -1, Worst: -1}}
	a.reselect()
	if a.model.Cursor != noSelection {
		t.Fatalf("a vanished pod must leave nothing selected, got %d", a.model.Cursor)
	}
}

func TestFilterSurvivesRefresh(t *testing.T) {
	a := testApp(t, "api-1", "web-1")
	press(a, tcell.KeyRune, '/')
	typeText(a, "api")
	press(a, tcell.KeyEnter, 0)

	a.model.All = []kube.Row{
		{Name: "api-2", CPUPct: -1, MemPct: -1, Worst: -1},
		{Name: "web-2", CPUPct: -1, MemPct: -1, Worst: -1},
	}
	a.reselect()

	if len(a.model.Rows) != 1 || a.model.Rows[0].Name != "api-2" {
		t.Fatalf("filter must be re-applied to fresh names, got %v", a.model.Rows)
	}
}

func TestShiftTabSwitchesFields(t *testing.T) {
	a := testApp(t, "api-1")

	press(a, tcell.KeyTab, 0)
	if a.model.Focus != ui.FocusPods {
		t.Fatalf("Tab from the table must open the pod search, got %v", a.model.Focus)
	}
	press(a, tcell.KeyBacktab, 0)
	if a.model.Focus != ui.FocusNamespace {
		t.Fatalf("focus: %v", a.model.Focus)
	}
	press(a, tcell.KeyBacktab, 0)
	if a.model.Focus != ui.FocusPods {
		t.Fatalf("focus: %v", a.model.Focus)
	}
}

func TestTabCompletesTheHighlightedPod(t *testing.T) {
	a := testApp(t, "api-worker-1", "api-worker-2", "web-1")

	press(a, tcell.KeyRune, '/')
	typeText(a, "api")
	press(a, tcell.KeyTab, 0)

	if a.model.PodQuery != "api-worker-1" {
		t.Fatalf("Tab must complete to the first match, got %q", a.model.PodQuery)
	}
	if a.model.Focus != ui.FocusPods {
		t.Fatal("completing must keep the field open")
	}
	if len(a.model.Rows) != 1 || a.model.Rows[0].Name != "api-worker-1" {
		t.Fatalf("completed query must narrow the table, got %v", a.model.Rows)
	}

	press(a, tcell.KeyEnter, 0)
	if a.model.Focus != ui.FocusTable || a.model.SelectedName() != "api-worker-1" {
		t.Fatalf("Enter after completion: focus %v, selected %q", a.model.Focus, a.model.SelectedName())
	}
}

func TestTabCompletesTheHighlightedNamespace(t *testing.T) {
	a := testApp(t, "api-1")
	a.model.Namespaces = []string{"default", "prod-blue", "production"}

	press(a, tcell.KeyRune, 'n')
	typeText(a, "prod")
	press(a, tcell.KeyDown, 0)
	press(a, tcell.KeyTab, 0)

	if a.model.NamespaceQuery != "production" {
		t.Fatalf("Tab must complete the highlighted entry, got %q", a.model.NamespaceQuery)
	}
	if a.namespace != "production" {
		press(a, tcell.KeyEnter, 0)
	}
	if a.namespace != "production" {
		t.Fatalf("Enter must apply the completed namespace, got %q", a.namespace)
	}
}

func TestTabWithoutMatchesSwitchesField(t *testing.T) {
	a := testApp(t, "api-1")

	press(a, tcell.KeyRune, '/')
	typeText(a, "zzz")
	press(a, tcell.KeyTab, 0)

	if a.model.Focus != ui.FocusNamespace {
		t.Fatalf("with nothing to complete Tab must move on, got %v", a.model.Focus)
	}
	if a.model.PodQuery != "zzz" {
		t.Fatalf("query must survive, got %q", a.model.PodQuery)
	}
}

func TestClickOnEmptySpaceClosesTheDropdown(t *testing.T) {
	a := testApp(t, "api-1", "web-1")
	a.model.Namespaces = []string{"default", "production"}

	press(a, tcell.KeyRune, 'n')
	if a.model.Focus != ui.FocusNamespace {
		t.Fatal("namespace search must be open")
	}

	click(a, 160, 30)
	if a.model.Focus != ui.FocusTable {
		t.Fatalf("a click on empty space must close the list, got %v", a.model.Focus)
	}
	if a.namespace != "production" {
		t.Fatalf("closing the list must not switch namespaces, got %q", a.namespace)
	}

	press(a, tcell.KeyRune, '/')
	click(a, 168, 38)
	if a.model.Focus != ui.FocusTable {
		t.Fatalf("the pod list must close the same way, got %v", a.model.Focus)
	}
}

func TestCtrlCQuitsFromSearch(t *testing.T) {
	a := testApp(t, "api-1")
	press(a, tcell.KeyRune, '/')
	if quit := press(a, tcell.KeyCtrlC, 0); !quit {
		t.Fatal("Ctrl+C must quit from the search box")
	}
}

func click(a *app, x, y int) {
	a.handleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
	a.clamp()
}

func TestClickFocusesSearchFields(t *testing.T) {
	a := testApp(t, "api-1", "web-1")

	click(a, 3, 2)
	if a.model.Focus != ui.FocusNamespace {
		t.Fatalf("click on the namespace box: focus %v", a.model.Focus)
	}

	press(a, tcell.KeyEscape, 0)
	click(a, 3, 7)
	if a.model.Focus != ui.FocusPods {
		t.Fatalf("click on the pod box: focus %v", a.model.Focus)
	}
}

func TestClickOnDropdownSwitchesNamespace(t *testing.T) {
	a := testApp(t, "api-1")
	a.model.Namespaces = []string{"default", "production", "staging"}

	click(a, 3, 3)
	click(a, 3, 8)

	if a.namespace != "staging" {
		t.Fatalf("clicking the third item must switch to staging, got %q", a.namespace)
	}
	if a.model.Focus != ui.FocusTable {
		t.Fatal("after choosing a namespace the table must get focus back")
	}
}

func TestClickOnRowMovesCursor(t *testing.T) {
	a := testApp(t, "api-1", "api-2", "web-1", "cache-1")

	click(a, 4, 11+2)
	if a.model.Cursor != 2 {
		t.Fatalf("cursor after the click: %d", a.model.Cursor)
	}
	if a.model.Focus != ui.FocusTable {
		t.Fatal("clicking a row must focus the table")
	}
}

func TestWheelScrollsWithoutSelecting(t *testing.T) {
	names := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		names = append(names, fmt.Sprintf("pod-%02d", i))
	}
	a := testApp(t, names...)

	a.handleMouse(tcell.NewEventMouse(5, 12, tcell.WheelDown, tcell.ModNone))
	a.clamp()
	if a.model.Offset != 1 {
		t.Fatalf("one notch must scroll one row, got offset %d", a.model.Offset)
	}
	if a.model.Cursor != noSelection {
		t.Fatalf("scrolling must not select anything, got %d", a.model.Cursor)
	}

	a.handleMouse(tcell.NewEventMouse(5, 12, tcell.WheelUp, tcell.ModNone))
	a.clamp()
	if a.model.Offset != 0 {
		t.Fatalf("offset after scrolling back: %d", a.model.Offset)
	}

	press(a, tcell.KeyRune, '/')
	a.handleMouse(tcell.NewEventMouse(5, 8, tcell.WheelDown, tcell.ModNone))
	if a.model.Choice != 1 {
		t.Fatalf("wheel inside the dropdown must move the choice, got %d", a.model.Choice)
	}
}

func TestNothingIsSelectedUntilYouAskForIt(t *testing.T) {
	a := testApp(t, "api-1", "api-2", "web-1")

	if a.model.Cursor != noSelection {
		t.Fatalf("a fresh table must have no selection, got %d", a.model.Cursor)
	}

	click(a, 4, 11)
	if a.model.Cursor != 0 || a.model.SelectedName() != "api-1" {
		t.Fatalf("a click must select that row, got %d %q", a.model.Cursor, a.model.SelectedName())
	}

	if quit := press(a, tcell.KeyEscape, 0); quit {
		t.Fatal("Esc must drop the selection first")
	}
	if a.model.Cursor != noSelection {
		t.Fatalf("Esc must clear the selection, got %d", a.model.Cursor)
	}
}

func TestArrowsStartTheSelectionAtTheTopOfTheView(t *testing.T) {
	names := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		names = append(names, fmt.Sprintf("pod-%02d", i))
	}
	a := testApp(t, names...)

	a.handleMouse(tcell.NewEventMouse(5, 12, tcell.WheelDown, tcell.ModNone))
	a.handleMouse(tcell.NewEventMouse(5, 12, tcell.WheelDown, tcell.ModNone))
	a.clamp()

	press(a, tcell.KeyDown, 0)
	if a.model.SelectedName() != "pod-02" {
		t.Fatalf("the first arrow selects the top visible row, got %q", a.model.SelectedName())
	}
	press(a, tcell.KeyDown, 0)
	if a.model.SelectedName() != "pod-03" {
		t.Fatalf("the next arrow steps one row, got %q", a.model.SelectedName())
	}
}

func TestArrowsMoveDropdownChoiceNotCursor(t *testing.T) {
	a := testApp(t, "api-1", "api-2", "api-3")
	a.model.Cursor = 0

	press(a, tcell.KeyRune, '/')
	press(a, tcell.KeyDown, 0)
	press(a, tcell.KeyDown, 0)

	if a.model.Choice != 2 {
		t.Fatalf("choice: %d", a.model.Choice)
	}
	if a.model.Cursor != 0 {
		t.Fatalf("table cursor must stay put while the dropdown is open, got %d", a.model.Cursor)
	}

	press(a, tcell.KeyEnter, 0)
	if a.model.Focus != ui.FocusTable {
		t.Fatal("Enter must return to the table")
	}
	if a.model.SelectedName() != "api-3" {
		t.Fatalf("Enter must jump to the chosen pod, selected %q", a.model.SelectedName())
	}
}

func TestDropdownChoiceClampsToOptions(t *testing.T) {
	a := testApp(t, "api-1", "web-1")

	press(a, tcell.KeyRune, '/')
	for i := 0; i < 10; i++ {
		press(a, tcell.KeyDown, 0)
	}
	if a.model.Choice != 1 {
		t.Fatalf("choice must clamp to the last option, got %d", a.model.Choice)
	}

	typeText(a, "zzz")
	if a.model.Choice != 0 {
		t.Fatalf("typing must reset the choice, got %d", a.model.Choice)
	}
}

func writeKubeconfig(t *testing.T, dir, name, namespace string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	body := "apiVersion: v1\nkind: Config\nclusters:\n- name: stub\n  cluster:\n    server: https://127.0.0.1:6443\n" +
		"contexts:\n- name: stub\n  context:\n    cluster: stub\n    user: stub\n    namespace: " + namespace + "\n" +
		"current-context: stub\nusers:\n- name: stub\n  user:\n    token: test\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	return path
}

func TestKubeconfigFieldOpensOnKeyAndClick(t *testing.T) {
	a := testApp(t, "api-1")
	a.model.Kubeconfig = "/home/u/.kube/config"

	press(a, tcell.KeyRune, 'c')
	if a.model.Focus != ui.FocusKubeconfig {
		t.Fatalf("c must open the kubeconfig field, got %v", a.model.Focus)
	}
	if a.model.PathQuery != "/home/u/.kube/" {
		t.Fatalf("the field must start in the current directory, got %q", a.model.PathQuery)
	}

	press(a, tcell.KeyEscape, 0)
	if a.model.Focus != ui.FocusTable || a.model.PathQuery != "" {
		t.Fatalf("Esc must close it: focus %v query %q", a.model.Focus, a.model.PathQuery)
	}

	lines := frame(t, a)
	column := strings.Index(lines[1], "/home")
	if column < 0 {
		t.Fatalf("path not drawn: %q", lines[1])
	}
	click(a, column+2, 1)
	if a.model.Focus != ui.FocusKubeconfig {
		t.Fatalf("a click on the path must open the field, got %v", a.model.Focus)
	}
}

func TestKubeconfigCompletionWalksDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "clusters"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeKubeconfig(t, filepath.Join(dir, "clusters"), "prod.yaml", "production")

	a := testApp(t, "api-1")
	a.model.Kubeconfig = filepath.Join(dir, "config")
	press(a, tcell.KeyRune, 'c')

	if len(a.model.PathOptions) == 0 {
		t.Fatal("opening the field must list the directory")
	}
	if !strings.HasSuffix(a.model.PathOptions[0], "clusters/") {
		t.Fatalf("directories come first, got %v", a.model.PathOptions)
	}

	press(a, tcell.KeyEnter, 0)
	if a.model.Focus != ui.FocusKubeconfig {
		t.Fatal("choosing a directory must keep the field open")
	}
	if !strings.HasSuffix(a.model.PathQuery, "clusters/") {
		t.Fatalf("query must descend into the directory, got %q", a.model.PathQuery)
	}
	if len(a.model.PathOptions) != 1 || !strings.HasSuffix(a.model.PathOptions[0], "prod.yaml") {
		t.Fatalf("options must follow the new directory, got %v", a.model.PathOptions)
	}
}

func TestKubeconfigApplySwitchesConfig(t *testing.T) {
	dir := t.TempDir()
	path := writeKubeconfig(t, dir, "prod.yaml", "payments")

	a := testApp(t, "api-1")
	a.model.Kubeconfig = filepath.Join(dir, "config")
	press(a, tcell.KeyRune, 'c')
	press(a, tcell.KeyEnter, 0)

	if a.model.Kubeconfig != path {
		t.Fatalf("kubeconfig after Enter: %q, want %q", a.model.Kubeconfig, path)
	}
	if a.namespace != "payments" || a.model.Namespace != "payments" {
		t.Fatalf("namespace must come from the new kubeconfig, got %q", a.namespace)
	}
	if a.model.Focus != ui.FocusTable || a.model.PathQuery != "" {
		t.Fatalf("the field must close: focus %v query %q", a.model.Focus, a.model.PathQuery)
	}
	if len(a.model.All) != 0 {
		t.Fatal("rows of the previous cluster must be dropped")
	}

	select {
	case res := <-a.pods:
		if res.namespace != "payments" {
			t.Fatalf("refresh asked for %q", res.namespace)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("switching kubeconfig must trigger a refresh")
	}
}

func TestKubeconfigApplyKeepsWorkingOnBadPath(t *testing.T) {
	a := testApp(t, "api-1")
	before := a.client

	press(a, tcell.KeyRune, 'c')
	press(a, tcell.KeyCtrlU, 0)
	typeText(a, "/definitely/not/a/kubeconfig.yaml")
	press(a, tcell.KeyEnter, 0)

	if a.model.Err == "" {
		t.Fatal("a bad path must be reported")
	}
	if strings.Contains(a.model.Err, "&") || strings.Contains(a.model.Err, "0x") {
		t.Errorf("the error must stay readable: %q", a.model.Err)
	}
	if a.client != before {
		t.Error("a failed switch must keep the working client")
	}
}

func TestResolveNamespacePrefersTheFlag(t *testing.T) {
	cases := []struct {
		flag, config, want string
	}{
		{"payments", "logging", "payments"},
		{"", "logging", "logging"},
		{"", "", "default"},
	}
	for _, c := range cases {
		if got := resolveNamespace(c.flag, c.config); got != c.want {
			t.Errorf("resolveNamespace(%q, %q) = %q, want %q", c.flag, c.config, got, c.want)
		}
	}
}

func TestFooterKeysWorkInBothCases(t *testing.T) {
	a := testApp(t, "api-1")

	press(a, tcell.KeyRune, 'N')
	if a.model.Focus != ui.FocusNamespace {
		t.Fatalf("N must open the namespace search, got %v", a.model.Focus)
	}
	press(a, tcell.KeyEscape, 0)

	press(a, tcell.KeyRune, 'C')
	if a.model.Focus != ui.FocusKubeconfig {
		t.Fatalf("C must open the kubeconfig field, got %v", a.model.Focus)
	}
	press(a, tcell.KeyEscape, 0)

	if quit := press(a, tcell.KeyRune, 'Q'); !quit {
		t.Fatal("Q must quit")
	}
}

func hotApp(t *testing.T) *app {
	t.Helper()
	a := testApp(t)
	a.model.All = []kube.Row{
		{Name: "hot-1", Worst: 96, CPUPct: 96, MemPct: 40},
		{Name: "hot-2", Worst: 91, CPUPct: 91, MemPct: 30},
		{Name: "warm-1", Worst: 80, CPUPct: 80, MemPct: 20},
		{Name: "calm-1", Worst: 12, CPUPct: 12, MemPct: 10},
	}
	ui.ApplyFilter(a.model)
	return a
}

func TestClickOnCountersFiltersTheTable(t *testing.T) {
	a := hotApp(t)
	g := ui.Geometry(*a.model, 170, 40)

	click(a, g.Critical.X+1, g.Critical.Y)
	if len(a.model.Rows) != 2 {
		t.Fatalf("critical filter must leave 2 rows, got %d", len(a.model.Rows))
	}
	for _, row := range a.model.Rows {
		if !strings.HasPrefix(row.Name, "hot-") {
			t.Fatalf("unexpected row %q", row.Name)
		}
	}

	click(a, g.Warning.X+1, g.Warning.Y)
	if len(a.model.Rows) != 1 || a.model.Rows[0].Name != "warm-1" {
		t.Fatalf("warning filter: %v", a.model.Rows)
	}

	click(a, g.Warning.X+1, g.Warning.Y)
	if len(a.model.Rows) != 4 {
		t.Fatalf("clicking the active counter must clear the filter, got %d rows", len(a.model.Rows))
	}
}

func TestEscapeClearsTheLevelFilter(t *testing.T) {
	a := hotApp(t)
	g := ui.Geometry(*a.model, 170, 40)

	click(a, g.Critical.X+1, g.Critical.Y)
	if quit := press(a, tcell.KeyEscape, 0); quit {
		t.Fatal("Esc must clear the filter before quitting")
	}
	if len(a.model.Rows) != 4 {
		t.Fatalf("filter must be gone, got %d rows", len(a.model.Rows))
	}
	if quit := press(a, tcell.KeyEscape, 0); !quit {
		t.Fatal("a second Esc quits")
	}
}

func TestLevelFilterSurvivesRefresh(t *testing.T) {
	a := hotApp(t)
	g := ui.Geometry(*a.model, 170, 40)
	click(a, g.Critical.X+1, g.Critical.Y)

	a.model.All = []kube.Row{
		{Name: "hot-3", Worst: 99, CPUPct: 99, MemPct: 50},
		{Name: "calm-2", Worst: 5, CPUPct: 5, MemPct: 5},
	}
	a.reselect()

	if len(a.model.Rows) != 1 || a.model.Rows[0].Name != "hot-3" {
		t.Fatalf("the filter must be re-applied to fresh rows, got %v", a.model.Rows)
	}
}

func TestClickOnDimensionsNarrowsWhatTheCountersMean(t *testing.T) {
	a := testApp(t)
	a.model.All = []kube.Row{
		{Name: "crashing", Severity: kube.Bad, CPUPct: 5, MemPct: 5, Worst: 5},
		{Name: "cpu-hot", Severity: kube.Good, CPUPct: 95, MemPct: 5, Worst: 95},
		{Name: "mem-warm", Severity: kube.Good, CPUPct: 5, MemPct: 80, Worst: 80},
		{Name: "calm", Severity: kube.Good, CPUPct: 5, MemPct: 5, Worst: 5},
	}
	ui.ApplyFilter(a.model)

	g := ui.Geometry(*a.model, 170, 40)
	click(a, g.DimStatus.X+1, g.DimStatus.Y)
	if a.model.Dimension != kube.DimStatus {
		t.Fatalf("dimension: %v", a.model.Dimension)
	}
	if len(a.model.Rows) != 4 {
		t.Fatalf("a dimension alone must not filter, got %d rows", len(a.model.Rows))
	}

	g = ui.Geometry(*a.model, 170, 40)
	click(a, g.Critical.X+1, g.Critical.Y)
	if len(a.model.Rows) != 1 || a.model.Rows[0].Name != "crashing" {
		t.Fatalf("critical by status: %v", a.model.Rows)
	}

	g = ui.Geometry(*a.model, 170, 40)
	click(a, g.DimMemory.X+1, g.DimMemory.Y)
	if a.model.Dimension != kube.DimMemory {
		t.Fatalf("dimension: %v", a.model.Dimension)
	}
	if len(a.model.Rows) != 0 {
		t.Fatalf("nothing is memory-critical here, got %v", a.model.Rows)
	}

	g = ui.Geometry(*a.model, 170, 40)
	click(a, g.Warning.X+1, g.Warning.Y)
	if len(a.model.Rows) != 1 || a.model.Rows[0].Name != "mem-warm" {
		t.Fatalf("warning by memory: %v", a.model.Rows)
	}
}

func TestSecondClickClearsTheDimension(t *testing.T) {
	a := hotApp(t)

	g := ui.Geometry(*a.model, 170, 40)
	click(a, g.DimCPU.X+1, g.DimCPU.Y)
	if a.model.Dimension != kube.DimCPU {
		t.Fatalf("dimension: %v", a.model.Dimension)
	}

	g = ui.Geometry(*a.model, 170, 40)
	click(a, g.DimCPU.X+1, g.DimCPU.Y)
	if a.model.Dimension != kube.DimAll {
		t.Fatalf("a second click must clear it, got %v", a.model.Dimension)
	}
	if len(a.model.Rows) != 4 {
		t.Fatalf("every pod must come back, got %d", len(a.model.Rows))
	}
}

func TestEscapeClearsDimensionAndLevel(t *testing.T) {
	a := hotApp(t)

	g := ui.Geometry(*a.model, 170, 40)
	click(a, g.DimCPU.X+1, g.DimCPU.Y)
	g = ui.Geometry(*a.model, 170, 40)
	click(a, g.Critical.X+1, g.Critical.Y)

	if quit := press(a, tcell.KeyEscape, 0); quit {
		t.Fatal("Esc must clear the selection first")
	}
	if a.model.Dimension != kube.DimAll || a.model.Level != kube.LevelAll {
		t.Fatalf("selection after Esc: dimension %v level %v", a.model.Dimension, a.model.Level)
	}
	if len(a.model.Rows) != 4 {
		t.Fatalf("every pod must come back, got %d", len(a.model.Rows))
	}
}
