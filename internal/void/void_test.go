package void

import (
	"github.com/cohesian-tools/workit/internal/graph"
	"os"
	"path/filepath"
	"testing"
)

func TestRefuseUnmanagedVoidAndDoNotFollowPayloadSymlinks(t *testing.T) {
	root := t.TempDir()
	v := Vault{}
	os.Mkdir(filepath.Join(root, ".void"), 0755)
	file := filepath.Join(root, ".void", "valuable")
	os.WriteFile(file, []byte("keep"), 0644)
	if _, err := v.Entries(root); err == nil {
		t.Fatal("accepted unmanaged void")
	}
	if v.Clean(root, nil) == nil {
		t.Fatal("cleaned unmanaged void")
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("unmanaged content lost")
	}
	root = t.TempDir()
	outside := t.TempDir()
	valuable := filepath.Join(outside, "keep")
	os.WriteFile(valuable, []byte("keep"), 0644)
	os.Mkdir(filepath.Join(root, "tools"), 0755)
	os.Symlink(outside, filepath.Join(root, "tools", "outside"))
	receipt, err := v.Move(root, graph.Node{Path: "tools", Kind: graph.Namespace}, []byte("before"))
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(receipt.Entry, "index-before.toml")); err != nil || string(b) != "before" {
		t.Fatal("recovery snapshot missing")
	}
	entries, err := v.Entries(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Clean(root, entries); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(valuable); err != nil {
		t.Fatal("clean followed a payload symlink")
	}
}
