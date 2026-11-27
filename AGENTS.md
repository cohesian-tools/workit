# Workit

Read README.md, docs/index-format.md and docs/decisions.md. The original design
is retained in docs/architecture-proposal.md; its final provisional section
describes the proposal's original state, not a requirement to keep code absent.

- This repo owns the Go/Cobra Workit CLI, independently of the Workspace index repository. Never initialize, reconcile, clone, fetch, pull, push, quarantine or clean
  the author's actual workspace as an implementation test. Use temporary roots
  and local bare Git remotes; scope network actions explicitly when requested.
- Keep graph pure. Workspace owns ports and coordinates adapters. Cobra only
  parses/presents commands; app composes dependencies. Avoid global command state
  and init registration. Keep the root/cmd entry points equivalent.
- `.workit.toml` version 1 owns registration and optional root exclusions.
  Excluded trees are never scanned or targeted; reject overlapping index nodes.
  Physical discovery and `.void`
  remain separate layers. Preserve ancestor closure and root identity. Nodes
  never acquire individual metadata files.
- Plan/validate before mutations. Conflicts and incomplete scans stop sync.
  Stop discovery at Git boundaries; never follow workspace directory symlinks.
- Serialize mutations through the root lock; validate and atomically save the
  index, refusing stale snapshots. Unknown schemas/fields are errors. Do not
  silently reinterpret other formats or rewrite remotes after protocol changes.
- Preserve old data during failures. Quarantine retains original paths, Git
  metadata, working edits and recovery records. Node-removal index-write failure
  must attempt restoration; clone/index failure must never silently register a
  node. Never remove an unrecognized void or empty it incidentally.
- Pull only clean, matching current branches with fast-forward merges. Push only
  configured origin upstreams, never force. Do not run Git via a shell.
- Run go test -race ./..., go vet ./... and go build ./.... Use meaningful tests
  for scope, identity, partial failure, filesystem recovery and Git behavior.
- Update docs with behavioral changes. Keep MIT in every distribution. Commit,
  push, tag and release only when requested; sign authorized commits with -S.
