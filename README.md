# ktop

A terminal window into one Kubernetes namespace: what its pods and workloads are doing right now,
what is wrong with them, and what you can do about it without leaving the screen.

It shows CPU and memory against each pod's own limits, restarts and OOM kills, the last exit code
with its signal decoded and how long ago the pod restarted. The same table also reads deployments,
replica sets, daemon sets and stateful sets, with every number summed over the pods they own. It sorts and filters that list, opens
any pod's configuration beside its live log stream, and can restart or delete a pod after asking
you twice. Namespace and kubeconfig are switched from inside the program.

A single static binary: no interpreter, no shared libraries, no `kubectl` at runtime. ktop talks
to the API server itself, using your kubeconfig the same way kubectl does, including exec
credential plugins such as `gke-gcloud-auth-plugin`.

## Install

```sh
brew install miraxmetov/tap/ktop                                                   # macOS
curl -fsSL https://raw.githubusercontent.com/miraxmetov/ktop/main/install.sh | sh  # anywhere
```

Homebrew keeps ktop up to date with everything else you have installed; `brew upgrade ktop` and
`brew uninstall ktop` work as usual.

The script detects your platform (macOS and Linux, amd64 and arm64), downloads the matching
release, verifies its SHA256 against `checksums.txt`, installs the binary into `~/.local/bin` and
adds completions for the shells it finds. It is POSIX sh, so your own shell does not matter, and
nothing needs root.

```sh
PREFIX=/usr/local ./install.sh     # install somewhere else
KTOP_VERSION=v1.0.0 ./install.sh   # pin a release
KTOP_BASE_URL=... ./install.sh     # fetch assets from a mirror
./install.sh --from-source         # build from a clone instead of downloading
./install.sh --uninstall           # remove ktop and its completions
```

If the target directory is not in your `PATH`, the installer prints the line to add, in the syntax
of your own shell. Running the same command again is how you update.

## Usage

```sh
ktop                      # namespace of the current kube context
ktop production           # a namespace by name
ktop -n production        # same thing
ktop production -i 5      # refresh every 5 seconds (default: 1)
ktop -c staging -n web    # another kube context
```

Without an argument ktop takes the namespace of the current kubeconfig context, falling back to
`default` when the context does not name one. The cluster comes from `$KUBECONFIG`, falling back
to `~/.kube/config`:

```sh
export KUBECONFIG=~/.kube/prod.yaml
```

| Key | Action |
| --- | --- |
| `/` | search pods by name |
| `m` / `M` | choose what the table lists: pods or a kind of workload |
| `n` / `N` | search and switch namespace |
| `c` / `C` | change the kubeconfig, with file completion |
| `Tab` | complete the search box with the highlighted entry |
| `Shift+Tab` | move between the two search fields |
| `Enter` | take the highlighted entry: open that pod's actions, or switch to that namespace |
| `Esc` | step back: the open question, the open actions, the search field, the filters, then quit |
| `Ctrl+U` | clear the current search field |
| `↑` `↓` / `k` `j` | pick from the open list, scroll the table, or scroll the log stream while inspecting |
| `Shift+↑` `↓` | scroll the configuration pane while inspecting |
| `PgUp` `PgDn` | scroll a page |
| `g` `G` / `Home` `End` | jump to the top or the bottom |
| `r` / `R` | refresh right now, ahead of the interval |
| `q` / `Q` / `Ctrl+C` | quit |

The mouse works everywhere: click a search box to open it, click an entry in a list to take it,
click a column header to sort by it, click a pod name to open its actions, click anywhere else to
close what is open, and scroll the wheel over the table, a list or the inspect screen.

## What the table lists

A framed box above the counters says what you are looking at, `Pods` to begin with. Click it and
a menu offers `Deployments`, `ReplicaSets`, `DaemonSets` and `StatefulSets`. Everything else keeps
working the same way: the same search, the same filters, the same sorting, the same actions.

A workload row carries what its pods add up to. CPU, memory, restarts and OOM kills are summed
over the pods it owns, `STATUS` reads as `2/3 ready`, and instead of the pod columns `EXIT` and
`LAST RESTART` you get `CREATED`, the age of the workload itself, and `LAST POD RESTART`, the
moment its most recently restarted pod went down.

## The table

Pods rest in alphabetical order, so everything from one deployment stays together instead of being
pulled up by a restart or a spike. `POD` always carries an arrow: `↑` for the alphabet, `↓`
against it, and a click flips it.

Every other column but `EXIT` carries a `⇅`. One click sorts from the largest, another from the
smallest, a third drops the sort. While one column sorts, every other mark steps aside, the `POD`
arrow included, so the order is never ambiguous; a click on `POD` then drops that sort and hands
the table back to the alphabet.

| Column | Meaning |
| --- | --- |
| `STATUS` | pod phase, or the waiting or terminated reason; `NotReady n/m` when containers are not ready |
| `CPU` / `MEM` | current usage, summed over the pod's containers |
| `%LIM` | usage against the sum of the containers' limits; `-` when any container has no limit |
| `RESTART CTR` | the pod's own restart total, with `+N` for restarts seen while ktop was watching |
| `OOM CTR` | OOM kills ktop saw while watching |
| `EXIT` | last exit code, with signal or reason decoded |
| `LAST RESTART` | age and local clock time of the most recent termination |

Green below 75%, amber from 75%, red from 90%. A percentage appears only when every container in
the pod declares that limit: comparing the whole pod's usage against one sidecar's limit would be
a number without meaning. Usage columns need metrics-server in the cluster; without it they show
`-` and the rest still works.

