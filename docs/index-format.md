# Index format v1

`.workit.toml` belongs at the physical workspace root. The nearest ancestor index
determines which workspace a command belongs to. A malformed nearest index is an
error; Workit never skips it to operate on an outer workspace.

```toml
version = 1
protocol = 'ssh'
exclude = ['scripts', 'tests']

[[nodes]]
path = '.'
kind = 'r'

[[nodes]]
path = 'tools'
kind = 'n'

[[nodes]]
path = 'tools/workit'
kind = 'w'
repository = 'cohesian-tools/workit'
```

`exclude` is optional. It contains workspace-relative directory paths, matched
by complete path components (case-insensitively). Excluded trees are never
traversed or registered, and cannot contain registered nodes. The root cannot
be excluded. Commands run inside an excluded directory fail instead of
implicitly selecting the whole workspace. Hidden folders, node_modules and
vendor are always excluded independently.

Only these fields are supported. Unknown fields, duplicate TOML keys,
unsupported versions and invalid graph structure fail before mutation.

- Exactly one root: path `.`, kind `r`.
- Namespace `n` and repository/workstation `w` have workspace-relative paths.
- Paths use `/` separators and ordinary directory components. Each component
  starts with an ASCII letter/digit, followed by letters/digits, `.`, `_`, `-`.
  Hidden components, `node_modules`, `vendor`, trailing dots, absolute paths,
  backslashes, empty components and traversal components are not supported.
- Duplicate and case-colliding paths are errors, including on Linux.
- Every non-root node has its immediate directory parent registered as a
  composite. This derives the single grouping edge; no duplicate adjacency list
  is persisted. Parent closure makes the tree rooted, reachable and acyclic.
- A workstation has a canonical lowercase GitHub `owner/repository` identity;
  composites have no repository field. The same repository can have independent
  checkouts at different registered paths.
- Only `https` and `ssh` are clone protocols. They do not change identity or
  rewrite the URLs of existing checkouts.

Selection uses complete path components. For example, `tools` includes
`tools/workit` but not `tools-extra`. An unregistered directory selects its
deepest registered ancestor. It does not register itself; sync explicitly
discovers eligible descendants of that selected composite.

TOML stores authoritative registration only. Ahead/behind status, physical
discovery, plans, and quarantine content are separate derived/state layers.
