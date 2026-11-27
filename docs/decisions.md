# Implementation decisions

The supplied architecture proposal is the design reference, not a description
of pre-existing source code. This document records the initial implementation's
choices for the points it left open.

1. **Index:** `.workit.toml`, schema version 1, typed node records with path/kind
   and optional GitHub identity. Grouping follows immediate registered parent
   paths. Namespace nesting is unrestricted; Git checkout nodes are leaves.
2. **Scope:** nearest ancestor index, deepest registered ancestor, then subtree.
   Selection resolves the working directory physically; workspace paths cannot
   traverse symlink directories. `-C` provides explicit directory selection.
3. **Discovery:** visible ordinary directory names, including empty namespaces;
   no hidden/vendor/node_modules traversal, no repository recursion, no nested
   Workit workspace traversal. Optional root exclude paths omit management trees
   before traversal and cannot overlap registered nodes. Unidentifiable Git origins are reported, not
   silently treated as absent. Any incomplete/conflicting scan blocks sync.
4. **Repository identity:** lowercase GitHub owner/name. HTTPS and SSH origins
   are accepted; bare repositories and non-GitHub nodes are outside v1. Protocol
   config governs only new clones. Authentication stays with Git/GitHub tooling.
5. **Planning:** sequential execution, complete preflight before Git mutations,
   optional `--dry-run`. No parallel workers, wave recursion, plugin execution
   or arbitrary user-defined functions in this first version.
6. **Pull:** the current committed local branch must match the given local
   branch; checkout must be clean including untracked content. Verify remote
   branch, fetch, merge `--ff-only`. No implicit checkout or stash. Divergence
   leaves the local branch unchanged, though fetch may refresh tracking refs.
7. **Push:** configured origin upstream only, explicit HEAD-to-upstream refspec,
   no force. Validate distinct push URLs against origin identity.
8. **Removal:** `.void` is root-owned and lazy. All removal plans require
   confirmation (or explicit `--yes`), including permanently cleaning Void.
   `void clean` requires the physical root, not just root selection from an
   unregistered directory. There is no `-p` abbreviation or implicit sync mode.
9. **Recovery:** move first, save index second; rollback removal if saving fails.
   Preserve failed additions in quarantine. Records and index snapshots are
   written/synced before a quarantine rename. An interrupted cross-layer
   operation can require manual recovery; no automatic journal replay yet.
10. **Ownership:** runtime metadata stays at the workspace root; source repos
    need no `.node` or front files. Workit does not perform an automatic legacy JSON conversion; Workspace owns
    its deliberately migrated TOML catalog.

## Packages

`graph` owns pure tree semantics. `workspace` owns use cases, plans and adapter
interfaces. `index`, `workdir`, `void` and `git` implement those ports. `cli`
constructs Cobra commands and presents results. `app` is a small composition
root shared by root and cmd/workit entry points, allowing both conventional
installation and `go run .` without duplicated adapter wiring.

The proposal's per-command/per-type filenames are not mandatory empty scaffolds:
related definitions stay together until a further split helps maintainability.

## Remaining extensions

Prebuilt release binaries, a reusable legacy-index importer, registry/tree
editing beyond add/remove, restoration commands, automatic crash replay,
parallel execution, scan exclusion configuration and Windows-specific filesystem
support remain separate work. Destructive tests use temporary directories only.
