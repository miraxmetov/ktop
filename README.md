# ktop

Live pod resource usage as a percentage of limits, in a terminal.

One screen per namespace: CPU and memory against the pod's limits, restarts, OOM kills,
the last exit code with its signal decoded, and how long ago the pod last restarted.
Problem pods sort to the top, the hottest first.

```
Current namespace: production                                        ~/.kube/prod.yaml
Search for namespaces...

3 critical / 5 warning                                                    16:42:07

api                             2/37

POD                          STATUS        CPU    %LIM      MEM   %LIM  RESTART CTR  OOM CTR  EXIT            LAST RESTART
-------------------------------------------------------------------------------------------------------------------------------
api-worker-5f7c9d8b4-xk2vn   Running      940m     94%   1840Mi    92%         9 +2        2  137 (SIGKILL)   4m12s ago (at 16:37:55)
api-gateway-6b8d7c9f5-2mkqp  Running      210m     21%    612Mi    60%            0        0  -               -

```

A single static binary: no interpreter, no shared libraries, no `kubectl` at runtime.
ktop talks to the API server itself, using your kubeconfig the same way kubectl does,
including exec credential plugins such as `gke-gcloud-auth-plugin`.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/miraxmetov/ktop/main/install.sh | sh
```

The script detects your platform (macOS and Linux, amd64 and arm64), downloads the
matching release, verifies its SHA256 against `checksums.txt`, installs the binary into
`~/.local/bin` and adds completions for the shells it finds. It is POSIX sh, so your own
shell does not matter.

```sh
PREFIX=/usr/local ./install.sh     # install somewhere else
KTOP_VERSION=v1.2.0 ./install.sh   # pin a release
KTOP_BASE_URL=... ./install.sh     # fetch assets from a mirror
./install.sh --from-source         # build from a clone instead of downloading
./install.sh --uninstall           # remove ktop and its completions
```

If the target directory is not in your `PATH`, the installer prints the line to add,
in the syntax of your own shell.

## Usage

```sh
ktop                      # namespace of the current kube context
ktop production           # a namespace by name
ktop -n production        # same thing
ktop production -i 5      # refresh every 5 seconds (default: 2)
ktop -c staging -n web    # another kube context
```

Without an argument ktop takes the namespace of the current kubeconfig context, falling back to
`default` when the context does not name one.

| Key | Action |
| --- | --- |
| `/` | search pods by name |
| `n` | search and switch namespace |
| `c` | change the kubeconfig, with file completion |
| `Tab` | complete the search box with the highlighted entry |
| `Shift+Tab` | move between the two search fields |
| `↑` `↓` | pick from the open list, or move the cursor in the table |
| `Enter` | take the highlighted entry: jump to that pod, or switch to that namespace |
| `Esc` | leave the search field, then clear the filter, then quit |
| `Ctrl+U` | clear the current search field |
| `k` `j` | move the cursor |
| `PgUp` `PgDn` | scroll a page |
| `g` `G` / `Home` `End` | jump to the first or last pod |
| `r` | refresh right now, ahead of the interval |
| `q` / `Ctrl+C` | quit |

The kubeconfig in use is shown at the top right. Click it, or press `c`, to point ktop at a
different one: the list offers what is in the directory you are typing, directories open as you
take them, and a file switches the cluster, namespace included.

The mouse works too: click a search box to open it, click an entry in the list to take it,
click a row to put the cursor there, click anywhere else to close the list, and scroll the
wheel over the table or the list.
Opening a search shows everything ktop can see — every pod in the namespace, every namespace
you may list — and the list narrows as you type.

The pod filter is matched against every refresh, so a pod that restarts under a new name
appears or disappears on its own, and the cursor follows the pod it was on.
Namespace search lists what you are allowed to see; without that permission you can still
type a namespace by hand and press Enter.

The cluster comes from `$KUBECONFIG`, falling back to `~/.kube/config`:

```sh
export KUBECONFIG=~/.kube/prod.yaml
```

## Permissions

ktop reads the cluster from your kubeconfig and asks for nothing else. What it cannot read
is reported in plain words on the status line instead of a Go error:

| Missing | What you see |
| --- | --- |
| `list pods` in the namespace | `no permission to list pods in namespace production` |
| pod metrics, or no metrics-server | `metrics-server unavailable, CPU and MEM hidden`, the table keeps working |
| `list namespaces` | `no permission to list namespaces, type a name and press Enter` |
| unreachable or expired credentials | `cannot reach the cluster at api.example.com`, `cluster rejected the credentials from your kubeconfig` |

## Columns

| Column | Meaning |
| --- | --- |
| `STATUS` | pod phase, or the waiting/terminated reason; `NotReady n/m` when containers are not ready |
| `CPU` / `MEM` | current usage, summed over the pod's containers |
| `%LIM` | usage against the sum of the containers' limits; `-` when no limit is set |
| `RESTART CTR` | the pod's own restart total, with `+N` for restarts seen while ktop was watching |
| `OOM CTR` | OOM kills ktop saw while watching |
| `EXIT` | last exit code, with signal or reason decoded |
| `LAST RESTART` | age and local clock time of the most recent termination |

`RESTART CTR` is the cumulative `restartCount` Kubernetes keeps per container: a container
restart does not replace the pod, so the number survives crash loops and resets only when the
pod itself is replaced, by a rollout, an eviction or a drain. When that total grows while ktop
is running, the row shows `9 +2` in yellow, so a pod restarting right now is easy to spot among
pods that merely restarted last week.

`OOM CTR` has no lifetime equivalent to report: the API keeps only the reason of the most recent
termination, and events expire within the hour, so no cumulative OOM count exists to read. The
column therefore counts the OOM kills ktop itself observed, and `EXIT` tells you whether the last
termination was one.

Green below 75%, yellow from 75%, red from 90%.
The table fills whatever terminal it is given: the pod column takes the space the other columns
leave, and the row count follows the window height. Narrow windows drop the rightmost columns
instead of wrapping.
Usage columns need metrics-server in the cluster; without it they show `-` and the rest still works.

## Development

```sh
go test ./...
go build -o dist/ktop ./cmd/ktop
go install github.com/miraxmetov/ktop/cmd/ktop@latest   # once a release is tagged
```

Releases are cut by pushing a tag; GitHub Actions runs goreleaser, which cross-compiles
all four targets, writes `checksums.txt` and publishes the archives that `install.sh` downloads.

```sh
git tag v1.0.0 && git push origin v1.0.0
goreleaser release --snapshot --clean --skip=publish   # dry run, artifacts in dist/
```

## License

MIT
