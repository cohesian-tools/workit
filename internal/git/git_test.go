package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s %v", args, b, err)
	}
	return strings.TrimSpace(string(b))
}
func commit(t *testing.T, dir, file, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", file)
	gitCmd(t, dir, "-c", "commit.gpgSign=false", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.com", "commit", "-qm", value)
}
func fixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	gitCmd(t, root, "init", "--bare", "-b", "main", remote)
	a := filepath.Join(root, "a")
	gitCmd(t, root, "clone", remote, a)
	commit(t, a, "tracked", "initial")
	gitCmd(t, a, "push", "-u", "origin", "main")
	b := filepath.Join(root, "b")
	gitCmd(t, root, "clone", remote, b)
	return a, b, remote
}

func TestGitHubIdentityParsing(t *testing.T) {
	for _, origin := range []string{"https://github.com/Cohesian-Tools/Workit.git", "git@github.com:cohesian-tools/workit.git", "ssh://git@github.com/cohesian-tools/workit"} {
		got, err := ParseOrigin(origin)
		if err != nil || got != "cohesian-tools/workit" {
			t.Fatal(got, err)
		}
	}
	for _, origin := range []string{"/tmp/local.git", "https://evil.example/cohesian-tools/workit", "https://secret@github.com/cohesian-tools/workit", "https://github.com/cohesian-tools/workit?token=secret", "ssh://root@github.com/cohesian-tools/workit", "https://github.com/a/../escape"} {
		if _, err := ParseOrigin(origin); err == nil {
			t.Fatal("accepted", origin)
		}
	}
}

func TestCachedDivergenceAndLocalChanges(t *testing.T) {
	a, b, _ := fixture(t)
	commit(t, b, "remote", "remote change")
	gitCmd(t, b, "push")
	commit(t, a, "local", "local change")
	gitCmd(t, a, "fetch", "origin")
	os.WriteFile(filepath.Join(a, "staged"), []byte("stage"), 0644)
	gitCmd(t, a, "add", "staged")
	os.WriteFile(filepath.Join(a, "tracked"), []byte("unstaged"), 0644)
	os.WriteFile(filepath.Join(a, "untracked"), []byte("new"), 0644)
	state, err := (Client{}).Inspect(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if state.Branch != "main" || state.Upstream != "origin/main" || state.Ahead != 1 || state.Behind != 1 || state.Staged != 1 || state.Unstaged != 1 || state.Untracked != 1 {
		t.Fatalf("unexpected status %+v", state)
	}
}

func TestFastForwardPullNeverSwitchesOrDiscardsChanges(t *testing.T) {
	a, b, _ := fixture(t)
	ctx := context.Background()
	client := Client{}
	commit(t, b, "remote", "advance")
	gitCmd(t, b, "push")
	if _, err := client.Run(ctx, a, "pull", []string{"main", "main"}); err != nil {
		t.Fatal(err)
	}
	if gitCmd(t, a, "rev-parse", "HEAD") != gitCmd(t, b, "rev-parse", "HEAD") {
		t.Fatal("did not fast-forward")
	}
	gitCmd(t, a, "switch", "-c", "other")
	if _, err := client.Run(ctx, a, "pull", []string{"main", "main"}); err == nil {
		t.Fatal("switched branches implicitly")
	}
	if gitCmd(t, a, "branch", "--show-current") != "other" {
		t.Fatal("branch changed")
	}
	gitCmd(t, a, "switch", "main")
	os.WriteFile(filepath.Join(a, "untracked"), []byte("keep"), 0644)
	if _, err := client.Run(ctx, a, "pull", []string{"main", "main"}); err == nil {
		t.Fatal("pulled dirty checkout")
	}
	os.Remove(filepath.Join(a, "untracked"))
	commit(t, a, "local", "local fork")
	commit(t, b, "remote", "remote fork")
	gitCmd(t, b, "push")
	before := gitCmd(t, a, "rev-parse", "HEAD")
	if _, err := client.Run(ctx, a, "pull", []string{"main", "main"}); err == nil {
		t.Fatal("divergent pull was allowed")
	}
	if gitCmd(t, a, "rev-parse", "HEAD") != before {
		t.Fatal("rewrote local branch")
	}
	remoteBefore := gitCmd(t, b, "rev-parse", "HEAD")
	if _, err := client.Run(ctx, a, "push", nil); err == nil {
		t.Fatal("non-fast-forward push was forced")
	}
	if gitCmd(t, a, "ls-remote", "origin", "refs/heads/main") != remoteBefore+"\trefs/heads/main" {
		t.Fatal("remote branch changed")
	}
}

func TestCloneInstallationCollisionsAndFailedCleanup(t *testing.T) {
	a, _, remote := fixture(t)
	parent := t.TempDir()
	client := Client{cloneURL: func(string, string) string { return remote }}
	destination := filepath.Join(parent, "repo")
	if err := client.Clone(context.Background(), destination, "cohesian-tools/workit", "ssh"); err != nil {
		t.Fatal(err)
	}
	if gitCmd(t, destination, "rev-parse", "HEAD") != gitCmd(t, a, "rev-parse", "HEAD") {
		t.Fatal("clone content mismatch")
	}
	if err := client.Clone(context.Background(), destination, "cohesian-tools/workit", "ssh"); err == nil {
		t.Fatal("overwrote checkout")
	}
	empty := filepath.Join(parent, "empty")
	os.Mkdir(empty, 0755)
	if err := client.Clone(context.Background(), empty, "cohesian-tools/workit", "ssh"); err == nil {
		t.Fatal("replaced existing empty directory")
	}
	client.cloneURL = func(string, string) string { return filepath.Join(parent, "missing.git") }
	if err := client.Clone(context.Background(), filepath.Join(parent, "failed"), "cohesian-tools/workit", "ssh"); err == nil {
		t.Fatal("expected clone failure")
	}
	entries, _ := os.ReadDir(parent)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".workit-clone-") || entry.Name() == "failed" {
			t.Fatal("failed clone left staging content")
		}
	}
}

