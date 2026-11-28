package cli_test

import (
	"bytes"
	"context"
	"errors"
	"github.com/cohesian-tools/workit/internal/cli"
	gitadapter "github.com/cohesian-tools/workit/internal/git"
	"github.com/cohesian-tools/workit/internal/index"
	"github.com/cohesian-tools/workit/internal/void"
	"github.com/cohesian-tools/workit/internal/workdir"
	"github.com/cohesian-tools/workit/internal/workspace"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func run(t *testing.T, dir, input string, args ...string) (string, error) {
	t.Helper()
	git := gitadapter.Client{}
	s := workspace.Service{Store: index.Store{}, Disk: workdir.Disk{Git: git}, Git: git, Void: void.Vault{}}
	cmd := cli.New(s, "test")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(input))
	cmd.SetArgs(append([]string{"-C", dir}, args...))
	err := cmd.Execute()
	return out.String(), err
}

func TestCLIHelpAndFilesystemFlow(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{{"--help"}, {"node", "--help"}, {"sync", "workdir", "--help"}, {"repo", "pull", "--help"}, {"void", "clean", "--help"}} {
		if _, err := run(t, root, "", args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := run(t, root, "", "init"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, root, "", "init"); err == nil {
		t.Fatal("nested init allowed")
	}
	before, _ := os.ReadFile(filepath.Join(root, index.Filename))
	out, err := run(t, root, "", "node", "add", "tools", "--dry-run")
	if err != nil || !strings.Contains(out, "└── tools/ · add") {
		t.Fatal(out, err)
	}
	after, _ := os.ReadFile(filepath.Join(root, index.Filename))
	if !bytes.Equal(before, after) {
		t.Fatal("dry run wrote index")
	}
	if _, err := run(t, root, "", "node", "add", "tools"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, filepath.Join(root, "tools"), "", "config", "protocol", "https"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, root, "n\n", "node", "remove", "tools"); err == nil {
		t.Fatal("negative confirmation applied")
	}
	if _, err := os.Stat(filepath.Join(root, "tools")); err != nil {
		t.Fatal("negative confirmation changed directory")
	}
	if _, err := run(t, root, "", "node", "remove", "tools", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, root, "", "void", "clean"); err == nil {
		t.Fatal("noninteractive deletion without --yes")
	}
	if _, err := run(t, root, "", "void", "clean", "--yes"); err != nil {
		t.Fatal(err)
	}
}

func TestCommandArgumentsAndEmptyGroupsDoNotMutate(t *testing.T) {
	root := t.TempDir()
	run(t, root, "", "init")
	before, _ := os.ReadFile(filepath.Join(root, index.Filename))
	for _, args := range [][]string{{"sync"}, {"sync", "index"}, {"sync", "workdir"}, {"repo"}} {
		if _, err := run(t, root, "", args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"node", "add", "../escape"}, {"node", "remove", "."}, {"repo", "pull", "main"}, {"sync", "all"}, {"completion"}, {"config", "protocol", "ftp"}} {
		if _, err := run(t, root, "", args...); err == nil {
			t.Fatal("accepted invalid command", args)
		}
	}
	after, _ := os.ReadFile(filepath.Join(root, index.Filename))
	if !bytes.Equal(before, after) {
		t.Fatal("invalid commands changed index")
	}
}

type blockedInput struct{ entered, release chan struct{} }

func (b blockedInput) Read([]byte) (int, error) { close(b.entered); <-b.release; return 0, io.EOF }

func TestCancelledConfirmationReleasesLockWithoutMovingContent(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, root, "", "init"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, root, "", "node", "add", "tools"); err != nil {
		t.Fatal(err)
	}
	git := gitadapter.Client{}
	service := workspace.Service{Store: index.Store{}, Disk: workdir.Disk{Git: git}, Git: git, Void: void.Vault{}}
	cmd := cli.New(service, "test")
	cmd.SetArgs([]string{"-C", root, "node", "remove", "tools"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	input := blockedInput{make(chan struct{}), make(chan struct{})}
	defer close(input.release)
	cmd.SetIn(input)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()
	select {
	case <-input.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("confirmation not reached")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not interrupt confirmation")
	}
	if _, err := os.Stat(filepath.Join(root, "tools")); err != nil {
		t.Fatal("cancellation moved content")
	}
	if _, err := os.Stat(filepath.Join(root, ".workit.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("lock not released")
	}
}

func TestSyncStatusPrintsThreeScopedTrees(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"node", "add", "tools"},
		{"node", "add", "unrelated"},
	} {
		if _, err := run(t, root, "", args...); err != nil {
			t.Fatal(err)
		}
	}
	tools := filepath.Join(root, "tools")
	for _, name := range []string{"common", "missing"} {
		if _, err := run(t, tools, "", "node", "add", name); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(tools, "missing")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tools, "common", "extra", "child"), 0755); err != nil {
		t.Fatal(err)
	}

	indexPath := filepath.Join(root, index.Filename)
	before, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	// A read-only comparison also works when the write lock exists.
	if err := os.Mkdir(filepath.Join(root, ".workit.lock"), 0755); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, tools, "", "sync", "status")
	want := "✅ Inner · index & workdir\n./tools/ ✅\n└── common/ ✅\n\n" +
		"☑️ Outer · index only\n./tools/\n└── missing/ ☑️\n\n" +
		"⚠️ Outer · workdir only\n./tools/\n└── common/\n    └── extra/ ⚠️\n        └── child/ ⚠️\n"
	if err != nil || out != want {
		t.Fatalf("got:\n%s\nwant:\n%s\nerr: %v", out, want, err)
	}
	after, err := os.ReadFile(indexPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("status changed the index", err)
	}
	if err := os.Mkdir(filepath.Join(tools, "common", ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, tools, "", "sync", "status"); err == nil || out != "" {
		t.Fatalf("expected an error without a partial report, got %q, %v", out, err)
	}
}

func TestHelpPrintsCommandTree(t *testing.T) {
	out, err := run(t, t.TempDir(), "", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"├── 📁 sync",
		"│   ├── ▸ status · Show matching",
		"│       └── ▸ remove",
		"--directory",
		"--version",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "completion") {
		t.Fatalf("completion is still present in help:\n%s", out)
	}
}
