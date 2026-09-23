package main

import (
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
			Interval:  2 * time.Second,
			All:       rows,
		},
	}
	ui.ApplyFilter(a.model)
	return a
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
	if len(a.model.All) != 0 || a.model.Cursor != 0 {
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
	if a.model.Cursor != 0 {
		t.Fatalf("cursor must clamp when the pod is gone, got %d", a.model.Cursor)
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

	click(a, 3, 1)
	if a.model.Focus != ui.FocusNamespace {
		t.Fatalf("click on the namespace box: focus %v", a.model.Focus)
	}

	click(a, 3, 5)
	if a.model.Focus != ui.FocusPods {
		t.Fatalf("click on the pod box: focus %v", a.model.Focus)
	}
}

func TestClickOnDropdownSwitchesNamespace(t *testing.T) {
	a := testApp(t, "api-1")
	a.model.Namespaces = []string{"default", "production", "staging"}

	click(a, 3, 1)
	click(a, 3, 5)

	if a.namespace != "staging" {
		t.Fatalf("clicking the third item must switch to staging, got %q", a.namespace)
	}
	if a.model.Focus != ui.FocusTable {
		t.Fatal("after choosing a namespace the table must get focus back")
	}
}

func TestClickOnRowMovesCursor(t *testing.T) {
	a := testApp(t, "api-1", "api-2", "web-1", "cache-1")

	click(a, 4, 9+2)
	if a.model.Cursor != 2 {
		t.Fatalf("cursor after the click: %d", a.model.Cursor)
	}
	if a.model.Focus != ui.FocusTable {
		t.Fatal("clicking a row must focus the table")
	}
}

func TestWheelScrollsTableAndDropdown(t *testing.T) {
	a := testApp(t, "a-1", "a-2", "a-3", "a-4", "a-5")
	a.model.Cursor = 4

	a.handleMouse(tcell.NewEventMouse(5, 12, tcell.WheelUp, tcell.ModNone))
	a.clamp()
	if a.model.Cursor != 1 {
		t.Fatalf("wheel up must move the cursor by three, got %d", a.model.Cursor)
	}

	press(a, tcell.KeyRune, '/')
	a.handleMouse(tcell.NewEventMouse(5, 6, tcell.WheelDown, tcell.ModNone))
	if a.model.Choice != 1 {
		t.Fatalf("wheel inside the dropdown must move the choice, got %d", a.model.Choice)
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