When the table has nothing to show, a small framed note rests in the middle of it: `Oops!` over
`No resources found in this namespace.`, or, while a filter or a search is on, the reason that
nothing came through.

The table fills whatever terminal it is given: the pod column takes the space the other columns
leave, and the row count follows the window height. Narrow windows drop the rightmost columns
instead of wrapping. A pod name too long for its column scrolls inside it once you click that pod:
it rests for a moment, walks to its end a character at a time, rests again and walks back.

## Finding what is wrong

The status line is a filter. `N critical / N warnings` counts the pods behind each number, and
`issues regarding status / cpu / memory` decides what those words mean: pick `cpu` and the
counters describe CPU against limits alone, pick `status` and they count crashing and not-ready
pods. Click a number to keep only those pods, click the chosen word again to let the rest back in.
Nothing is chosen by default, the current choice is highlighted, and `Esc` clears both. In a
namespace with hundreds of pods that is the fastest way to see what is actually wrong.

Press `/` to search pods by name. The filter is applied again on every refresh, so a pod that
comes back under a new name appears or disappears on its own. Press `n` to search namespaces, or
`c` to point ktop at another kubeconfig: the list offers what is in the directory you are typing,
directories open as you take them, and a file switches the cluster, namespace included. Opening
any search shows everything ktop can see and narrows the list as you type.

## Acting on a pod or a workload

Click a name and three actions open under it, stacked inside the first column and pushing the rest
of the table down: `[ Inspect ]`, `[ Restart ]`, `[ Terminate ]`.

Restart and Terminate never fire on the first click. The three lines become `Are you sure?`,
`[ Yes ]` and `[ Cancel ]`, and only `Yes` acts. What acting means follows the row. A pod is
deleted: gracefully for Restart, so its controller brings it back, and immediately for Terminate,
which is what a stuck pod needs. A deployment, daemon set or stateful set is rolled out the way
`kubectl rollout restart` does it, by stamping `kubectl.kubernetes.io/restartedAt` on its pod
template, so the controller replaces the pods at its own pace and nothing is deleted behind its
back. A replica set has no rollout of its own, so its pods are deleted gracefully instead.
Terminate on any workload force-deletes the pods it owns and leaves the object itself alone.

Inspect gives the pod, or the workload, a screen of its own, split into two framed panes: its
configuration on the left, a live log stream on the right. The right pane is titled with the pod
it streams; for a workload that title carries a `⇅`, and a click on it or `p` picks another of its
pods. The two panes scroll apart: the arrows, `PgUp` `PgDn` and `Home` `End` move the stream, the
same keys with `Shift` move the configuration, and the wheel moves whichever pane it is over. The
stream follows its tail until you scroll it back, and then holds its place while new lines arrive.
Three buttons at the bottom pick how the configuration reads: `[ default ]` lists the fields, `[ textual ]` says the same in a few sentences, `[ yaml ]`
shows the manifest. Each line of the stream carries the time the container printed it, which
Kubernetes keeps for the lines written before ktop started too. The log pane has its own search
box fenced off at its bottom edge: press `/` or click it, the stream filters as you type and every
match is highlighted in place. A line too long for the pane wraps under its own timestamp instead
of being cut, so nothing is lost. `Esc` steps back to the table.

All three views colour what usually matters: a broken status or an OOM kill in red, restarts,
missing limits, a BestEffort class or a false condition in amber, a running container in green,
while labels, nodes and addresses stay plain so the eye lands on the exception. A workload reads
the same way, with its replica count coloured by how many of them are ready and the quality of
service its pod template asks for spelled out.

## Counters

`RESTART CTR` is the cumulative `restartCount` Kubernetes keeps per container: a container restart
does not replace the pod, so the number survives crash loops and resets only when the pod itself
is replaced, by a rollout, an eviction or a drain. When that total grows while ktop is running,
the row shows `9 +2` in amber, so a pod restarting right now is easy to spot among pods that
merely restarted last week.

`OOM CTR` has no lifetime equivalent to report: the API keeps only the reason of the most recent
termination, and events expire within the hour, so no cumulative OOM count exists to read. The
column therefore counts the OOM kills ktop itself observed, and `EXIT` tells you whether the last
termination was one.

## Permissions

ktop reads the cluster from your kubeconfig and asks for nothing else. What it cannot read is
reported in plain words on the status line instead of a Go error:

| Missing | What you see |
| --- | --- |
| `list pods` in the namespace | `no permission to list pods in namespace production` |
| pod metrics, or no metrics-server | `metrics-server unavailable, CPU and MEM hidden`, the table keeps working |
| `list namespaces` | `no permission to list namespaces, type a name and press Enter` |
| pod logs | `no permission to read the logs of api-worker-1`, the configuration pane still works |
| unreachable or expired credentials | `cannot reach the cluster at api.example.com`, `cluster rejected the credentials from your kubeconfig` |

Terminate, and Restart on a pod or a replica set, need `delete` on pods in that namespace; Restart
on a deployment, daemon set or stateful set needs `patch` on that object. Without it the click
reports the refusal, `no permission to restart deployment api`, and changes nothing.

## Development

```sh
go test ./...
go build -o dist/ktop ./cmd/ktop
go install github.com/miraxmetov/ktop/cmd/ktop@latest
```

Releases are cut by pushing a tag; GitHub Actions runs goreleaser, which cross-compiles all four
targets, writes `checksums.txt` and publishes the archives that `install.sh` downloads.

```sh
git tag v1.0.0 && git push origin v1.0.0
goreleaser release --snapshot --clean --skip=publish   # dry run, artifacts in dist/
```

## License

MIT