func TestGitEnvironmentCannotRedirectRepository(t *testing.T) {
	a, b, _ := fixture(t)
	gitCmd(t, b, "switch", "-c", "foreign")
	t.Setenv("GIT_DIR", filepath.Join(b, ".git"))
	t.Setenv("GIT_WORK_TREE", b)
	state, err := (Client{}).Inspect(context.Background(), a)
	if err != nil || state.Branch != "main" {
		t.Fatal("inherited Git environment redirected operation", state, err)
	}
}

func TestPushRefusesDifferentPushIdentity(t *testing.T) {
	a, _, _ := fixture(t)
	gitCmd(t, a, "config", "remote.origin.pushurl", "https://github.com/someone/another.git")
	if err := (Client{}).Preflight(context.Background(), a, "push", nil); err == nil {
		t.Fatal("different push target accepted")
	}
}

func TestPushUsesOnlyExplicitUpstream(t *testing.T) {
	a, _, _ := fixture(t)
	gitCmd(t, a, "switch", "-c", "side")
	gitCmd(t, a, "push", "origin", "side")
	sideBefore := gitCmd(t, a, "rev-parse", "HEAD")
	commit(t, a, "side", "unpublished side commit")
	gitCmd(t, a, "switch", "main")
	commit(t, a, "main", "publish main")
	gitCmd(t, a, "config", "push.default", "matching")
	gitCmd(t, a, "config", "remote.origin.push", "+refs/heads/*:refs/heads/*")
	if _, err := (Client{}).Run(context.Background(), a, "push", nil); err != nil {
		t.Fatal(err)
	}
	if gitCmd(t, a, "ls-remote", "origin", "refs/heads/side") != sideBefore+"\trefs/heads/side" {
		t.Fatal("pushed an unselected branch")
	}
	if gitCmd(t, a, "ls-remote", "origin", "refs/heads/main") != gitCmd(t, a, "rev-parse", "HEAD")+"\trefs/heads/main" {
		t.Fatal("upstream was not updated")
	}
}
