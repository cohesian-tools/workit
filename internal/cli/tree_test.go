package cli

import (
	"strings"
	"testing"

	"github.com/cohesian-tools/workit/internal/workspace"
)

func TestRepoStatusIndicators(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*workspace.RepoState)
		want string
	}{
		{"synced", func(*workspace.RepoState) {}, "head ✅ synced (main → origin/main) | stage ✅ no changes | conflicts ✅ none"},
		{"ahead and staged", func(s *workspace.RepoState) { s.Ahead, s.Staged = 2, 1 }, "head ⬆️ ahead 2 (main → origin/main) | stage ☑️ 1 staged | conflicts ✅ none"},
		{"behind and untracked", func(s *workspace.RepoState) { s.Behind, s.Untracked = 3, 1 }, "head ⬇️ behind 3 (main → origin/main) | stage ⚠️ 1 untracked | conflicts ✅ none"},
		{"diverged and dirty", func(s *workspace.RepoState) {
			s.Ahead, s.Behind, s.Staged, s.Unstaged, s.Untracked, s.Conflicts = 1, 2, 3, 4, 5, 6
		}, "head ↔️ unsynced (+1 −2) (main → origin/main) | stage ☑️ 3 staged ⚠️ 4 unstaged, 5 untracked | conflicts ⚠️ 6"},
		{"unrelated", func(s *workspace.RepoState) { s.Ahead, s.Behind, s.Unrelated = 1, 1, true }, "head ↔️ no common ancestor visible"},
		{"detached", func(s *workspace.RepoState) { s.Branch, s.Upstream = "(detached)", "" }, "head ⚠️ detached"},
		{"unborn", func(s *workspace.RepoState) { s.HasCommit = false }, "head ⚠️ no commits"},
		{"no upstream", func(s *workspace.RepoState) { s.Upstream = "" }, "head ⚠️ no upstream (main)"},
		{"missing upstream ref", func(s *workspace.RepoState) { s.HasComparison = false }, "head ⚠️ upstream unavailable (main → origin/main)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := workspace.RepoState{Branch: "main", Upstream: "origin/main", HasCommit: true, HasComparison: true}
			test.edit(&state)
			if got := repoStatus(state); !strings.HasPrefix(got, test.want) {
				t.Fatalf("got %q, want prefix %q", got, test.want)
			}
		})
	}
}
