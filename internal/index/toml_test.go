package index

import (
	"github.com/cohesian-tools/workit/internal/graph"
	"os"
	"path/filepath"
	"testing"
)

func TestStrictRoundtripLockAndChangedIndex(t *testing.T) {
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	s := Store{}
	i := graph.Index{Version: 1, Protocol: "ssh", Nodes: []graph.Node{{Path: ".", Kind: graph.Root}}}
	if err := s.Create(root, i); err != nil {
		t.Fatal(err)
	}
	if s.Create(root, i) == nil {
		t.Fatal("overwrote existing index")
	}
	_, raw, err := s.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := s.Lock(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lock(root); err == nil {
		t.Fatal("parallel writer acquired lock")
	}
	unlock()
	i.Protocol = "https"
	if err := s.Save(root, i, raw); err != nil {
		t.Fatal(err)
	}
	if s.Save(root, i, raw) == nil {
		t.Fatal("stale write overwrote newer data")
	}
	_, next, _ := s.Load(root)
	if err := os.WriteFile(filepath.Join(root, Filename), append(next, []byte("unknown = true\n")...), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Load(root); err == nil {
		t.Fatal("unknown TOML field accepted")
	}
}

func TestNearestWorkspaceAndSymlinkIndex(t *testing.T) {
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	s := Store{}
	i := graph.Index{Version: 1, Protocol: "ssh", Nodes: []graph.Node{{Path: ".", Kind: graph.Root}}}
	if err := s.Create(root, i); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "a", "b")
	os.MkdirAll(child, 0755)
	found, err := s.Find(child)
	if err != nil || found != root {
		t.Fatalf("%s %v", found, err)
	}
	link := filepath.Join(root, "a", Filename)
	if err := os.Symlink(filepath.Join(root, Filename), link); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Find(child); err == nil {
		t.Fatal("followed symlinked index")
	}
}
