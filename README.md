# Workit

Centralized knowledge. Localized behavior. Generalized execution.

Workit is a Go + Cobra CLI for a tree of independent Git repositories. A single
`.workit.toml` records the workspace; your current directory selects the deepest
registered ancestor and its subtree. Repositories need no Workit metadata.

Workit compares the index and workdir with `sync status` and reports local Git
state with `repo status`. Help, plans and results use the same tree layout.

## Run and install

Requires Go 1.25+ and Git. GitHub authentication stays external: use
`gh auth login` / `gh auth setup-git` for HTTPS, or an SSH key for SSH clones.

```sh
go install github.com/cohesian-tools/workit@v0.2.0
export PATH="$(go env GOPATH)/bin:$PATH"
workit --version
```

If you set GOBIN, put that directory on PATH instead. Go builds for the current
computer/phone. On Termux, install Go and Git with `pkg install golang git` and
build natively; the current public module can also be installed with `@latest`.

From a source checkout:

```sh
go run . --help
go test -race ./...
go build -o bin/workit ./cmd/workit
go install ./cmd/workit
```

Both source entry points share the same composition root. Native macOS and
Linux behavior is covered by CI; Android/Termux runtime tests have not been run.

## First workspace

After installing, in a new directory outside any existing Workit workspace:

```sh
mkdir work
cd work
workit init
workit node add tools
cd tools
workit node add cohesian-tools/typy.git --dry-run
workit node add cohesian-tools/typy.git
workit sync status
workit repo status
```

The default clone protocol is SSH. `workit config protocol https` changes future
clones, from any registered scope, without editing existing remotes. This tool
clones existing GitHub repositories; it does not create GitHub repositories.

`-C <directory>` selects another working directory. For example, from the source
checkout, `go run . -C /path/to/work repo status` examines that workspace.

## Commands

```text
workit
├── init
├── config protocol <https|ssh>
├── node
│   ├── add <name|owner/repository.git>
│   └── remove <name>
├── sync
│   ├── index
│   │   ├── add
│   │   └── prune
│   ├── status
│   └── workdir
│       ├── add
│       └── remove
├── repo
│   ├── status
│   ├── fetch
│   ├── pull <local_branch> <remote_branch>
│   └── push
└── void clean
```

Every command has `--help`. Mutation commands support `--dry-run`, which plans
without writing files, taking a lock, fetching or pushing. Pull planning checks
remote branch existence with a read-only Git query. Destructive commands display
the plan and require confirmation; `--yes` / `-y` supplies explicit confirmation
for automation. Bare groups show help and perform no reconciliation.

| Operation | Effect |
|---|---|
| `node add` | Create namespace / clone repository, then register it |
| `node remove` | Quarantine physical subtree, then unregister it |
| `sync status` | Compare index and workdir in the selected scope |
| `sync index add` | Register discovered, identifiable nodes |
| `sync index prune` | Unregister missing nodes; keep physical content |
| `sync workdir add` | Create/clone missing registered nodes; keep index |
| `sync workdir remove` | Quarantine extras; keep index and matched descendants |
| `void clean` | Permanently delete managed quarantine entries, at the physical root only |

Node commands and reconciliation require a root/namespace scope. Node arguments are relative
to the selected composite, not an unregistered current directory. A namespace
argument is one directory name. An argument ending in `.git` must be
`owner/repository.git`; its canonical lowercase repository name becomes the local
directory name. Existing paths, including empty directories, are collisions.

## Sync status

`workit sync status` scans the selected scope without writing or locking it.
It reports three trees, even when a category is empty.

| Tree | Nodes |
|---|---|
| ✅ Inner | Registered and present with the same kind and repository identity |
| ☑️ Outer index | Registered and missing from the workdir |
| ⚠️ Outer workdir | Present in the workdir and missing from the index |

Unmarked ancestors provide directory context. For example, a registered
`tools/` directory can appear as context in both outer trees:

```text
✅ Inner · index & workdir
./ ✅
└── tools/ ✅
    └── workit ✅ cohesian-tools/workit

☑️ Outer · index only
./
└── tools/
    └── typy ☑️ cohesian-tools/typy

⚠️ Outer · workdir only
./
└── tools/
    └── drafts/ ⚠️
```

From inside a registered checkout, status compares that checkout's identity
and stops at its Git boundary. Scan errors and identity or kind conflicts stop
the comparison with an error.

## Git forest

