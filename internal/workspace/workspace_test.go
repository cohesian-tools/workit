package workspace_test

import (
	"context"
	"errors"
	"github.com/cohesian-tools/workit/internal/graph"
	"github.com/cohesian-tools/workit/internal/index"
	"github.com/cohesian-tools/workit/internal/void"
	"github.com/cohesian-tools/workit/internal/workdir"
	"github.com/cohesian-tools/workit/internal/workspace"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var ctx = context.Background()
var yes = workspace.Options{Approve: func(workspace.Plan) error { return nil }}

type fakeGit struct {
	calls                    []string
	failClone, failPreflight string
}

func (f *fakeGit) Identity(_ context.Context, p string) (string, error) {
	b, e := os.ReadFile(filepath.Join(p, ".git", "origin"))
	return string(b), e
}
func (f *fakeGit) Clone(_ context.Context, p, repo, protocol string) error {
	if strings.Contains(p, f.failClone) && f.failClone != "" {
		return errors.New("clone failed")
	}
	if err := os.Mkdir(p, 0755); err != nil {
		return err
	}
	os.Mkdir(filepath.Join(p, ".git"), 0755)
	os.WriteFile(filepath.Join(p, ".git", "origin"), []byte(repo), 0644)
	f.calls = append(f.calls, "clone:"+protocol+":"+repo)
	return nil
}
func (f *fakeGit) Inspect(context.Context, string) (workspace.RepoState, error) {
	return workspace.RepoState{Branch: "main"}, nil
}
func (f *fakeGit) Preflight(_ context.Context, p, op string, _ []string) error {
	if strings.Contains(p, f.failPreflight) && f.failPreflight != "" {
		return errors.New("preflight failed")
	}
	return nil
}
func (f *fakeGit) Run(_ context.Context, p, op string, _ []string) (string, error) {
	f.calls = append(f.calls, op+":"+filepath.Base(p))
	return "", nil
}

func fixture(t *testing.T) (string, workspace.Service, *fakeGit) {
	t.Helper()
	root := t.TempDir()
	g := &fakeGit{}
	s := workspace.Service{Store: index.Store{}, Disk: workdir.Disk{Git: g}, Git: g, Void: void.Vault{}}
	if err := s.Init(root, "ssh"); err != nil {
		t.Fatal(err)
	}
	return root, s, g
}
func load(t *testing.T, s workspace.Service, root string) graph.Index {
	t.Helper()
	i, _, err := s.Store.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return i
}
func add(t *testing.T, s workspace.Service, dir, arg string) {
	t.Helper()
	if _, _, err := s.Add(ctx, dir, arg, workspace.Options{}); err != nil {
		t.Fatal(err)
	}
}

func TestNodeLifecycleScopeAndVoid(t *testing.T) {
	root, s, _ := fixture(t)
	add(t, s, root, "tools")
	tools := filepath.Join(root, "tools")
	add(t, s, tools, "cohesian-tools/workit.git")
	repo := filepath.Join(tools, "workit")
	os.Mkdir(filepath.Join(repo, "src"), 0755)
	os.WriteFile(filepath.Join(repo, "notes.txt"), []byte("uncommitted work"), 0644)
	p, _, err := s.Repositories(ctx, filepath.Join(repo, "src"), "status", nil, workspace.Options{})
	if err != nil || p.Selected.Path != "tools/workit" || len(p.Steps) != 1 {
		t.Fatalf("scope: %+v %v", p, err)
	}
	if _, _, err := s.Remove(ctx, root, "tools", workspace.Options{}); err == nil {
		t.Fatal("removed without confirmation")
	}
	_, r, err := s.Remove(ctx, root, "tools", yes)
	if err != nil {
		t.Fatal(err)
	}
	if len(load(t, s, root).Nodes) != 1 {
		t.Fatal("subtree remained registered")
	}
	payload := filepath.Join(r[0].Output, "payload", "tools", "workit", "notes.txt")
	if b, e := os.ReadFile(payload); e != nil || string(b) != "uncommitted work" {
		t.Fatal("lost payload", e)
	}
	if _, _, err := s.Clean(root, workspace.Options{}); err == nil {
		t.Fatal("clean without confirmation")
	}
	if _, _, err := s.Clean(root, yes); err != nil {
		t.Fatal(err)
	}
	entries, err := s.Void.Entries(root)
	if err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
}

func TestReconcileSidesAndNoImplicitRegistration(t *testing.T) {
	root, s, g := fixture(t)
	os.Mkdir(filepath.Join(root, "tools"), 0755)
	if err := g.Clone(ctx, filepath.Join(root, "tools", "one"), "cohesian-tools/one", "ssh"); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(root, "tools", "one", "nested", "fake-repository"), 0755)
	p, _, err := s.Sync(ctx, filepath.Join(root, "tools"), "index", "add", workspace.Options{DryRun: true})
	if err != nil || p.Selected.Path != "." || len(p.Steps) != 2 {
		t.Fatal(p, err)
	}
	if len(load(t, s, root).Nodes) != 1 {
		t.Fatal("dry run registered nodes")
	}
	if _, _, err := s.Sync(ctx, root, "index", "add", workspace.Options{}); err != nil {
		t.Fatal(err)
	}
	if len(load(t, s, root).Nodes) != 3 {
		t.Fatal("discovery crossed repository boundary")
	}
	os.RemoveAll(filepath.Join(root, "tools", "one"))
	if _, _, err := s.Sync(ctx, root, "workdir", "add", workspace.Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tools", "one", ".git")); err != nil {
		t.Fatal(err)
	}
	os.Mkdir(filepath.Join(root, "extra"), 0755)
	os.WriteFile(filepath.Join(root, "extra", "valuable"), []byte("keep"), 0644)
	if _, _, err := s.Sync(ctx, root, "workdir", "remove", yes); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tools", "one")); err != nil {
		t.Fatal("removed matched repository")
	}
	os.RemoveAll(filepath.Join(root, "tools"))
	if _, _, err := s.Sync(ctx, root, "index", "prune", workspace.Options{}); err != nil {
		t.Fatal(err)
	}
	if len(load(t, s, root).Nodes) != 1 {
		t.Fatal("prune did not retain ancestor closure")
	}
}

