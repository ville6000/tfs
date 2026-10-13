# tfs

A fast terminal client for **on-premises Azure DevOps Server** (formerly TFS).

Browse and update work items, explore the backlog hierarchy, list and check out pull requests,
and spin up git worktrees straight from a work item — with interactive terminal UIs built on
[Bubble Tea](https://github.com/charmbracelet/bubbletea), and plain/JSON output for scripts.

```
 tfs  Demo
  ID  TYPE        STATE   ASSIGNED     TITLE
▌ 6   Bug         New     Alice Smith  Login button misaligned on Safari
  4   Task        Active  Alice Smith  [API] Add reset endpoint
  3   User Story  New     —            As a user I can reset my password
  2   Feature     Active  Bob Jones    Login and accounts
3/4
enter details • o open in browser • s set state • a assign to me • w worktree • c comment • …
```

## Features

- **Work items** — list, filter, show (description, acceptance criteria, comments rendered as
  Markdown), create, update state/title/assignee, comment, open in the browser.
- **Hierarchy tree** — Epic → Feature → Story → Task, collapsible, with ancestors of matches kept
  visible so the tree stays connected.
- **Pull requests** — list with approval status, view details and reviewers, open, check out the
  source branch or create a worktree for review, open the create-PR page for the current branch.
- **Worktrees** — `tfs wi worktree 123` creates a branch named after the work item and a git
  worktree next to your repository.
- **Interactive when it helps, scriptable when you need it** — TUIs in a terminal; static tables
  when piped or with `--no-tui`; `--json` everywhere.
- **Per-repository config** layered over your global config.

Supports Azure DevOps Server 2019 and later (set `apiVersion` to `5.0` for 2019).

## Install

With Go 1.26+:

```bash
go install github.com/ville6000/tfs/cmd/tfs@latest
```

Or download a binary for macOS, Linux or Windows from the
[releases page](https://github.com/ville6000/tfs/releases).

## Quick start

```bash
tfs config wizard                 # collection URL, default project, …
export TFS_PAT=<personal access token>
tfs wi list                       # interactive list of open work items
```

### Personal access token

`tfs` authenticates with a personal access token read from the **`TFS_PAT`** environment
variable. It is never written to disk. Create a token with **Custom defined** scopes and grant
only what you use:

| Scope                      | Needed for                                                |
|----------------------------|-----------------------------------------------------------|
| Work Items — Read & write  | `wi …`, `board open` (Read is enough for list/show/tree)  |
| Code — Read                | `pr …`                                                    |

`pr checkout` and `wi worktree` run your local `git`, which uses your normal git credentials.

The token is sent with HTTP Basic auth, so `tfs` refuses plain `http://` collection URLs. For a
local test server set `TFS_ALLOW_INSECURE=1`.

## Commands

Run `tfs <command> --help` for every flag.

### Work items

```bash
tfs wi list                                  # open items in the default project
tfs wi list -t bug -s active -a @me          # filters; slugs work: -t user-story -s in-progress
tfs wi list --tag backend --resolved         # --all / --open / --resolved shorthands
tfs wi tree                                  # whole project hierarchy
tfs wi tree 1234                             # an item and its ancestors
tfs wi tree -T task -S removed               # deny-lists: --no-type / --no-state
tfs wi show 1234                             # details; omit the id to pick from a list
tfs wi open 1234
tfs wi create -t bug --title "Login fails" --parent 1200 -a @me
tfs wi create --parent 1200 --title "Sign in with SSO"   # type follows the parent: Epic → Feature → Story → Task
tfs wi create                                # interactive form
tfs wi update 1234 --state active --assigned-to @me
tfs wi comment 1234 "Retested on staging"
tfs wi comment 1234 - < notes.md            # read the comment from stdin
tfs wi worktree 1234                         # branch feature/1234-<slug> in ../1234-<slug>
```

In the tree, press `i` on an item to create a child under it. The type is preselected from your
process (Epic → Feature → User Story / Product Backlog Item / Requirement → Task), and the new
item appears in place.

By default lists and the project tree hide `Closed` and `Removed` items; the tree also hides
Test Cases, Plans and Suites unless you ask for them with `--type`.

**Slugs**: type and state filters accept slugs — the name lower-cased with spaces replaced by
hyphens — so `-t user-story` means `User Story` and `-s to-do` means `To Do`.

### Pull requests

```bash
tfs pr list                                  # active PRs in the project
tfs pr list --status all --creator alice --repo web
tfs pr show 4567
tfs pr open 4567
tfs pr checkout 4567                         # fetch and switch to the source branch
tfs pr checkout 4567 --worktree              # or a worktree at ../<repo>-pr-4567
tfs pr create                                # open the create-PR page for the current branch
tfs pr create --target develop
```

### Boards

```bash
tfs board open
```

### Project resolution

Commands that need a project use, in order: `--project`, the `defaultProject` config, and
finally the project of the current repository's git remote (`…/{project}/_git/{repo}`).

## Interactive views

Lists, the tree, and detail views are interactive when stdin and stdout are a terminal.

| Key            | Action                                  |
|----------------|-----------------------------------------|
| `↑`/`↓`, `j`/`k`, `g`/`G`, `pgup`/`pgdn` | move               |
| `/`            | filter (lists)                          |
| `enter`        | details                                 |
| `i`            | new child of the selected work item (tree, details) |
| `o`            | open in browser                         |
| `y`            | copy URL to clipboard                   |
| `s`            | set state (work items)                  |
| `a`            | assign to me (work items)               |
| `c`            | comment (work items) / checkout (PRs)   |
| `w`            | create worktree                         |
| `p`            | go to parent (work item details)        |
| `←`/`→`, `space`, `e`, `z` | collapse, expand, toggle, expand all, collapse all (tree) |
| `r`            | refresh                                 |
| `esc`          | back                                    |
| `?`            | all keys                                |
| `q`            | quit                                    |

Use `--no-tui` for static output or `--json` for machine-readable output. Colors are disabled
automatically when output is piped or `NO_COLOR` is set. Markdown styling follows
`GLAMOUR_STYLE` if set.

## Configuration

```bash
tfs config list                              # every key, its value, and where it came from
tfs config get defaultProject
tfs config set defaultProject Portal         # project file inside a repo, else global
tfs config set apiVersion 5.0 --global
tfs config unset branchTemplate
tfs config wizard
```

**Global config**: `$XDG_CONFIG_HOME/tfs/config.json` (when set to an absolute path) or
`~/.config/tfs/config.json`.

**Project config**: inside a git repository, `.config/tfs.json` or `.tfs.json` at the
repository root is merged over the global config field by field. Writes go there by default;
pass `--global` to write to the global file. Config files are written with mode `0600`.

| Key                | Default               | Description                                                   |
|--------------------|-----------------------|---------------------------------------------------------------|
| `collectionUrl`    | —                     | e.g. `https://ado.example.com/DefaultCollection` (required)   |
| `defaultProject`   | —                     | project used when `--project` is omitted                      |
| `apiVersion`       | `6.0`                 | REST API version (`5.0` for Azure DevOps Server 2019)          |
| `userIdentity`     | —                     | what `@me` resolves to when assigning (global only); otherwise asked from the server |
| `defaultType`      | `Task`                | type for `wi create` when `--type` is omitted                 |
| `branchTemplate`   | `feature/{id}-{slug}` | branch for `wi worktree`; placeholders `{id}` `{slug}` `{type}` |
| `remote`           | `origin`              | git remote used by `pr checkout` and `pr create`              |
| `requireParentFor` | —                     | comma-separated types that `wi create` requires `--parent` for |

Example project config, safe to commit:

```json
{
  "collectionUrl": "https://ado.example.com/DefaultCollection",
  "defaultProject": "Portal",
  "branchTemplate": "{type}/{id}-{slug}",
  "requireParentFor": ["User Story"]
}
```

### Environment variables

| Variable             | Purpose                                                    |
|----------------------|------------------------------------------------------------|
| `TFS_PAT`            | personal access token (required)                           |
| `TFS_COLLECTION_URL` | overrides `collectionUrl` (handy in CI)                    |
| `TFS_ALLOW_INSECURE` | allow `http://` collection URLs (development only)         |
| `TFS_BROWSER`        | command used to open URLs instead of the system default    |

## Development

```bash
make build      # ./bin/tfs
make test       # go test -race ./...
make lint       # go vet + golangci-lint
```

The code is organised as:

- `internal/domain` — work item / pull request logic (slugs, filters, trees, branch names)
- `internal/ado` — Azure DevOps Server REST client
- `internal/config` — layered config files
- `internal/git`, `internal/browser` — local integrations
- `internal/tui` — Bubble Tea views; `internal/output` — static tables; `internal/ui` — shared styles
- `internal/cli` — Cobra commands

Releases are built by GoReleaser when a `v*` tag is pushed.

## License

[MIT](LICENSE)
