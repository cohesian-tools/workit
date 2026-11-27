package workspace

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

func (s Service) Init(directory, protocol string) error {
	if _, err := s.Store.Find(directory); err == nil {
		return fmt.Errorf("already inside a workspace")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	root, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return err
	}
	release, err := s.Store.Lock(root)
	if err != nil {
		return err
	}
	defer release()
	return s.Store.Create(root, graph.Index{Version: 1, Protocol: protocol, Nodes: []graph.Node{{Path: ".", Kind: graph.Root}}})
}

func (s Service) locate(directory string) (string, graph.Index, []byte, graph.Node, error) {
	root, err := s.Store.Find(directory)
	if err != nil {
		return "", graph.Index{}, nil, graph.Node{}, fmt.Errorf("find workspace: %w", err)
	}
	i, raw, err := s.Store.Load(root)
	if err != nil {
		return "", i, raw, graph.Node{}, err
	}
	real, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", i, raw, graph.Node{}, err
	}
	relative, err := filepath.Rel(root, real)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", i, raw, graph.Node{}, fmt.Errorf("directory is outside the workspace")
	}
	if strings.HasPrefix(filepath.ToSlash(relative), ".void/") || relative == ".void" || strings.HasPrefix(relative, ".workit") {
		return "", i, raw, graph.Node{}, fmt.Errorf("Workit metadata is not an execution scope")
	}
	if i.Excluded(filepath.ToSlash(relative)) {
		return "", i, raw, graph.Node{}, fmt.Errorf("directory is excluded from this workspace")
	}
	return root, i, raw, i.Select(filepath.ToSlash(relative)), nil
}

// writeScope holds the lock through planning, approval and execution.
func (s Service) writeScope(directory string, dry bool, fn func(string, graph.Index, []byte, graph.Node) (Plan, []Result, error)) (Plan, []Result, error) {
	root, _, _, _, err := s.locate(directory)
	if err != nil {
		return Plan{}, nil, err
	}
	if !dry {
		release, err := s.Store.Lock(root)
		if err != nil {
			return Plan{}, nil, err
		}
		defer release()
	}
	root, i, raw, selected, err := s.locate(directory)
	if err != nil {
		return Plan{}, nil, err
	}
	return fn(root, i, raw, selected)
}

func approve(plan Plan, options Options) error {
	if len(plan.Steps) == 0 || options.DryRun || !plan.Destructive {
		return nil
	}
	if options.Approve == nil {
		return fmt.Errorf("this removal requires explicit confirmation")
	}
	return options.Approve(plan)
}

func composite(node graph.Node) error {
	if node.Kind == graph.Workstation {
		return fmt.Errorf("this command requires a root or namespace scope, not a repository")
	}
	return nil
}

func (s Service) Protocol(directory, protocol string) error {
	_, _, err := s.writeScope(directory, false, func(root string, i graph.Index, raw []byte, selected graph.Node) (Plan, []Result, error) {
		i.Protocol = protocol
		return Plan{}, nil, s.Store.Save(root, i, raw)
	})
	return err
}

func (s Service) Add(ctx context.Context, directory, argument string, options Options) (Plan, []Result, error) {
	return s.writeScope(directory, options.DryRun, func(root string, i graph.Index, raw []byte, selected graph.Node) (Plan, []Result, error) {
		plan := Plan{Root: root, Selected: selected}
		if err := composite(selected); err != nil {
			return plan, nil, err
		}
		if exists, err := s.Disk.Check(root, selected); err != nil || !exists {
			return plan, nil, fmt.Errorf("selected composite is missing or incompatible: %v", err)
		}
		node := graph.Node{Kind: graph.Namespace}
		name := argument
		if strings.HasSuffix(argument, ".git") {
			repo, err := graph.Repository(strings.TrimSuffix(argument, ".git"))
			if err != nil {
				return plan, nil, err
			}
			node.Kind, node.Repository, name = graph.Workstation, repo, path.Base(repo)
		} else if !graph.ValidName(argument) {
			return plan, nil, fmt.Errorf("expected a namespace name or owner/repository.git")
		}
		node.Path = path.Join(selected.Path, name)
		i.Nodes = append(i.Nodes, node)
		if err := i.Validate(); err != nil {
			return plan, nil, err
		}
		if exists, err := s.Disk.Check(root, node); err != nil || exists {
			return plan, nil, fmt.Errorf("node path is occupied or incompatible: %s (%v)", node.Path, err)
		}
		step := Step{Action: "add", Node: node}
		plan.Steps = []Step{step}
		if options.DryRun {
			return plan, nil, nil
		}
		if err := s.materialize(ctx, root, node, i.Protocol); err != nil {
			return plan, []Result{{Step: step, Err: err}}, err
		}
		if err := s.Store.Save(root, i, raw); err != nil {
			receipt, moveErr := s.Void.Move(root, node, raw)
			return plan, nil, errors.Join(err, moveErr, fmt.Errorf("index was not updated; materialized content recovery: %s", receipt.Entry))
		}
		return plan, []Result{{Step: step}}, nil
	})
}