func TestConflictAndIncompleteScanNeverPrune(t *testing.T) {
	root, s, _ := fixture(t)
	add(t, s, root, "cohesian-tools/one.git")
	add(t, s, root, "cohesian-tools/missing.git")
	os.RemoveAll(filepath.Join(root, "missing"))
	before, _ := os.ReadFile(filepath.Join(root, index.Filename))
	os.WriteFile(filepath.Join(root, "one", ".git", "origin"), []byte("other/one"), 0644)
	if _, _, err := s.Sync(ctx, root, "index", "prune", workspace.Options{}); err == nil {
		t.Fatal("origin conflict accepted")
	}
	after, _ := os.ReadFile(filepath.Join(root, index.Filename))
	if string(before) != string(after) {
		t.Fatal("conflict changed index")
	}
	os.WriteFile(filepath.Join(root, "one", ".git", "origin"), []byte("cohesian-tools/one"), 0644)
	os.Mkdir(filepath.Join(root, "nested"), 0755)
	os.WriteFile(filepath.Join(root, "nested", index.Filename), []byte("invalid"), 0644)
	if _, _, err := s.Sync(ctx, root, "index", "prune", workspace.Options{}); err == nil {
		t.Fatal("pruned after incomplete nested-workspace discovery")
	}
	after, _ = os.ReadFile(filepath.Join(root, index.Filename))
	if string(before) != string(after) {
		t.Fatal("incomplete scan changed index")
	}
}

func TestProtocolClonesFailureAndForestPreflight(t *testing.T) {
	root, s, g := fixture(t)
	if err := s.Protocol(root, "https"); err != nil {
		t.Fatal(err)
	}
	add(t, s, root, "cohesian-tools/one.git")
	add(t, s, root, "cohesian-tools/two.git")
	if !strings.HasPrefix(g.calls[0], "clone:https:") {
		t.Fatal(g.calls)
	}
	g.calls = nil
	g.failPreflight = "two"
	if _, _, err := s.Repositories(ctx, root, "fetch", nil, workspace.Options{}); err == nil {
		t.Fatal("preflight succeeded")
	}
	if len(g.calls) != 0 {
		t.Fatal("executed before validating all targets")
	}
	g.failClone = "bad"
	before, _ := os.ReadFile(filepath.Join(root, index.Filename))
	if _, _, err := s.Add(ctx, root, "cohesian-tools/bad.git", workspace.Options{}); err == nil {
		t.Fatal("clone unexpectedly succeeded")
	}
	after, _ := os.ReadFile(filepath.Join(root, index.Filename))
	if string(before) != string(after) {
		t.Fatal("failed clone changed index")
	}
}

type failingStore struct{ workspace.Store }

func (failingStore) Save(string, graph.Index, []byte) error {
	return errors.New("injected write failure")
}

