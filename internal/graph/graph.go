// Package graph defines Workit's filesystem-independent rooted tree.
package graph

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

type Kind string

const (
	Root        Kind = "r"
	Namespace   Kind = "n"
	Workstation Kind = "w"
)

type Node struct {
	Path       string `toml:"path" json:"path"`
	Kind       Kind   `toml:"kind" json:"kind"`
	Repository string `toml:"repository,omitempty" json:"repository,omitempty"`
}

type Index struct {
	Version  int      `toml:"version"`
	Protocol string   `toml:"protocol"`
	Exclude  []string `toml:"exclude,omitempty"`
	Nodes    []Node   `toml:"nodes"`
}

var name = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
var owner = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)

func ValidName(s string) bool {
	return name.MatchString(s) && !strings.HasSuffix(s, ".") && !strings.HasSuffix(s, " ") && s != "node_modules" && s != "vendor"
}

func Repository(s string) (string, error) {
	parts := strings.Split(s, "/")
	if len(parts) != 2 || !owner.MatchString(parts[0]) || strings.HasSuffix(parts[0], "-") || !ValidName(parts[1]) || strings.HasSuffix(parts[1], ".git") {
		return "", fmt.Errorf("expected GitHub owner/repository identity, got %q", s)
	}
	return strings.ToLower(s), nil
}

func ValidPath(s string) bool {
	if s == "." {
		return true
	}
	if s == "" || path.IsAbs(s) || path.Clean(s) != s || strings.Contains(s, "\\") {
		return false
	}
	for _, component := range strings.Split(s, "/") {
		if !ValidName(component) {
			return false
		}
	}
	return true
}

func Within(parent, child string) bool {
	return parent == "." || parent == child || strings.HasPrefix(child, parent+"/")
}

func (i Index) ByPath() map[string]Node {
	nodes := make(map[string]Node, len(i.Nodes))
	for _, node := range i.Nodes {
		nodes[node.Path] = node
	}
	return nodes
}

func Sort(nodes []Node) {
	sort.Slice(nodes, func(a, b int) bool {
		if depth(nodes[a].Path) != depth(nodes[b].Path) {
			return depth(nodes[a].Path) < depth(nodes[b].Path)
		}
		return nodes[a].Path < nodes[b].Path
	})
}

func depth(p string) int {
	if p == "." {
		return 0
	}
	return strings.Count(p, "/") + 1
}

func (i Index) Excluded(relative string) bool {
	for _, excluded := range i.Exclude {
		if Within(strings.ToLower(excluded), strings.ToLower(relative)) {
			return true
		}
	}
	return false
}

func (i Index) Validate() error {
	if i.Version != 1 {
		return fmt.Errorf("unsupported index version %d (expected 1)", i.Version)
	}
	if i.Protocol != "ssh" && i.Protocol != "https" {
		return fmt.Errorf("protocol must be ssh or https")
	}
	excluded := make(map[string]bool)
	for _, p := range i.Exclude {
		key := strings.ToLower(p)
		if p == "." || !ValidPath(p) || excluded[key] {
			return fmt.Errorf("invalid or duplicate exclusion %q", p)
		}
		excluded[key] = true
	}
	nodes, folded := make(map[string]Node), make(map[string]bool)
	for _, node := range i.Nodes {
		if !ValidPath(node.Path) || i.Excluded(node.Path) {
			return fmt.Errorf("invalid node path %q", node.Path)
		}
		key := strings.ToLower(node.Path)
		if folded[key] {
			return fmt.Errorf("duplicate or case-colliding node path %q", node.Path)
		}
		folded[key], nodes[node.Path] = true, node
		if node.Kind != Root && node.Kind != Namespace && node.Kind != Workstation {
			return fmt.Errorf("invalid node kind %q", node.Kind)
		}
		if (node.Path == ".") != (node.Kind == Root) {
			return fmt.Errorf("the unique root must have path '.' and kind r")
		}
		if node.Kind == Workstation {
			repo, err := Repository(node.Repository)
			if err != nil || repo != node.Repository {
				return fmt.Errorf("%s: repository must be canonical lowercase owner/name", node.Path)
			}
		} else if node.Repository != "" {
			return fmt.Errorf("%s: composites cannot declare a repository", node.Path)
		}
	}
	if _, ok := nodes["."]; !ok {
		return fmt.Errorf("missing root node")
	}
	for _, node := range i.Nodes {
		if node.Kind == Root {
			continue
		}
		parent, ok := nodes[path.Dir(node.Path)]
		if !ok || parent.Kind == Workstation {
			return fmt.Errorf("%s: missing composite parent", node.Path)
		}
	}
	return nil
}

func (i Index) Select(relative string) Node {
	selected := i.ByPath()["."]
	for _, node := range i.Nodes {
		if node.Path != "." && Within(node.Path, relative) && (selected.Path == "." || len(node.Path) > len(selected.Path)) {
			selected = node
		}
	}
	return selected
}

func (i Index) Scope(selected Node) []Node {
	var result []Node
	for _, node := range i.Nodes {
		if Within(selected.Path, node.Path) {
			result = append(result, node)
		}
	}
	Sort(result)
	return result
}