`workit repo status` shows each registered checkout's local branch, its cached
upstream and three groups of indicators. Status uses the local tracking ref.
Run `workit repo fetch` to refresh it first when needed.

| Group | Indicators |
|---|---|
| `head` | ✅ synced, ⬆️ ahead, ⬇️ behind, ↔️ unsynced when both sides have commits past a common ancestor |
| `stage` | ✅ no changes, ☑️ staged changes, ⚠️ unstaged or untracked changes. Both change markers can appear together. |
| `conflicts` | ✅ none, ⚠️ unresolved conflicts with a count |

HEAD also reports detached or unborn branches, a missing or unavailable
upstream, and divergence with no visible common ancestor. Git's normal untracked
mode can count an untracked directory as one entry. Repository errors are shown
at the affected node and produce a nonzero exit status.

Fetch updates `origin`, without merging. Pull requires a committed current local
branch matching its first argument and a clean checkout including untracked
content. It verifies the remote branch exists, fetches it and merges with
`--ff-only`. It never switches branches, rebases, stashes or resets. For example:

```sh
workit repo fetch
workit repo pull main main --dry-run
workit repo pull main main
workit repo push
```

Push requires each branch's existing `origin` upstream and explicitly pushes
`HEAD` to that upstream branch, without force. Fetch and push URLs must identify
the same repository; different SSH/HTTPS transports are allowed. Detached,
unborn and conflicted states cannot pull/push. All mutation targets pass
preflight before execution starts; later network/filesystem errors can still
produce partial success and are reported by node. Git operations are sequential.

## Index and discovery

See [the index format](docs/index-format.md) and
[implementation decisions](docs/decisions.md). Same-path kind/identity conflicts
stop reconciliation. A GitHub repository is identified independently of protocol.

Discovery includes ordinary ASCII-named directory namespaces, including empty
ones; it stops at `.git` directories/files and excludes hidden directories,
`node_modules` and `vendor`. It does not follow directory symlinks. Unknown Git
origins, nested Workit workspaces, unreadable directories and registered-path
type conflicts stop synchronization rather than making an incomplete scan look
like missing data. A workspace root may itself version its index with Git.

Workit does not import the older `repositories.json` automatically. Workspace
has migrated its catalog to `.workit.toml`; the old JSON is a historical snapshot.
Root `exclude` paths can keep management folders such as `scripts/` and `tests/`
outside the logical workdir. Workit remains independent of Workspace-specific
layout, scripts and migration code.

## Quarantine and recovery

`.void/` is initialized lazily, and contains a unique entry for every moved
subtree. It retains the original relative directory tree, Git metadata,
uncommitted work, an `entry.json` record and `index-before.toml` snapshot.
Quarantine uses a filesystem rename; cross-device failures do not fall back to
copy-and-delete. Index-write failure after node removal triggers a move back;
failed additions preserve newly created content in quarantine. Recovery paths
are included in error/result output. Existing unrecognized `.void/` directories
are never adopted or deleted.

Cross-layer operations are recoverable, not one atomic transaction. After an
interruption, inspect the record, payload and current index before restoring
anything; a snapshot may predate later successful operations. With Workit stopped,
move a payload back only into an absent original path, then reconcile the index
deliberately. There is no automatic `void restore` command yet.

Index edits are locked with `.workit.lock/`, validated, checked against the loaded
bytes, and installed by atomic file replacement. Writes normalize TOML formatting
and comments. A stale lock is never auto-deleted: remove that empty directory only
after verifying its owning Workit process has exited. Locks coordinate Workit
processes; they do not lock editors or other Git processes. Avoid concurrent
external filesystem changes while applying a plan. Power-loss recovery is not a
cross-filesystem transaction guarantee.

Keep these runtime paths out of a workspace's own Git history:

```gitignore
.void/
.workit.lock/
.workit-index-*
.workit-clone-*
```

Workit leaves the workspace's existing `.gitignore` untouched. The index itself
can be versioned. Ignore rules do not untrack files already committed.

## Development

```sh
go test -race ./...
go vet ./...
go build ./...
```

Tests use temporary workspaces and local Git remotes. They do not fetch, pull,
push or purge the developer's real repositories. Cobra handles parsing/help;
the graph package has no filesystem, Git, Cobra or TOML dependencies. Interfaces
belong to workspace, and the composition root injects the concrete adapters.

The [original architecture proposal](docs/architecture-proposal.md) is preserved
as supplied; [decisions](docs/decisions.md) distinguishes the implemented choices.

License: [MIT](LICENSE).
