package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/miraxmetov/ktop/internal/kube"
	"github.com/miraxmetov/ktop/internal/paths"
	"github.com/miraxmetov/ktop/internal/ui"
)

var version = "dev"

type options struct {
	namespace string
	context   string
	interval  time.Duration
}

func parseFlags() (options, error) {
	var (
		opts        options
		nsFlag      string
		ctxFlag     string
		intervalArg float64
		showVersion bool
	)

	fs := flag.NewFlagSet("ktop", flag.ContinueOnError)
	fs.StringVar(&nsFlag, "n", "", "namespace to watch")
	fs.StringVar(&nsFlag, "namespace", "", "namespace to watch")
	fs.StringVar(&ctxFlag, "c", "", "kube context to use")
	fs.StringVar(&ctxFlag, "context", "", "kube context to use")
	fs.Float64Var(&intervalArg, "i", 1, "refresh interval in seconds")
	fs.Float64Var(&intervalArg, "interval", 1, "refresh interval in seconds")
	fs.BoolVar(&showVersion, "V", false, "print version and exit")
	fs.BoolVar(&showVersion, "version", false, "print version and exit")
	fs.Usage = func() { fmt.Fprint(fs.Output(), usage) }
	if err := fs.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		return opts, err
	}
	if showVersion {
		fmt.Println("ktop " + version)
		os.Exit(0)
	}
	if intervalArg <= 0 {
		return opts, fmt.Errorf("interval must be positive")
	}

	opts.namespace = nsFlag
	if fs.NArg() > 0 {
		opts.namespace = fs.Arg(0)
	}
	opts.context = ctxFlag
	opts.interval = time.Duration(intervalArg * float64(time.Second))
	return opts, nil
}

const usage = `ktop - live pod resource usage as a percentage of limits

Usage:
  ktop [namespace] [flags]

Flags:
  -n, --namespace NAMESPACE  namespace to watch; without it ktop takes the one from the
                             current kubeconfig context
                             ktop -n production

  -c, --context CONTEXT      kube context to use, instead of the current one
                             ktop -c staging-eu

  -i, --interval SECONDS     how often to refresh, in seconds (default 1)
                             ktop -i 5

  -h, --help                 show this help and exit
                             ktop --help

  -V, --version              print the version and exit
                             ktop --version

The cluster comes from $KUBECONFIG, or ~/.kube/config; press c inside ktop to switch it.

Examples:
  ktop                       watch the namespace of the current context
  ktop production            watch a namespace by name
  ktop production -i 5       the same, refreshed every five seconds
  KUBECONFIG=~/.kube/prod.yaml ktop
`

const noSelection = -1

type podResult struct {
	namespace string
	result    kube.Result
	err       error
}

type namespaceResult struct {
	names []string
	err   error
}

type app struct {
	screen     tcell.Screen
	client     *kube.Client
	model      *ui.Model
	namespace  string
	context    string
	interval   time.Duration
	pods       chan podResult
	namespaces chan namespaceResult
}

func main() {
	opts, err := parseFlags()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ktop: "+err.Error())
		os.Exit(2)
	}

	client, defaultNamespace, err := kube.New(opts.context)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ktop: "+err.Error())
		os.Exit(1)
	}
	namespace := resolveNamespace(opts.namespace, defaultNamespace)

	screen, err := tcell.NewScreen()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ktop: "+err.Error())
		os.Exit(1)
	}
	if err := screen.Init(); err != nil {
		fmt.Fprintln(os.Stderr, "ktop: "+err.Error())
		os.Exit(1)
	}
	screen.EnableMouse(tcell.MouseButtonEvents)

	a := &app{
		screen:     screen,
		client:     client,
		namespace:  namespace,
		context:    opts.context,
		interval:   opts.interval,
		pods:       make(chan podResult, 1),
		namespaces: make(chan namespaceResult, 1),
		model: &ui.Model{
			Namespace:  namespace,
			Context:    client.Context,
			Kubeconfig: client.Kubeconfig,
			Started:    time.Now(),
			Cursor:     noSelection,
		},
	}

	defer func() {
		screen.Fini()
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "ktop: internal error: %v\n", r)
			os.Exit(1)
		}
	}()

	a.run()
}

func resolveNamespace(flag, fromConfig string) string {
	for _, candidate := range []string{flag, fromConfig} {
		if candidate != "" {
			return candidate
		}
	}
	return "default"
}

func (a *app) fetchPods() {
	namespace := a.namespace
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		result, err := a.client.Rows(ctx, namespace)
		a.pods <- podResult{namespace: namespace, result: result, err: err}
	}()
}

