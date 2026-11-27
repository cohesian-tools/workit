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
	if err != nil || !strings.Contains(out, "add  tools") {
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
	for _, args := range [][]string{{"node", "add", "../escape"}, {"node", "remove", "."}, {"repo", "pull", "main"}, {"sync", "all"}, {"config", "protocol", "ftp"}} {
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