func (s Service) materialize(ctx context.Context, root string, node graph.Node, protocol string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if node.Kind == graph.Namespace {
		return s.Disk.CreateNamespace(root, node)
	}
	return s.Git.Clone(ctx, filepath.Join(root, filepath.FromSlash(node.Path)), node.Repository, protocol)
}

func (s Service) Remove(ctx context.Context, directory, name string, options Options) (Plan, []Result, error) {
	return s.writeScope(directory, options.DryRun, func(root string, i graph.Index, raw []byte, selected graph.Node) (Plan, []Result, error) {
		plan := Plan{Root: root, Selected: selected, Destructive: true}
		if err := composite(selected); err != nil {
			return plan, nil, err
		}
		if !graph.ValidName(name) {
			return plan, nil, fmt.Errorf("remove expects an immediate child name")
		}
		node, ok := i.ByPath()[path.Join(selected.Path, name)]
		if !ok {
			return plan, nil, fmt.Errorf("node is not registered")
		}
		exists, err := s.Disk.Check(root, node)
		if err != nil {
			return plan, nil, err
		}
		if exists && node.Kind == graph.Workstation {
			if err := s.identity(ctx, root, node); err != nil {
				return plan, nil, err
			}
		}
		var retained []graph.Node
		for _, child := range i.Scope(node) {
			present, err := s.Disk.Check(root, child)
			if err != nil {
				return plan, nil, err
			}
			if present && child.Kind == graph.Workstation {
				if err := s.identity(ctx, root, child); err != nil {
					return plan, nil, err
				}
			}
		}
		for _, n := range i.Nodes {
			if !graph.Within(node.Path, n.Path) {
				retained = append(retained, n)
			}
		}
		i.Nodes = retained
		if err := i.Validate(); err != nil {
			return plan, nil, err
		}
		step := Step{Action: "remove", Node: node}
		plan.Steps = []Step{step}
		if err := approve(plan, options); err != nil {
			return plan, nil, err
		}
		if err := ctx.Err(); err != nil {
			return plan, nil, err
		}
		if options.DryRun {
			return plan, nil, nil
		}
		var receipt Receipt
		if exists {
			receipt, err = s.Void.Move(root, node, raw)
			if err != nil {
				return plan, nil, err
			}
		}
		if err := s.Store.Save(root, i, raw); err != nil {
			if exists {
				return plan, nil, errors.Join(err, s.Void.Restore(root, receipt))
			}
			return plan, nil, err
		}
		return plan, []Result{{Step: step, Output: receipt.Entry}}, nil
	})
}

func (s Service) identity(ctx context.Context, root string, node graph.Node) error {
	if exists, err := s.Disk.Check(root, node); err != nil || !exists {
		return fmt.Errorf("%s: missing or incompatible repository (%v)", node.Path, err)
	}
	repo, err := s.Git.Identity(ctx, filepath.Join(root, filepath.FromSlash(node.Path)))
	if err != nil {
		return err
	}
	if repo != node.Repository {
		return fmt.Errorf("%s: origin identity conflict: expected %s, found %s", node.Path, node.Repository, repo)
	}
	return nil
}

func (s Service) Repositories(ctx context.Context, directory, operation string, args []string, options Options) (Plan, []Result, error) {
	return s.writeScope(directory, options.DryRun || operation == "status", func(root string, i graph.Index, raw []byte, selected graph.Node) (Plan, []Result, error) {
		plan := Plan{Root: root, Selected: selected, Nodes: i.Scope(selected)}
		var results []Result
		var failures []error
		for _, node := range i.Scope(selected) {
			if node.Kind == graph.Workstation {
				plan.Steps = append(plan.Steps, Step{Action: operation, Node: node})
			}
		}
		// For mutations, every target must pass preflight before the first Git action.
		if operation != "status" {
			for _, step := range plan.Steps {
				if err := s.identity(ctx, root, step.Node); err != nil {
					return plan, nil, err
				}
				if err := s.Git.Preflight(ctx, filepath.Join(root, filepath.FromSlash(step.Node.Path)), operation, args); err != nil {
					return plan, nil, fmt.Errorf("%s: %w", step.Node.Path, err)
				}
			}
		}
		if options.DryRun {
			return plan, nil, nil
		}
		for _, step := range plan.Steps {
			result := Result{Step: step}
			result.Err = s.identity(ctx, root, step.Node)
			if result.Err == nil {
				directory := filepath.Join(root, filepath.FromSlash(step.Node.Path))
				if operation == "status" {
					state, err := s.Git.Inspect(ctx, directory)
					result.State, result.Err = &state, err
				} else {
					result.Output, result.Err = s.Git.Run(ctx, directory, operation, args)
				}
			}
			if result.Err != nil {
				failures = append(failures, fmt.Errorf("%s: %w", step.Node.Path, result.Err))
			}
			results = append(results, result)
			if ctx.Err() != nil {
				break
			}
		}
		return plan, results, errors.Join(failures...)
	})
}
