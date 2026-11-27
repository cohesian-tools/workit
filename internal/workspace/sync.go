package workspace

import (
	"context"
	"errors"
	"fmt"
	"github.com/cohesian-tools/workit/internal/graph"
	"path/filepath"
	"strings"
)

func (s Service) Sync(ctx context.Context, directory, side, mode string, options Options) (Plan, []Result, error) {
	return s.writeScope(directory, options.DryRun, func(root string, i graph.Index, raw []byte, selected graph.Node) (Plan, []Result, error) {
		plan := Plan{Root: root, Selected: selected}
		if err := composite(selected); err != nil {
			return plan, nil, err
		}
		if !(side == "index" && (mode == "add" || mode == "prune")) && !(side == "workdir" && (mode == "add" || mode == "remove")) {
			return plan, nil, fmt.Errorf("unknown synchronization operation")
		}
		discovered, err := s.Disk.Discover(ctx, root, selected, i.Exclude)
		if err != nil {
			return plan, nil, err
		}
		indexed := i.Scope(selected)
		byIndex, byDisk := make(map[string]graph.Node), make(map[string]graph.Node)
		folded := make(map[string]string)
		for _, node := range indexed {
			if _, err := s.Disk.Check(root, node); err != nil {
				return plan, nil, err
			}
			byIndex[node.Path], folded[strings.ToLower(node.Path)] = node, node.Path
		}
		for _, node := range discovered {
			key := strings.ToLower(node.Path)
			if previous, ok := folded[key]; ok && previous != node.Path {
				return plan, nil, fmt.Errorf("case conflict: %s and %s", previous, node.Path)
			}
			folded[key], byDisk[node.Path] = node.Path, node
			if registered, ok := byIndex[node.Path]; ok && registered != node {
				return plan, nil, fmt.Errorf("identity conflict at %s: index=%+v, physical=%+v", node.Path, registered, node)
			}
		}
		var missing, extra []graph.Node
		for _, node := range indexed {
			if _, ok := byDisk[node.Path]; !ok && node.Kind != graph.Root {
				missing = append(missing, node)
			}
		}
		for _, node := range discovered {
			if _, ok := byIndex[node.Path]; !ok && node.Kind != graph.Root {
				extra = append(extra, node)
			}
		}
		graph.Sort(missing)
		graph.Sort(extra)
		if side == "index" {
			if mode == "add" {
				for _, node := range extra {
					plan.Steps = append(plan.Steps, Step{Action: "register", Node: node})
					i.Nodes = append(i.Nodes, node)
				}
			} else {
				removed := make(map[string]bool)
				for _, node := range missing {
					removed[node.Path] = true
					plan.Steps = append(plan.Steps, Step{Action: "unregister", Node: node})
				}
				var retained []graph.Node
				for _, node := range i.Nodes {
					if !removed[node.Path] {
						retained = append(retained, node)
					}
				}
				i.Nodes = retained
			}
			if err := i.Validate(); err != nil {
				return plan, nil, err
			}
			if options.DryRun || len(plan.Steps) == 0 {
				return plan, nil, nil
			}
			if err := s.Store.Save(root, i, raw); err != nil {
				return plan, nil, err
			}
			var results []Result
			for _, step := range plan.Steps {
				results = append(results, Result{Step: step})
			}
			return plan, results, nil
		}
		if mode == "add" {
			for _, node := range missing {
				plan.Steps = append(plan.Steps, Step{Action: "materialize", Node: node})
			}
		} else {
			plan.Destructive = true
			// Quarantine only maximal extra subtrees; never a matched descendant.
			for _, node := range extra {
				covered := false
				for _, step := range plan.Steps {
					if graph.Within(step.Node.Path, node.Path) {
						covered = true
						break
					}
				}
				if covered {
					continue
				}
				for _, registered := range indexed {
					if graph.Within(node.Path, registered.Path) {
						return plan, nil, fmt.Errorf("refusing to remove matched descendant %s", registered.Path)
					}
				}
				plan.Steps = append(plan.Steps, Step{Action: "quarantine", Node: node})
			}
		}
		if err := approve(plan, options); err != nil {
			return plan, nil, err
		}
		if options.DryRun {
			return plan, nil, nil
		}
		var results []Result
		var failures []error
		for _, step := range plan.Steps {
			result := Result{Step: step}
			if err := ctx.Err(); err != nil {
				failures = append(failures, err)
				break
			}
			if mode == "add" {
				result.Err = s.materialize(ctx, root, step.Node, i.Protocol)
			} else {
				receipt, err := s.Void.Move(root, step.Node, raw)
				result.Output, result.Err = receipt.Entry, err
			}
			if result.Err != nil {
				failures = append(failures, fmt.Errorf("%s: %w", step.Node.Path, result.Err))
			}
			results = append(results, result)
		}
		return plan, results, errors.Join(failures...)
	})
}

func (s Service) Clean(directory string, options Options) (Plan, []Result, error) {
	return s.writeScope(directory, options.DryRun, func(root string, i graph.Index, raw []byte, selected graph.Node) (Plan, []Result, error) {
		plan := Plan{Root: root, Selected: selected, Destructive: true}
		if selected.Kind != graph.Root {
			return plan, nil, fmt.Errorf("void clean is root-only")
		}
		// Root-only refers to the physical root as well as its selected scope.
		real, err := filepath.EvalSymlinks(directory)
		if err != nil {
			return plan, nil, err
		}
		if real != root {
			return plan, nil, fmt.Errorf("void clean must run at the workspace root")
		}
		entries, err := s.Void.Entries(root)
		if err != nil {
			return plan, nil, err
		}
		for _, entry := range entries {
			plan.Steps = append(plan.Steps, Step{Action: "delete permanently", Node: graph.Node{Path: ".void/" + entry, Kind: graph.Namespace}})
		}
		if err := approve(plan, options); err != nil {
			return plan, nil, err
		}
		if options.DryRun || len(entries) == 0 {
			return plan, nil, nil
		}
		if err := s.Void.Clean(root, entries); err != nil {
			return plan, nil, err
		}
		var results []Result
		for _, step := range plan.Steps {
			results = append(results, Result{Step: step})
		}
		return plan, results, nil
	})
}
