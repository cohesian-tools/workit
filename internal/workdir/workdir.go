// Package workdir discovers physical nodes without crossing repository boundaries.
package workdir

import (
	"context"
	"errors"
	"fmt"
	"github.com/cohesian-tools/workit/internal/graph"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type Identifier interface {
	Identity(context.Context, string) (string, error)
}
type Disk struct{ Git Identifier }

// Resolve checks every existing component; it never follows workspace symlinks.
func Resolve(root, relative string) (string, bool, error) {
	if !graph.ValidPath(relative) {
		return "", false, fmt.Errorf("invalid workspace path %q", relative)
	}
	if relative == "." {
		return root, true, nil
	}
	current := root
	for _, component := range strings.Split(relative, "/") {
		entries, err := os.ReadDir(current)
		if errors.Is(err, os.ErrNotExist) {
			return filepath.Join(root, filepath.FromSlash(relative)), false, nil
		}
		if err != nil {
			return "", false, err
		}
		for _, entry := range entries {
			if strings.EqualFold(entry.Name(), component) && entry.Name() != component {
				return "", false, fmt.Errorf("case conflict: %s versus %s", entry.Name(), component)
			}
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return filepath.Join(root, filepath.FromSlash(relative)), false, nil
		}
		if err != nil {
			return "", false, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", false, fmt.Errorf("%s must be a real directory, not a file or symlink", current)
		}
	}
	return current, true, nil
}

func (Disk) Check(root string, node graph.Node) (bool, error) {
	target, exists, err := Resolve(root, node.Path)
	if err != nil || !exists || node.Kind == graph.Root {
		return exists, err
	}
	_, markerErr := os.Lstat(filepath.Join(target, ".git"))
	if markerErr != nil && !errors.Is(markerErr, os.ErrNotExist) {
		return false, markerErr
	}
	if (markerErr == nil) != (node.Kind == graph.Workstation) {
		return false, fmt.Errorf("%s: physical node kind conflicts with index kind %s", node.Path, node.Kind)
	}
	return true, nil
}

func (d Disk) Discover(ctx context.Context, root string, selected graph.Node, exclusions []string) ([]graph.Node, error) {
	rules := graph.Index{Exclude: exclusions}
	if rules.Excluded(selected.Path) {
		return nil, fmt.Errorf("selected directory is excluded")
	}
	base, exists, err := Resolve(root, selected.Path)
	if err != nil || !exists {
		return nil, err
	}
	var nodes []graph.Node
	var visit func(string, string) error
	visit = func(directory, relative string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if bareLayout(directory) {
			return fmt.Errorf("%s: bare Git repositories are not supported; discovery stopped", relative)
		}
		kind := graph.Namespace
		if relative == "." {
			kind = graph.Root
		} else {
			if _, err := os.Lstat(filepath.Join(directory, ".workit.toml")); err == nil {
				return fmt.Errorf("nested workspace at %s; discovery is incomplete", relative)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if _, err := os.Lstat(filepath.Join(directory, ".git")); err == nil {
				repo, err := d.Git.Identity(ctx, directory)
				if err != nil {
					return fmt.Errorf("%s: %w", relative, err)
				}
				nodes = append(nodes, graph.Node{Path: relative, Kind: graph.Workstation, Repository: repo})
				return nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		nodes = append(nodes, graph.Node{Path: relative, Kind: kind})
		entries, err := os.ReadDir(directory)
		if err != nil {
			return fmt.Errorf("incomplete discovery at %s: %w", relative, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() || !graph.ValidName(entry.Name()) || rules.Excluded(path.Join(relative, entry.Name())) {
				continue
			}
			if err := visit(filepath.Join(directory, entry.Name()), path.Join(relative, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(base, selected.Path); err != nil {
		return nil, err
	}
	graph.Sort(nodes)
	return nodes, nil
}

func bareLayout(directory string) bool {
	for _, name := range []string{"HEAD", "config", "objects", "refs"} {
		info, err := os.Lstat(filepath.Join(directory, name))
		if err != nil || (name == "objects" || name == "refs") != info.IsDir() {
			return false
		}
	}
	return true
}

func (Disk) CreateNamespace(root string, node graph.Node) error {
	target, exists, err := Resolve(root, node.Path)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("path already exists: %s", node.Path)
	}
	return os.Mkdir(target, 0755)
}
