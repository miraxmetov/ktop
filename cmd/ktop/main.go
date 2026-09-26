package main

import (
	"bufio"
	"context"
	"errors"
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

func fail(err error) {
	fmt.Fprintln(os.Stderr, "ktop: "+err.Error())

	var config *kube.ConfigError
	if errors.As(err, &config) && config.Hint != "" {
		fmt.Fprintln(os.Stderr, "hint: "+config.Hint)
	}
	os.Exit(1)
}

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
	inspected  chan inspectResult
	logs       chan logLine
	stopLogs   context.CancelFunc
}

type inspectResult struct {
	name      string
	container string
	pods      []string
	readable  []string
	textual   []string
	yaml      []string
	err       error
}

type logLine struct {
	pod  string
	line string
	err  error
}

func main() {
	opts, err := parseFlags()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ktop: "+err.Error())
		os.Exit(2)
	}

	client, defaultNamespace, err := kube.New(opts.context)
	if err != nil {
		fail(err)
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
		inspected:  make(chan inspectResult, 1),
		logs:       make(chan logLine, 256),
		model: &ui.Model{
			Namespace:  namespace,
			Context:    client.Context,
			Kubeconfig: client.Kubeconfig,
			Started:    time.Now(),
			NameOrder:  kube.OrderAsc,
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
	kind := a.model.Kind
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		result, err := a.client.Rows(ctx, namespace, kind)
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
	marquee := time.NewTicker(120 * time.Millisecond)
	defer marquee.Stop()

	for {
		select {
		case <-ticker.C:
			a.fetchPods()
		case <-clock.C:
			a.draw()
		case <-marquee.C:
			if a.model.Screen != ui.ScreenTable {
				continue
			}
			width, height := a.screen.Size()
			if !ui.NeedsMarquee(*a.model, width, height) {
				continue
			}
			a.model.Tick++
			a.draw()
		case res := <-a.pods:
			if res.namespace != a.namespace {
				continue
			}
			a.model.Loaded = true
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
		case res := <-a.inspected:
			if res.name != a.model.Inspect.Pod {
				continue
			}
			a.model.Inspect.Loading = false
			if res.err != nil {
				a.model.Inspect.Err = res.err.Error()
			} else {
				a.model.Inspect.Err = ""
				a.model.Inspect.Default = res.readable
				a.model.Inspect.Textual = res.textual
				a.model.Inspect.Yaml = res.yaml
				a.model.Inspect.Container = res.container
				a.model.Inspect.LogPods = res.pods
				if a.model.Inspect.LogPod == "" && len(res.pods) > 0 {
					a.model.Inspect.LogPod = res.pods[0]
				}
				a.followLogs(a.model.Inspect.LogPod, res.container)
			}
			a.draw()
		case line := <-a.logs:
			if line.pod != a.model.Inspect.LogPod || a.model.Screen != ui.ScreenInspect {
				continue
			}
			if line.err != nil {
				a.model.Inspect.LogErr = line.err.Error()
			} else {
				stamp, text := kube.SplitLogLine(line.line)
				a.appendLog(ui.LogLine{Time: stamp, Text: text})
			}
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
	ui.ApplyFilter(a.model)
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
	a.model.Expanded = ""
	a.model.Confirm = ui.ActionNone
	a.model.Offset = 0
	a.model.Loaded = false
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

func (a *app) focusKind() {
	a.model.Focus = ui.FocusKind
	a.model.Choice = 0
	for i, kind := range kube.Kinds() {
		if kind == a.model.Kind {
			a.model.Choice = i
		}
	}
}

func (a *app) switchKind(name string) {
	kind, ok := kube.KindByName(name)
	a.model.Focus = ui.FocusTable
	if !ok || kind == a.model.Kind {
		return
	}

	a.model.Kind = kind
	a.model.All = nil
	a.model.Rows = nil
	a.model.Expanded = ""
	a.model.Confirm = ui.ActionNone
	a.model.Offset = 0
	a.model.Loaded = false
	a.model.SortKey, a.model.SortOrder = "", kube.OrderNone
	a.fetchPods()
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
		var config *kube.ConfigError
		if errors.As(err, &config) && config.Hint != "" {
			a.model.Err += ", " + config.Hint
		}
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
	a.model.Expanded = ""
	a.model.Confirm = ui.ActionNone
	a.model.Offset = 0
	a.model.Loaded = false
	a.model.Choice = 0
	a.model.Focus = ui.FocusTable
	a.fetchPods()
	a.fetchNamespaces()
}

func (a *app) applyChoice() {
	options := a.model.Options()
	choice := a.model.Choice

	if a.model.Focus == ui.FocusKind {
		if choice >= 0 && choice < len(options) {
			a.switchKind(options[choice])
			return
		}
		a.model.Focus = ui.FocusTable
		return
	}

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
				a.model.Expanded = row.Name
				a.scrollTo(i)
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

	if a.model.Screen == ui.ScreenInspect {
		switch {
		case e.Buttons()&tcell.Button1 != 0:
			target, index := ui.HitInspect(*a.model, width, height, x, y)
			switch target {
			case ui.HitFormatDefault:
				a.setFormat(ui.FormatDefault)
			case ui.HitFormatTextual:
				a.setFormat(ui.FormatTextual)
			case ui.HitFormatYAML:
				a.setFormat(ui.FormatYAML)
			case ui.HitLogPod:
				a.model.Inspect.PodPicker = len(a.model.Inspect.LogPods) > 1 && !a.model.Inspect.PodPicker
			case ui.HitLogPodItem:
				a.choosePod(index)
			case ui.HitLogSearch:
				a.model.Inspect.LogSearch = true
			case ui.HitConfigPane, ui.HitLogPane:
				a.model.Inspect.LogSearch = false
				a.model.Inspect.PodPicker = false
			}
		case e.Buttons()&tcell.WheelUp != 0:
			a.wheelInspect(x, y, -1)
		case e.Buttons()&tcell.WheelDown != 0:
			a.wheelInspect(x, y, 1)
		}
		return
	}

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
		case ui.HitKindBox:
			a.focusKind()
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
		case ui.HitPodName:
			a.toggleActions(index)
		case ui.HitColumn:
			a.sortByColumn(index)
		case ui.HitActionInspect:
			a.openInspect(index)
		case ui.HitActionRestart:
			a.model.Confirm = ui.ActionRestart
		case ui.HitActionTerminate:
			a.model.Confirm = ui.ActionTerminate
		case ui.HitConfirmYes:
			a.runAction()
		case ui.HitConfirmCancel:
			a.model.Confirm = ui.ActionNone
		case ui.HitRow:
			a.model.Focus = ui.FocusTable
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
	if a.model.Screen == ui.ScreenInspect {
		return a.handleInspectKey(e)
	}
	if a.model.Focus != ui.FocusTable {
		return a.handleInputKey(e)
	}

	_, height := a.screen.Size()
	page := ui.Visible(height)

	switch e.Key() {
	case tcell.KeyCtrlC:
		return true
	case tcell.KeyEscape:
		if a.model.Confirm != ui.ActionNone {
			a.model.Confirm = ui.ActionNone
			return false
		}
		if a.model.Expanded != "" {
			a.model.Expanded = ""
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
		a.scroll(-1)
	case tcell.KeyDown:
		a.scroll(1)
	case tcell.KeyPgUp:
		a.scroll(-page)
	case tcell.KeyPgDn:
		a.scroll(page)
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
			a.scroll(-1)
		case 'j':
			a.scroll(1)
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
		case 'm', 'M':
			a.focusKind()
		}
	}
	return false
}

func (a *app) handleInspectKey(e *tcell.EventKey) bool {
	width, height := a.screen.Size()

	if a.model.Inspect.PodPicker {
		switch e.Key() {
		case tcell.KeyCtrlC:
			return true
		case tcell.KeyEscape:
			a.model.Inspect.PodPicker = false
		case tcell.KeyEnter:
			a.choosePod(a.model.Inspect.PodChoice)
		case tcell.KeyUp:
			a.model.Inspect.PodChoice--
		case tcell.KeyDown:
			a.model.Inspect.PodChoice++
		}
		if a.model.Inspect.PodChoice < 0 {
			a.model.Inspect.PodChoice = 0
		}
		if a.model.Inspect.PodChoice >= len(a.model.Inspect.LogPods) {
			a.model.Inspect.PodChoice = len(a.model.Inspect.LogPods) - 1
		}
		return false
	}

	if a.model.Inspect.LogSearch {
		switch e.Key() {
		case tcell.KeyCtrlC:
			return true
		case tcell.KeyEscape, tcell.KeyEnter:
			a.model.Inspect.LogSearch = false
		case tcell.KeyBackspace, tcell.KeyBackspace2, tcell.KeyDelete:
			a.model.Inspect.LogQuery = trimLast(a.model.Inspect.LogQuery)
			a.model.Inspect.LogOffset = 0
		case tcell.KeyCtrlU:
			a.model.Inspect.LogQuery = ""
			a.model.Inspect.LogOffset = 0
		case tcell.KeyRune:
			a.model.Inspect.LogQuery += string(e.Rune())
			a.model.Inspect.LogOffset = 0
		}
		return false
	}

	config := e.Modifiers()&tcell.ModShift != 0

	switch e.Key() {
	case tcell.KeyCtrlC:
		return true
	case tcell.KeyEscape:
		a.closeInspect()
	case tcell.KeyUp:
		a.scrollPane(config, -1)
	case tcell.KeyDown:
		a.scrollPane(config, 1)
	case tcell.KeyPgUp:
		a.scrollPane(config, -inspectPage(config, width, height))
	case tcell.KeyPgDn:
		a.scrollPane(config, inspectPage(config, width, height))
	case tcell.KeyHome:
		a.jumpPane(config, true)
	case tcell.KeyEnd:
		a.jumpPane(config, false)
	case tcell.KeyTab, tcell.KeyBacktab:
		a.toggleFormat()
	case tcell.KeyRune:
		switch e.Rune() {
		case 'q', 'Q':
			a.closeInspect()
		case 'k':
			a.scrollPane(config, -1)
		case 'j':
			a.scrollPane(config, 1)
		case 'g':
			a.jumpPane(config, true)
		case 'G':
			a.jumpPane(config, false)
		case 'y', 'Y':
			a.setFormat(ui.FormatYAML)
		case 'd', 'D':
			a.setFormat(ui.FormatDefault)
		case 't', 'T':
			a.setFormat(ui.FormatTextual)
		case '/':
			a.model.Inspect.LogSearch = true
		case 'p', 'P':
			a.model.Inspect.PodPicker = len(a.model.Inspect.LogPods) > 1
		}
	}
	return false
}

func (a *app) toggleFormat() {
	switch a.model.Inspect.Format {
	case ui.FormatDefault:
		a.setFormat(ui.FormatTextual)
	case ui.FormatTextual:
		a.setFormat(ui.FormatYAML)
	default:
		a.setFormat(ui.FormatDefault)
	}
}

func (a *app) setFormat(format ui.Format) {
	if a.model.Inspect.Format == format {
		return
	}
	a.model.Inspect.Format = format
	a.model.Inspect.Offset = 0
}

func (a *app) handleInputKey(e *tcell.EventKey) bool {
	pods := a.model.Focus == ui.FocusPods
	config := a.model.Focus == ui.FocusKubeconfig
	kinds := a.model.Focus == ui.FocusKind

	if kinds {
		switch e.Key() {
		case tcell.KeyCtrlC:
			return true
		case tcell.KeyEscape:
			a.model.Focus = ui.FocusTable
		case tcell.KeyEnter, tcell.KeyTab:
			a.applyChoice()
		case tcell.KeyUp:
			a.model.Choice--
		case tcell.KeyDown:
			a.model.Choice++
		}
		a.model.ClampChoice()
		return false
	}

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

func (a *app) scroll(delta int) {
	if a.model.Screen == ui.ScreenInspect {
		a.model.Inspect.Offset += delta
		a.clampInspect()
		return
	}
	a.model.Offset += delta
}

func inspectPage(config bool, width, height int) int {
	if config {
		return ui.InspectRoom(width, height)
	}
	return ui.LogRoom(width, height)
}

func (a *app) scrollPane(config bool, delta int) {
	if config {
		a.scroll(delta)
		return
	}
	a.scrollLogs(-delta)
}

func (a *app) scrollLogs(delta int) {
	a.model.Inspect.LogOffset += delta
	a.clampLogs()
}

func (a *app) jumpPane(config, top bool) {
	width, height := a.screen.Size()

	if config {
		if top {
			a.jump(0)
			return
		}
		a.jump(ui.MaxInspectOffset(*a.model, width, height))
		return
	}
	a.model.Inspect.LogOffset = 0
	if top {
		a.model.Inspect.LogOffset = ui.MaxLogOffset(*a.model, width, height)
	}
}

func (a *app) wheelInspect(x, y, delta int) {
	width, height := a.screen.Size()

	target, _ := ui.HitInspect(*a.model, width, height, x, y)
	a.scrollPane(target == ui.HitConfigPane, delta)
}

func (a *app) clampLogs() {
	width, height := a.screen.Size()

	if limit := ui.MaxLogOffset(*a.model, width, height); a.model.Inspect.LogOffset > limit {
		a.model.Inspect.LogOffset = limit
	}
	if a.model.Inspect.LogOffset < 0 {
		a.model.Inspect.LogOffset = 0
	}
}

func (a *app) jump(index int) {
	if a.model.Screen == ui.ScreenInspect {
		a.model.Inspect.Offset = index
		a.clampInspect()
		return
	}
	a.model.Offset = index
}

func (a *app) clampInspect() {
	width, height := a.screen.Size()

	if limit := ui.MaxInspectOffset(*a.model, width, height); a.model.Inspect.Offset > limit {
		a.model.Inspect.Offset = limit
	}
	if a.model.Inspect.Offset < 0 {
		a.model.Inspect.Offset = 0
	}
}

func (a *app) sortByColumn(index int) {
	width, height := a.screen.Size()
	key := ui.ColumnKeyAt(*a.model, width, height, index)
	if key == "" {
		return
	}

	if key == "name" {
		switch {
		case a.model.SortKey != "" && a.model.SortOrder != kube.OrderNone:
			a.model.SortKey, a.model.SortOrder = "", kube.OrderNone
		case a.model.NameOrder == kube.OrderDesc:
			a.model.NameOrder = kube.OrderAsc
		default:
			a.model.NameOrder = kube.OrderDesc
		}
		a.model.Offset = 0
		a.reselect()
		return
	}

	if a.model.SortKey != key {
		a.model.SortKey, a.model.SortOrder = key, kube.OrderDesc
	} else {
		a.model.SortOrder = ui.NextOrder(a.model.SortOrder)
		if a.model.SortOrder == kube.OrderNone {
			a.model.SortKey = ""
		}
	}
	a.model.Offset = 0
	a.reselect()
}

func (a *app) toggleActions(index int) {
	if index < 0 || index >= len(a.model.Rows) {
		return
	}
	name := a.model.Rows[index].Name

	a.model.Focus = ui.FocusTable
	a.model.Confirm = ui.ActionNone
	if a.model.Expanded == name {
		a.model.Expanded = ""
		return
	}
	a.model.Expanded = name
	a.model.Tick = 0
}

func (a *app) openInspect(index int) {
	if index < 0 || index >= len(a.model.Rows) {
		return
	}
	name := a.model.Rows[index].Name

	a.stopStream()
	a.model.Screen = ui.ScreenInspect

	logPod := name
	pods := []string{name}
	if a.model.Kind != kube.KindPod {
		pods = append([]string(nil), a.model.Rows[index].Pods...)
		logPod = ""
		if len(pods) > 0 {
			logPod = pods[0]
		}
	}

	a.model.Inspect = ui.Inspection{
		Pod:     name,
		LogPod:  logPod,
		LogPods: pods,
		Loading: true,
		Format:  a.model.Inspect.Format,
	}
	a.fetchPod(name)
}

func (a *app) choosePod(index int) {
	if index < 0 || index >= len(a.model.Inspect.LogPods) {
		return
	}
	name := a.model.Inspect.LogPods[index]

	a.model.Inspect.PodPicker = false
	a.model.Inspect.PodChoice = index
	if name == a.model.Inspect.LogPod {
		return
	}

	a.model.Inspect.LogPod = name
	a.model.Inspect.Logs = nil
	a.model.Inspect.LogErr = ""
	a.model.Inspect.LogOffset = 0
	a.followLogs(name, "")
}

func (a *app) appendLog(line ui.LogLine) {
	const keep = 2000
	if a.model.Inspect.LogOffset > 0 && a.model.Inspect.Matches(line) {
		width, height := a.screen.Size()
		a.model.Inspect.LogOffset += ui.LogLineRows(*a.model, width, height, line)
	}
	a.model.Inspect.Logs = append(a.model.Inspect.Logs, line)
	if len(a.model.Inspect.Logs) > keep {
		a.model.Inspect.Logs = a.model.Inspect.Logs[len(a.model.Inspect.Logs)-keep:]
	}
}

func (a *app) stopStream() {
	if a.stopLogs != nil {
		a.stopLogs()
		a.stopLogs = nil
	}
}

func (a *app) followLogs(name, container string) {
	a.stopStream()

	ctx, cancel := context.WithCancel(context.Background())
	a.stopLogs = cancel
	namespace := a.namespace

	go func() {
		stream, err := a.client.FollowLogs(ctx, namespace, name, container, 200)
		if err != nil {
			select {
			case a.logs <- logLine{pod: name, err: err}:
			case <-ctx.Done():
			}
			return
		}
		defer stream.Close()

		reader := bufio.NewReader(stream)
		for {
			text, err := reader.ReadString('\n')
			if text != "" {
				select {
				case a.logs <- logLine{pod: name, line: strings.TrimRight(text, "\r\n")}:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
}

func (a *app) closeInspect() {
	a.stopStream()
	a.model.Screen = ui.ScreenTable
	a.model.Inspect.Loading = false
	a.model.Inspect.LogSearch = false
}

func (a *app) fetchPod(name string) {
	namespace := a.namespace
	kind := a.model.Kind
	pods := append([]string(nil), a.model.Inspect.LogPods...)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		now := time.Now()

		if kind != kube.KindPod {
			detail, err := a.client.Workload(ctx, namespace, kind, name)
			if err != nil {
				a.inspected <- inspectResult{name: name, err: err}
				return
			}
			body, err := detail.YAML()
			if err != nil {
				a.inspected <- inspectResult{name: name, err: err}
				return
			}
			a.inspected <- inspectResult{
				name:     name,
				pods:     pods,
				readable: detail.Describe(pods, now),
				textual:  detail.Textual(pods, now),
				yaml:     body,
			}
			return
		}

		pod, err := a.client.Pod(ctx, namespace, name)
		if err != nil {
			a.inspected <- inspectResult{name: name, err: err}
			return
		}
		body, err := kube.YAML(pod)
		if err != nil {
			a.inspected <- inspectResult{name: name, err: err}
			return
		}
		a.inspected <- inspectResult{
			name:      name,
			container: kube.FirstContainer(pod),
			pods:      pods,
			readable:  kube.Describe(pod, now),
			textual:   kube.Textual(pod, now),
			yaml:      body,
		}
	}()
}

func (a *app) runAction() {
	action := a.model.Confirm
	name := a.model.ExpandedName()
	a.model.Confirm = ui.ActionNone
	if name == "" || action == ui.ActionNone {
		return
	}

	namespace := a.namespace
	kind := a.model.Kind
	pods := a.expandedPods()
	verb := "restarting"
	if action == ui.ActionTerminate {
		verb = "terminating"
	}
	a.model.Note = verb + " " + name
	a.model.Expanded = ""

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		var err error
		if action == ui.ActionTerminate {
			err = a.client.Terminate(ctx, namespace, kind, name, pods)
		} else {
			err = a.client.Restart(ctx, namespace, kind, name, pods)
		}
		if err != nil {
			a.pods <- podResult{namespace: namespace, err: err}
			return
		}
		a.pods <- podResult{namespace: namespace, result: kube.Result{}, err: nil}
	}()
}

func (a *app) expandedPods() []string {
	index := a.model.ExpandedIndex()
	if index < 0 {
		return nil
	}
	return a.model.Rows[index].Pods
}

func (a *app) scrollTo(index int) {
	_, height := a.screen.Size()
	room := ui.Visible(height)

	if index < a.model.Offset {
		a.model.Offset = index
	}
	if index >= a.model.Offset+room {
		a.model.Offset = index - room + 1
	}
}

func (a *app) clamp() {
	if a.model.Screen == ui.ScreenInspect {
		a.clampInspect()
		return
	}

	_, height := a.screen.Size()
	room := ui.Visible(height)
	model := a.model

	if model.ExpandedIndex() < 0 {
		model.Expanded = ""
		model.Confirm = ui.ActionNone
	}
	if model.Offset > len(model.Rows)-room {
		model.Offset = len(model.Rows) - room
	}
	if model.Offset < 0 {
		model.Offset = 0
	}
}
