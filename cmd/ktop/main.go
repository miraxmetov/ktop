package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/miraxmetov/ktop/internal/kube"
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
	fs.Float64Var(&intervalArg, "i", 2, "refresh interval in seconds")
	fs.Float64Var(&intervalArg, "interval", 2, "refresh interval in seconds")
	fs.BoolVar(&showVersion, "V", false, "print version and exit")
	fs.BoolVar(&showVersion, "version", false, "print version and exit")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: ktop [namespace] [-n namespace] [-c context] [-i seconds]\n\n")
		fs.PrintDefaults()
	}
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
	namespace := opts.namespace
	if namespace == "" {
		namespace = defaultNamespace
	}

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
		interval:   opts.interval,
		pods:       make(chan podResult, 1),
		namespaces: make(chan namespaceResult, 1),
		model: &ui.Model{
			Namespace: namespace,
			Context:   client.Context,
			Interval:  opts.interval,
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
	a.model.Cursor = 0
	a.model.Offset = 0
	a.fetchPods()
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

func (a *app) applyChoice() {
	options := a.model.Options()
	choice := a.model.Choice

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
	if pods {
		a.model.PodQuery = options[a.model.Choice]
		a.reselect()
	} else {
		a.model.NamespaceQuery = options[a.model.Choice]
	}
	a.model.Choice = 0
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
			a.model.Cursor -= 3
		} else {
			a.model.Choice--
		}
	case e.Buttons()&tcell.WheelDown != 0:
		if a.model.Focus == ui.FocusTable {
			a.model.Cursor += 3
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
		if a.model.PodQuery != "" {
			a.model.PodQuery = ""
			a.reselect()
			return false
		}
		return true
	case tcell.KeyUp:
		a.model.Cursor--
	case tcell.KeyDown:
		a.model.Cursor++
	case tcell.KeyPgUp:
		a.model.Cursor -= page
	case tcell.KeyPgDn:
		a.model.Cursor += page
	case tcell.KeyHome:
		a.model.Cursor = 0
	case tcell.KeyEnd:
		a.model.Cursor = len(a.model.Rows) - 1
	case tcell.KeyTab:
		a.focusPods()
	case tcell.KeyBacktab:
		a.focusNamespace()
	case tcell.KeyRune:
		switch e.Rune() {
		case 'q':
			return true
		case 'k':
			a.model.Cursor--
		case 'j':
			a.model.Cursor++
		case 'g':
			a.model.Cursor = 0
		case 'G':
			a.model.Cursor = len(a.model.Rows) - 1
		case 'r':
			a.fetchPods()
			a.fetchNamespaces()
		case '/':
			a.focusPods()
		case 'n':
			a.focusNamespace()
		}
	}
	return false
}

func (a *app) handleInputKey(e *tcell.EventKey) bool {
	pods := a.model.Focus == ui.FocusPods

	switch e.Key() {
	case tcell.KeyCtrlC:
		return true
	case tcell.KeyEscape:
		if !pods {
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
		if pods {
			a.model.PodQuery = trimLast(a.model.PodQuery)
			a.reselect()
		} else {
			a.model.NamespaceQuery = trimLast(a.model.NamespaceQuery)
		}
		a.model.Choice = 0
	case tcell.KeyCtrlU:
		if pods {
			a.model.PodQuery = ""
			a.reselect()
		} else {
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
		if pods {
			a.model.PodQuery += string(e.Rune())
			a.reselect()
		} else {
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

func (a *app) clamp() {
	_, height := a.screen.Size()
	room := ui.Visible(height)
	model := a.model

	if model.Cursor > len(model.Rows)-1 {
		model.Cursor = len(model.Rows) - 1
	}
	if model.Cursor < 0 {
		model.Cursor = 0
	}
	if model.Cursor < model.Offset {
		model.Offset = model.Cursor
	}
	if model.Cursor >= model.Offset+room {
		model.Offset = model.Cursor - room + 1
	}
	if model.Offset > len(model.Rows)-room {
		model.Offset = len(model.Rows) - room
	}
	if model.Offset < 0 {
		model.Offset = 0
	}
}