func (a *app) fetchNamespaces() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		names, err := a.client.Namespaces(ctx)
		a.namespaces <- namespaceResult{names: names, err: err}
	}()
}

func (a *app) draw() {
	a.model.Now = time.Now()
	ui.Draw(a.screen, *a.model)
}

func (a *app) run() {
	events := make(chan tcell.Event)
	stop := make(chan struct{})
	go a.screen.ChannelEvents(events, stop)

	a.fetchPods()
	a.fetchNamespaces()
	a.draw()

	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()
	clock := time.NewTicker(time.Second)
	defer clock.Stop()

	for {
		select {
		case <-ticker.C:
			a.fetchPods()
		case <-clock.C:
			a.draw()
		case res := <-a.pods:
			if res.namespace != a.namespace {
				continue
			}
			if res.err != nil {
				a.model.Err = res.err.Error()
				a.model.All = nil
			} else {
				a.model.Err = ""
				a.model.Note = res.result.Note
				a.model.All = res.result.Rows
			}
			a.reselect()
			a.draw()
		case res := <-a.namespaces:
			if res.err != nil {
				a.model.Namespaces = nil
				a.model.NamespaceNote = kube.ExplainNamespaces(res.err)
			} else {
				a.model.Namespaces = res.names
				a.model.NamespaceNote = ""
			}
			a.draw()
		case ev, ok := <-events:
			if !ok {
				return
			}
			switch e := ev.(type) {
			case *tcell.EventResize:
				a.screen.Sync()
				a.clamp()
				a.draw()
			case *tcell.EventKey:
				if a.handleKey(e) {
					close(stop)
					return
				}
				a.clamp()
				a.draw()
			case *tcell.EventMouse:
				a.handleMouse(e)
				a.clamp()
				a.draw()
			}
		}
	}
}

func (a *app) reselect() {
	previous := a.model.SelectedName()
	ui.ApplyFilter(a.model)
	if previous != "" {
		for i, row := range a.model.Rows {
			if row.Name == previous {
				a.model.Cursor = i
				a.clamp()
				return
			}
		}
		a.model.Cursor = noSelection
	}
	a.clamp()
}

func (a *app) switchNamespace(name string) {
	if name == "" || name == a.namespace {
		a.model.Focus = ui.FocusTable
		a.model.NamespaceQuery = ""
		a.model.Choice = 0
		return
	}
	a.namespace = name
	a.model.Namespace = name
	a.model.NamespaceQuery = ""
	a.model.Focus = ui.FocusTable
	a.model.Choice = 0
	a.model.All = nil
	a.model.Rows = nil
	a.model.Err = ""
	a.model.Note = ""
	a.model.Cursor = noSelection
	a.model.Offset = 0
	a.fetchPods()
}

func (a *app) toggleLevel(level kube.Level) {
	a.model.Focus = ui.FocusTable
	if a.model.Level == level {
		a.model.Level = kube.LevelAll
	} else {
		a.model.Level = level
	}
	a.reselect()
}

func (a *app) toggleDimension(dimension kube.Dimension) {
	a.model.Focus = ui.FocusTable
	if a.model.Dimension == dimension {
		a.model.Dimension = kube.DimAll
	} else {
		a.model.Dimension = dimension
	}
	a.reselect()
}

func (a *app) focusPods() {
	a.model.Focus = ui.FocusPods
	a.model.Choice = 0
}

func (a *app) focusNamespace() {
	a.model.Focus = ui.FocusNamespace
	a.model.Choice = 0
	a.fetchNamespaces()
}

func (a *app) focusKubeconfig() {
	a.model.Focus = ui.FocusKubeconfig
	a.model.Choice = 0
	if a.model.PathQuery == "" {
		a.model.PathQuery = directoryOf(a.model.Kubeconfig)
	}
	a.refreshPaths()
}

func directoryOf(path string) string {
	if path == "" {
		return ""
	}
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[:i+1]
	}
	return ""
}

func (a *app) refreshPaths() {
	a.model.PathOptions = paths.Complete(a.model.PathQuery)
	a.model.ClampChoice()
}

func (a *app) applyKubeconfig(path string) {
	path = paths.Expand(strings.TrimSpace(path))
	if path == "" {
		a.model.Focus = ui.FocusTable
		return
	}

	client, namespace, err := kube.NewWithPath(path, a.context)
	if err != nil {
		a.model.Err = err.Error()
		return
	}

	a.client = client
	a.namespace = namespace
	a.model.Namespace = namespace
	a.model.Context = client.Context
	a.model.Kubeconfig = client.Kubeconfig
	a.model.PathQuery = ""
	a.model.PathOptions = nil
	a.model.Namespaces = nil
	a.model.NamespaceNote = ""
	a.model.All = nil
	a.model.Rows = nil
	a.model.Err = ""
	a.model.Note = ""
	a.model.Cursor = noSelection
	a.model.Offset = 0
	a.model.Choice = 0
	a.model.Focus = ui.FocusTable
	a.fetchPods()
	a.fetchNamespaces()
}