func TestCrossLayerRecoveryOnIndexFailure(t *testing.T) {
	root, s, _ := fixture(t)
	add(t, s, root, "tools")
	os.WriteFile(filepath.Join(root, "tools", "valuable"), []byte("preserve"), 0644)
	s.Store = failingStore{s.Store}
	if _, _, err := s.Remove(ctx, root, "tools", yes); err == nil {
		t.Fatal("expected injected failure")
	}
	if b, e := os.ReadFile(filepath.Join(root, "tools", "valuable")); e != nil || string(b) != "preserve" {
		t.Fatal("quarantine not rolled back", e)
	}
	if len(load(t, s, root).Nodes) != 2 {
		t.Fatal("failure unregistered node")
	}
	if _, _, err := s.Add(ctx, root, "new", workspace.Options{}); err == nil {
		t.Fatal("expected write failure")
	}
	if _, err := os.Stat(filepath.Join(root, "new")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed addition left an unmanaged live path")
	}
	entries, err := s.Void.Entries(root)
	if err != nil || len(entries) != 1 {
		t.Fatal("missing recovery entry", entries, err)
	}
}

func TestSymlinkAndTypeConflicts(t *testing.T) {
	root, s, _ := fixture(t)
	add(t, s, root, "tools")
	os.Remove(filepath.Join(root, "tools"))
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "keep"), []byte("outside"), 0644)
	os.Symlink(outside, filepath.Join(root, "tools"))
	if _, _, err := s.Sync(ctx, root, "index", "prune", workspace.Options{}); err == nil {
		t.Fatal("registered symlink treated as missing")
	}
	if _, _, err := s.Remove(ctx, root, "tools", yes); err == nil {
		t.Fatal("quarantined symlink")
	}
	os.Remove(filepath.Join(root, "tools"))
	os.Mkdir(filepath.Join(root, "tools"), 0755)
	os.Mkdir(filepath.Join(root, "tools", ".git"), 0755)
	if _, _, err := s.Add(ctx, filepath.Join(root, "tools"), "child", workspace.Options{}); err == nil {
		t.Fatal("added under a namespace converted to a repository")
	}
	if _, err := os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Fatal("outside tree changed")
	}
}

func TestVoidIsPhysicallyRootOnlyAndMetadataIsNotAScope(t *testing.T) {
	root, s, _ := fixture(t)
	add(t, s, root, "tools")
	for _, directory := range []string{filepath.Join(root, "tools"), filepath.Join(root, "unregistered")} {
		os.MkdirAll(directory, 0755)
		if _, _, err := s.Clean(directory, yes); err == nil {
			t.Fatal("void clean outside root")
		}
	}
	if _, _, err := s.Remove(ctx, root, "tools", yes); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Repositories(ctx, filepath.Join(root, ".void"), "status", nil, workspace.Options{}); err == nil {
		t.Fatal("metadata selected as a scope")
	}
}

type failedQuarantine struct{ workspace.Quarantine }

func (failedQuarantine) Move(string, graph.Node, []byte) (workspace.Receipt, error) {
	return workspace.Receipt{}, errors.New("injected quarantine failure")
}

func TestQuarantineFailureDoesNotUnregisterOrDelete(t *testing.T) {
	root, s, _ := fixture(t)
	add(t, s, root, "tools")
	s.Void = failedQuarantine{s.Void}
	before, _ := os.ReadFile(filepath.Join(root, index.Filename))
	if _, _, err := s.Remove(ctx, root, "tools", yes); err == nil {
		t.Fatal("expected quarantine failure")
	}
	after, _ := os.ReadFile(filepath.Join(root, index.Filename))
	if string(before) != string(after) {
		t.Fatal("failed quarantine changed index")
	}
	if _, err := os.Stat(filepath.Join(root, "tools")); err != nil {
		t.Fatal("failed quarantine lost original")
	}
}

func TestExcludedManagementDirectoriesNeverEnterSyncPlans(t *testing.T) {
	root, s, _ := fixture(t)
	i, raw, err := s.Store.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	i.Exclude = []string{"scripts", "tests"}
	if err := s.Store.Save(root, i, raw); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(root, "scripts", "test"), 0755)
	os.WriteFile(filepath.Join(root, "scripts", "test", ".workit.toml"), []byte("fixture"), 0644)
	plan, _, err := s.Sync(ctx, root, "index", "add", workspace.Options{DryRun: true})
	if err != nil || len(plan.Steps) != 0 {
		t.Fatal(plan, err)
	}
	if _, _, err := s.Sync(ctx, root, "workdir", "remove", yes); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "scripts", "test", ".workit.toml")); err != nil {
		t.Fatal("management files were quarantined")
	}
	if _, _, err := s.Add(ctx, root, "scripts", workspace.Options{}); err == nil {
		t.Fatal("registered excluded path")
	}
	if _, _, err := s.Repositories(ctx, filepath.Join(root, "scripts"), "status", nil, workspace.Options{}); err == nil {
		t.Fatal("excluded directory selected root implicitly")
	}
}
