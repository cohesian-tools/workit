package workdir

import (
	"context"
	"github.com/cohesian-tools/workit/internal/graph"
	"os"
	"path/filepath"
	"testing"
)

type identifier struct{ calls []string }

func (i *identifier) Identity(_ context.Context, directory string) (string, error) {
	i.calls = append(i.calls, directory)
	return "cohesian-tools/" + filepath.Base(directory), nil
}

func TestDiscoveryStopsAtGitFilesAndDirectoriesAndExcludesMetadata(t *testing.T) {
	root := t.TempDir()
	git := &identifier{}
	d := Disk{Git: git}
	for _, directory := range []string{"tools/one/.git", "tools/one/src/nested", "tools/two", ".void/recovery", ".hidden", "node_modules/dependency", "vendor/dependency"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(root, "tools", "two", ".git"), []byte("gitdir: elsewhere"), 0644)
	nodes, err := d.Discover(context.Background(), root, graph.Node{Path: ".", Kind: graph.Root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 4 || len(git.calls) != 2 {
		t.Fatalf("crossed a boundary or included excluded dirs: %+v", nodes)
	}
	if nodes[2].Kind != graph.Workstation || nodes[3].Kind != graph.Workstation {
		t.Fatal(nodes)
	}
}

func TestResolveRejectsPathAndCaseEscapes(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "Tools"), 0755)
	for _, relative := range []string{"../outside", "tools", "/absolute", ".void/item"} {
		if _, _, err := Resolve(root, relative); err == nil {
			t.Fatal("accepted path", relative)
		}
	}
	os.Symlink(t.TempDir(), filepath.Join(root, "link"))
	if _, _, err := Resolve(root, "link/nested"); err == nil {
		t.Fatal("followed directory symlink")
	}
}

func TestBareRepositoryDoesNotBecomeNamespaceTree(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "remote.git")
	os.MkdirAll(filepath.Join(base, "objects"), 0755)
	os.MkdirAll(filepath.Join(base, "refs"), 0755)
	os.WriteFile(filepath.Join(base, "HEAD"), []byte("ref: refs/heads/main\n"), 0644)
	os.WriteFile(filepath.Join(base, "config"), []byte("[core]\n bare = true\n"), 0644)
	if _, err := (Disk{Git: &identifier{}}).Discover(context.Background(), root, graph.Node{Path: ".", Kind: graph.Root}, nil); err == nil {
		t.Fatal("discovered inside a bare repository")
	}
}

func TestExclusionsPreventTraversalNotJustRegistration(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "scripts", "nested"), 0755)
	os.WriteFile(filepath.Join(root, "scripts", "nested", ".workit.toml"), []byte("invalid nested workspace"), 0644)
	os.Mkdir(filepath.Join(root, "scripts-extra"), 0755)
	nodes, err := (Disk{Git: &identifier{}}).Discover(context.Background(), root, graph.Node{Path: ".", Kind: graph.Root}, []string{"scripts"})
	if err != nil || len(nodes) != 2 || nodes[1].Path != "scripts-extra" {
		t.Fatal(nodes, err)
	}
}