func (a *app) applyChoice() {
	options := a.model.Options()
	choice := a.model.Choice

	if a.model.Focus == ui.FocusKubeconfig {
		selected := a.model.PathQuery
		if choice >= 0 && choice < len(options) {
			selected = options[choice]
		}
		if paths.IsDir(selected) {
			a.model.PathQuery = selected
			a.model.Choice = 0
			a.refreshPaths()
			return
		}
		a.applyKubeconfig(selected)
		return
	}

	if a.model.Focus == ui.FocusNamespace {
		name := a.model.NamespaceQuery
		for _, option := range options {
			if option == a.model.NamespaceQuery {
				name = option
			}
		}
		if name == a.model.NamespaceQuery && choice >= 0 && choice < len(options) {
			name = options[choice]
		}
		a.switchNamespace(name)
		return
	}

	if choice >= 0 && choice < len(options) {
		for i, row := range a.model.Rows {
			if row.Name == options[choice] {
				a.model.Cursor = i
				break
			}
		}
	}
	a.model.Focus = ui.FocusTable
}

func (a *app) switchField(pods bool) {
	if pods {
		a.focusNamespace()
		return
	}
	a.focusPods()
}

func (a *app) complete(pods bool) bool {
	options := a.model.Options()
	if a.model.Choice < 0 || a.model.Choice >= len(options) {
		return false
	}
	chosen := options[a.model.Choice]

	switch a.model.Focus {
	case ui.FocusKubeconfig:
		a.model.PathQuery = chosen
		a.model.Choice = 0
		a.refreshPaths()
	case ui.FocusPods:
		a.model.PodQuery = chosen
		a.reselect()
		a.model.Choice = 0
	default:
		a.model.NamespaceQuery = chosen
		a.model.Choice = 0
	}
	return true
}

func (a *app) handleMouse(e *tcell.EventMouse) {
	x, y := e.Position()
	width, height := a.screen.Size()

	switch {
	case e.Buttons()&tcell.Button1 != 0:
		target, index := ui.Hit(*a.model, width, height, x, y)
		switch target {
		case ui.HitNamespaceInput:
			a.focusNamespace()
		case ui.HitPodInput:
			a.focusPods()
		case ui.HitKubeconfig:
			a.focusKubeconfig()
		case ui.HitCritical:
			a.toggleLevel(kube.LevelCritical)
		case ui.HitWarning:
			a.toggleLevel(kube.LevelWarning)
		case ui.HitDimStatus:
			a.toggleDimension(kube.DimStatus)
		case ui.HitDimCPU:
			a.toggleDimension(kube.DimCPU)
		case ui.HitDimMemory:
			a.toggleDimension(kube.DimMemory)
		case ui.HitDropdown:
			a.model.Choice = index
			a.applyChoice()
		case ui.HitRow:
			a.model.Focus = ui.FocusTable
			a.model.Cursor = index
		case ui.HitNone:
			a.model.Focus = ui.FocusTable
		}
	case e.Buttons()&tcell.WheelUp != 0:
		if a.model.Focus == ui.FocusTable {
			a.scroll(-1)
		} else {
			a.model.Choice--
		}
	case e.Buttons()&tcell.WheelDown != 0:
		if a.model.Focus == ui.FocusTable {
			a.scroll(1)
		} else {
			a.model.Choice++
		}
	}
	a.model.ClampChoice()
}

