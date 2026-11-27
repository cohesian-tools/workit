package graph

import "testing"

func sample() Index {
	return Index{Version: 1, Protocol: "ssh", Nodes: []Node{{Path: ".", Kind: Root}, {Path: "a", Kind: Namespace}, {Path: "a/tool", Kind: Workstation, Repository: "cohesian-tools/workit"}, {Path: "abc", Kind: Namespace}}}
}

func TestTreeValidationAndComponentScope(t *testing.T) {
	i := sample()
	if err := i.Validate(); err != nil {
		t.Fatal(err)
	}
	for cwd, want := range map[string]string{".": ".", "a": "a", "a/new/subdir": "a", "a/tool/src": "a/tool", "abc/subdir": "abc"} {
		if got := i.Select(cwd).Path; got != want {
			t.Errorf("select %s: %s, want %s", cwd, got, want)
		}
	}
	if Within("a", "abc") {
		t.Fatal("scope uses string prefixes instead of components")
	}
	if len(i.Scope(i.Select("a"))) != 2 {
		t.Fatal("scope leaked into sibling")
	}
}

func TestInvalidTrees(t *testing.T) {
	for name, change := range map[string]func(*Index){
		"version":        func(i *Index) { i.Version = 2 },
		"root":           func(i *Index) { i.Nodes = i.Nodes[1:] },
		"orphan":         func(i *Index) { i.Nodes = append(i.Nodes, Node{Path: "missing/child", Kind: Namespace}) },
		"leaf child":     func(i *Index) { i.Nodes = append(i.Nodes, Node{Path: "a/tool/child", Kind: Namespace}) },
		"case collision": func(i *Index) { i.Nodes = append(i.Nodes, Node{Path: "A", Kind: Namespace}) },
		"traversal":      func(i *Index) { i.Nodes[1].Path = "../escape" },
		"metadata":       func(i *Index) { i.Nodes[1].Path = ".void" },
		"identity":       func(i *Index) { i.Nodes[2].Repository = "someone/repo.git" },
		"root kind":      func(i *Index) { i.Nodes[0].Kind = Namespace },
	} {
		t.Run(name, func(t *testing.T) {
			i := sample()
			change(&i)
			if i.Validate() == nil {
				t.Fatal("accepted invalid tree")
			}
		})
	}
}

func TestExclusionsAreConfinedAndCannotHideRegisteredNodes(t *testing.T) {
	i := sample()
	i.Exclude = []string{"scripts"}
	if err := i.Validate(); err != nil {
		t.Fatal(err)
	}
	if !i.Excluded("scripts/tests") || i.Excluded("scripts-extra") {
		t.Fatal("exclusion boundary is not component-based")
	}
	for _, exclude := range [][]string{{"."}, {"../outside"}, {"scripts", "Scripts"}, {"a"}, {"a/tool"}} {
		i.Exclude = exclude
		if i.Validate() == nil {
			t.Fatal("accepted exclusion", exclude)
		}
	}
}
