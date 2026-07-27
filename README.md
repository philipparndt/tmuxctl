# tmuxctl

Open project windows in tmux from YAML templates.

```sh
make build     # builds ./tmuxctl
make install   # go install into GOBIN (default ~/go/bin)
```

## Usage

```sh
tmuxctl add acme-apigateway-go   # exact folder name
tmuxctl add apigateway-go         # substring also works
tmuxctl add -t dev myproject      # explicit template
tmuxctl add -t support            # template with a fixed dir needs no project
tmuxctl add -s main -b myproject  # target session "main", stay in background
tmuxctl workspace acme           # apply a whole workspace (all its windows)
tmuxctl workspace -s ws-test acme  # ... into a separate session, e.g. to test
tmuxctl ui                        # interactive picker for all of the above
tmuxctl templates                 # list configured templates
tmuxctl workspaces                # list configured workspaces
```

`tmuxctl ui` opens a small TUI (Bubble Tea): one compact, filterable list
grouped into labeled sections — workspaces (⊞), fixed-dir templates (⊡), and
the projects below `dev_dirs`.
A project is a repository root (a `.git` is present) — grouping folders and
repos nested inside other repos are not listed, and the search never
descends into a repository. Initially only *recent* projects are shown —
those with git activity in the last `recent_days` (default 14), newest
first and grouped into time sections (today, yesterday, last 7 days, …);
`a` toggles the full alphabetical list, and filtering always searches all
projects. Recency is read from the mtimes of `.git/index`, `HEAD`, and
`FETCH_HEAD` — local stats only, no git commands, no network; since IDEs
refresh git status on save, the index mtime closely tracks the last time a
project was actually worked on. Set `recent_days: -1` to always show all.
Type `/` to filter, enter to apply — picking a
project asks for the template in a second step, offering only templates
without a fixed dir (the default template preselected); if just one
qualifies it is applied directly without asking. Esc goes back, q quits.

`f` searches projects *by their files* — file names and file content —
for when you remember a config file or a code snippet but not which project
it lives in. Requires [ripgrep](https://github.com/BurntSushi/ripgrep)
(`rg`). The search streams: project roots are swept in batches, most
recently used first, so the likely hits appear within a few hundred
milliseconds while older bulk fills in behind a progress hint; typing
restarts the search, esc leaves the mode. Each result row shows the first
matching file as evidence (`(content)` marks a content match, `+N` more
matching files). Nothing of this runs outside `f` mode — the normal picker
is untouched.

Bind it to a tmux popup with:

```sh
tmuxctl setup           # binds prefix + "-" in ~/.tmux.conf
tmuxctl setup -key o    # ... or another prefix key
tmuxctl setup -n        # just print the config block
```

`setup` writes a marker-delimited block (`# >>> tmuxctl >>>` … `# <<< tmuxctl
<<<`) into `~/.tmux.conf` (or `~/.config/tmux/tmux.conf` if only that
exists), pointing at the running binary's path. Re-running it replaces only
that block, so it is safe after every update or key change; a running tmux
server is reloaded immediately. The binding:

```tmux
bind - display-popup -E -w 80% -h 70% "tmuxctl ui || { echo; echo '-- failed, press any key --'; read -r _; }"
```

The `||` branch keeps the popup open when something fails, so the error
stays readable instead of flashing by. New windows land in the session the
popup was opened from: tmuxctl reads the session id from `$TMUX`, because a
popup has no pane context and tmux would otherwise fall back to whatever
session was most recently active server-wide.

`add` searches the configured `dev_dirs` recursively (up to `search_depth`
levels) for a folder whose name matches the argument — exact matches win over
substring matches. When a match is ambiguous, an interactive selector opens
(↑/↓ or j/k, enter to select, q to cancel); without a terminal the candidates
are listed as an error instead. It then creates a new tmux window named after
the folder (minus any configured `trim_prefix`/`trim_suffix`) and applies the
template: every pane starts in the project directory, with its `run` command
typed in.

Inside tmux the window is added to the current session; outside, the fallback
`session` is created if needed and the attach command is printed.

## Configuration

`~/.config/tmuxctl/config.yaml` — created with these defaults on first run:

```yaml
dev_dirs:
  - ~/dev
search_depth: 3
recent_days: 14   # ui: initially only projects active in the last N days (-1: all)
session: dev
default_template: dev
templates:
  dev:
    trim_prefix: ["acme-"]   # window title: folder name without this prefix
    panes:
      - run: claude   # left: claude code in the project folder
      - run: ""       # right: plain shell in the project folder
        split: right
        size: 50%
```

The first pane in a template is the window's initial pane; each further pane
is split off the previous one. Per pane:

| key     | meaning                                                        |
|---------|----------------------------------------------------------------|
| `run`   | command typed into the pane (empty = just a shell)             |
| `split` | `right` (default) or `bottom`                                  |
| `size`  | passed to `tmux split-window -l`, e.g. `50%` or `20`           |

Per template, `trim_prefix` and `trim_suffix` are lists of strings stripped
from the folder name (first matching entry each) to form the window title,
e.g. `trim_prefix: ["acme-"]` names the window for `acme-apigateway-go`
just `apigateway-go`. `name` overrides the title entirely.

### Claude code panes

Claude code has first-class support: instead of `run`, a pane can declare
`claude` with a permission mode and an initial prompt or slash command —
passed as CLI arguments, so there is no key-sending fragility. Combined with
`dir`, a template can be pinned to a fixed folder so no project argument is
needed, and `background: true` keeps the current window focused:

```yaml
  support:
    dir: ~/dev/acme/refactorings/complete
    name: support
    background: true
    panes:
      - claude:
          mode: auto            # --permission-mode auto
          prompt: /tar-monitor  # initial prompt or slash command
          # args: [--continue]  # extra claude CLI arguments
```

`tmuxctl add -t support` opens claude in that folder in auto mode and starts
`/tar-monitor`, without switching away from the current window. `add -b`
forces background mode for any template; `add -s <session>` targets (and
creates, if needed) a specific session.

### Workspaces

A workspace is a named set of windows applied in one command. Each window
references a template, optionally overriding its fields, or is defined
inline; `project` resolves the folder by searching `dev_dirs` like the `add`
argument does:

```yaml
workspaces:
  acme:
    windows:
      - name: system tests
        dir: ~/dev/acme/acme-test
        background: true
        panes:
          - claude:
              mode: auto
              prompt: /monitor-ci
      - template: support
      - template: dev             # claude left, terminal right
        project: acme-cluster-ctl
```

`tmuxctl workspace acme` creates all windows first (apps boot in parallel)
and then processes their send items. `workspace -s ws-test acme` applies it
to a fresh session instead — handy for testing a workspace without touching
the current session (switch with `tmux switch-client -t ws-test`, throw away
with `tmux kill-session -t ws-test`). Newly created sessions contain exactly
the workspace windows, no placeholder shell window.

### Driving other interactive apps

For apps without first-class support, a pane can type into the app it
launched via `send`. Each item can observe the screen instead of sleeping
blindly: `await` is a regex polled against the visible pane content (with
`timeout`, default 30s) before typing, and Enter is only pressed once the
typed text is actually visible in the pane. `wait` adds a fixed delay,
`enter: false` sends bare keys (tmux key names like `BTab` work), and
`escape: true` closes an autocomplete popup before Enter:

```yaml
    panes:
      - run: myrepl
        send:
          - await: "myrepl ready"   # observe the screen, don't guess timing
            keys: load module x
```

Pick an `await` pattern that appears only in the app's output — the typed
`run` command and your shell prompt are also part of the pane content.