func (a *app) handleKey(e *tcell.EventKey) bool {
	if a.model.Focus != ui.FocusTable {
		return a.handleInputKey(e)
	}

	_, height := a.screen.Size()
	page := ui.Visible(height)

	switch e.Key() {
	case tcell.KeyCtrlC:
		return true
	case tcell.KeyEscape:
		if a.model.Cursor >= 0 {
			a.model.Cursor = noSelection
			return false
		}
		if a.model.PodQuery != "" {
			a.model.PodQuery = ""
			a.reselect()
			return false
		}
		if a.model.Level != kube.LevelAll || a.model.Dimension != kube.DimAll {
			a.model.Level = kube.LevelAll
			a.model.Dimension = kube.DimAll
			a.reselect()
			return false
		}
		return true
	case tcell.KeyUp:
		a.moveCursor(-1)
	case tcell.KeyDown:
		a.moveCursor(1)
	case tcell.KeyPgUp:
		a.page(-page)
	case tcell.KeyPgDn:
		a.page(page)
	case tcell.KeyHome:
		a.jump(0)
	case tcell.KeyEnd:
		a.jump(len(a.model.Rows) - 1)
	case tcell.KeyTab:
		a.focusPods()
	case tcell.KeyBacktab:
		a.focusNamespace()
	case tcell.KeyRune:
		switch e.Rune() {
		case 'q', 'Q':
			return true
		case 'k':
			a.moveCursor(-1)
		case 'j':
			a.moveCursor(1)
		case 'g':
			a.jump(0)
		case 'G':
			a.jump(len(a.model.Rows) - 1)
		case 'r', 'R':
			a.fetchPods()
			a.fetchNamespaces()
		case '/':
			a.focusPods()
		case 'n', 'N':
			a.focusNamespace()
		case 'c', 'C':
			a.focusKubeconfig()
		}
	}
	return false
}

func (a *app) handleInputKey(e *tcell.EventKey) bool {
	pods := a.model.Focus == ui.FocusPods
	config := a.model.Focus == ui.FocusKubeconfig

	switch e.Key() {
	case tcell.KeyCtrlC:
		return true
	case tcell.KeyEscape:
		if config {
			a.model.PathQuery = ""
			a.model.PathOptions = nil
		} else if !pods {
			a.model.NamespaceQuery = ""
		}
		a.model.Focus = ui.FocusTable
	case tcell.KeyEnter:
		a.applyChoice()
	case tcell.KeyTab:
		if !a.complete(pods) {
			a.switchField(pods)
		}
	case tcell.KeyBacktab:
		a.switchField(pods)
	case tcell.KeyBackspace, tcell.KeyBackspace2, tcell.KeyDelete:
		switch {
		case config:
			a.model.PathQuery = trimLast(a.model.PathQuery)
			a.refreshPaths()
		case pods:
			a.model.PodQuery = trimLast(a.model.PodQuery)
			a.reselect()
		default:
			a.model.NamespaceQuery = trimLast(a.model.NamespaceQuery)
		}
		a.model.Choice = 0
	case tcell.KeyCtrlU:
		switch {
		case config:
			a.model.PathQuery = ""
			a.refreshPaths()
		case pods:
			a.model.PodQuery = ""
			a.reselect()
		default:
			a.model.NamespaceQuery = ""
		}
		a.model.Choice = 0
	case tcell.KeyUp:
		a.model.Choice--
	case tcell.KeyDown:
		a.model.Choice++
	case tcell.KeyPgUp:
		a.model.Choice -= 5
	case tcell.KeyPgDn:
		a.model.Choice += 5
	case tcell.KeyRune:
		switch {
		case config:
			a.model.PathQuery += string(e.Rune())
			a.refreshPaths()
		case pods:
			a.model.PodQuery += string(e.Rune())
			a.reselect()
		default:
			a.model.NamespaceQuery += string(e.Rune())
		}
		a.model.Choice = 0
	}
	a.model.ClampChoice()
	return false
}

func trimLast(text string) string {
	runes := []rune(text)
	if len(runes) == 0 {
		return text
	}
	return string(runes[:len(runes)-1])
}

func (a *app) moveCursor(delta int) {
	if a.model.Cursor < 0 {
		a.model.Cursor = a.model.Offset
		return
	}
	a.model.Cursor += delta
}

func (a *app) page(delta int) {
	if a.model.Cursor >= 0 {
		a.model.Cursor += delta
		return
	}
	a.scroll(delta)
}

func (a *app) jump(index int) {
	if a.model.Cursor >= 0 {
		a.model.Cursor = index
		return
	}
	a.model.Offset = index
}

func (a *app) scroll(delta int) {
	a.model.Offset += delta
}

func (a *app) clamp() {
	_, height := a.screen.Size()
	room := ui.Visible(height)
	model := a.model

	if model.Cursor > len(model.Rows)-1 {
		model.Cursor = len(model.Rows) - 1
	}
	if model.Cursor < noSelection {
		model.Cursor = noSelection
	}
	if model.Cursor >= 0 {
		if model.Cursor < model.Offset {
			model.Offset = model.Cursor
		}
		if model.Cursor >= model.Offset+room {
			model.Offset = model.Cursor - room + 1
		}
	}
	if model.Offset > len(model.Rows)-room {
		model.Offset = len(model.Rows) - room
	}
	if model.Offset < 0 {
		model.Offset = 0
	}
}
