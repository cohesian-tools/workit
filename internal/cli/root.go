// Package cli maps Cobra commands to workspace operations. It owns no adapters.
package cli

import (
	"bufio"
	"context"
	"fmt"
	"github.com/cohesian-tools/workit/internal/graph"
	"github.com/cohesian-tools/workit/internal/workspace"
	"github.com/spf13/cobra"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func New(service workspace.Service, version string) *cobra.Command {
	var directory string
	newGroup := func(use, short string) *cobra.Command {
		return &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	}
	root := newGroup("workit", "One workspace index, directory-local scope")
	root.Version, root.SilenceUsage, root.SilenceErrors = version, true, true
	root.PersistentFlags().StringVarP(&directory, "directory", "C", "", "Select a working directory without changing the process directory")
	cwd := func() (string, error) {
		if directory == "" {
			return os.Getwd()
		}
		return filepath.Abs(directory)
	}
	root.AddCommand(&cobra.Command{Use: "init", Short: "Create .workit.toml outside an existing workspace", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		dir, err := cwd()
		if err != nil {
			return err
		}
		if err := service.Init(dir, "ssh"); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Initialized", filepath.Join(dir, ".workit.toml"))
		return nil
	}})
	config := newGroup("config", "Edit workspace-wide configuration")
	config.AddCommand(&cobra.Command{Use: "protocol <https|ssh>", Short: "Set future clone transport; keep existing remotes", Args: cobra.ExactArgs(1), ValidArgs: []string{"https", "ssh"}, RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := cwd()
		if err != nil {
			return err
		}
		if err := service.Protocol(dir, args[0]); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Clone protocol:", args[0])
		return nil
	}})
	root.AddCommand(config)
	type operation func(context.Context, string, []string, workspace.Options) (workspace.Plan, []workspace.Result, error)
	leaf := func(use, short string, args cobra.PositionalArgs, destructive bool, operation operation) *cobra.Command {
		cmd := &cobra.Command{Use: use, Short: short, Args: args}
		cmd.Flags().Bool("dry-run", false, "Validate and print the plan without applying it")
		if destructive {
			cmd.Flags().BoolP("yes", "y", false, "Explicitly confirm the displayed removal")
		}
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			dir, err := cwd()
			if err != nil {
				return err
			}
			dry, _ := cmd.Flags().GetBool("dry-run")
			options := workspace.Options{DryRun: dry}
			options.Approve = func(plan workspace.Plan) error {
				printPlan(cmd, plan)
				yes, _ := cmd.Flags().GetBool("yes")
				if yes {
					return nil
				}
				fmt.Fprint(cmd.OutOrStdout(), "Confirm removal [y/N]: ")
				type reply struct {
					answer string
					err    error
				}
				input := make(chan reply, 1)
				go func() { answer, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n'); input <- reply{answer, err} }()
				var answer string
				select {
				case <-cmd.Context().Done():
					return cmd.Context().Err()
				case received := <-input:
					answer = received.answer
					if received.err != nil && (received.err != io.EOF || answer == "") {
						return fmt.Errorf("confirmation required; use --yes for an explicit noninteractive removal")
					}
				}
				if strings.ToLower(strings.TrimSpace(answer)) != "y" && strings.ToLower(strings.TrimSpace(answer)) != "yes" {
					return fmt.Errorf("cancelled; nothing changed")
				}
				return nil
			}
			plan, results, err := operation(cmd.Context(), dir, args, options)
			if dry {
				printPlan(cmd, plan)
			} else {
				printResults(cmd, plan, results)
			}
			return err
		}
		return cmd
	}
	node := newGroup("node", "Manage indexed namespaces and repositories")
	node.AddCommand(leaf("add <name|owner/repository.git>", "Register and create or clone an immediate child", cobra.ExactArgs(1), false, func(ctx context.Context, dir string, args []string, options workspace.Options) (workspace.Plan, []workspace.Result, error) {
		return service.Add(ctx, dir, args[0], options)
	}))
	node.AddCommand(leaf("remove <name>", "Unregister a subtree and move its physical contents into .void", cobra.ExactArgs(1), true, func(ctx context.Context, dir string, args []string, options workspace.Options) (workspace.Plan, []workspace.Result, error) {
		return service.Remove(ctx, dir, args[0], options)
	}))
	root.AddCommand(node)
	sync := newGroup("sync", "Reconcile exactly one side of the workspace")
	for _, definition := range []struct {
		side  string
		modes []string
	}{{"index", []string{"add", "prune"}}, {"workdir", []string{"add", "remove"}}} {
		group := newGroup(definition.side, "Reconcile "+definition.side)
		for _, mode := range definition.modes {
			side, mode := definition.side, mode
			group.AddCommand(leaf(mode, "Reconcile "+side+" ("+mode+") in the selected subtree", cobra.NoArgs, side == "workdir" && mode == "remove", func(ctx context.Context, dir string, _ []string, options workspace.Options) (workspace.Plan, []workspace.Result, error) {
				return service.Sync(ctx, dir, side, mode, options)
			}))
		}
		sync.AddCommand(group)
	}
	root.AddCommand(sync)
	repo := newGroup("repo", "Plan Git operations over the selected repository forest")
	for _, definition := range []struct {
		use, name, description string
		args                   cobra.PositionalArgs
	}{
		{"status", "status", "Show current branches, cached upstream divergence and local edits", cobra.NoArgs},
		{"fetch", "fetch", "Fetch origin for every target; do not merge", cobra.NoArgs},
		{"pull <local_branch> <remote_branch>", "pull", "Fast-forward the current local branch from origin; never switch branches", cobra.ExactArgs(2)},
		{"push", "push", "Push each current branch to its configured origin upstream; never force", cobra.NoArgs},
	} {
		name := definition.name
		repo.AddCommand(leaf(definition.use, definition.description, definition.args, false, func(ctx context.Context, dir string, args []string, options workspace.Options) (workspace.Plan, []workspace.Result, error) {
			return service.Repositories(ctx, dir, name, args, options)
		}))
	}
	root.AddCommand(repo)
	void := newGroup("void", "Manage root quarantine")
	void.AddCommand(leaf("clean", "Permanently delete initialized quarantine entries (root only)", cobra.NoArgs, true, func(_ context.Context, dir string, _ []string, options workspace.Options) (workspace.Plan, []workspace.Result, error) {
		return service.Clean(dir, options)
	}))
	root.AddCommand(void)
	return root
}

func printPlan(cmd *cobra.Command, plan workspace.Plan) {
	if plan.Root == "" {
		return
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Workspace: %s\nScope: %s (%s)\n", plan.Root, plan.Selected.Path, plan.Selected.Kind)
	for _, step := range plan.Steps {
		fmt.Fprintf(cmd.OutOrStdout(), "  %s  %s\n", step.Action, step.Node.Path)
	}
	if len(plan.Steps) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No changes.")
	}
}

func printResults(cmd *cobra.Command, plan workspace.Plan, results []workspace.Result) {
	if len(plan.Nodes) != 0 {
		byPath := make(map[string]workspace.Result)
		children := make(map[string][]graph.Node)
		for _, node := range plan.Nodes {
			if node.Kind != graph.Root {
				children[path.Dir(node.Path)] = append(children[path.Dir(node.Path)], node)
			}
		}
		for _, result := range results {
			byPath[result.Step.Node.Path] = result
		}
		var walk func(graph.Node, string)
		walk = func(node graph.Node, prefix string) {
			label := path.Base(node.Path)
			if node.Path == "." {
				label = filepath.Base(plan.Root)
			}
			detail := ""
			if result, ok := byPath[node.Path]; ok {
				if result.Err != nil {
					detail = "  ERROR: " + result.Err.Error()
				} else if state := result.State; state != nil {
					upstream := state.Upstream
					if upstream == "" {
						upstream = "no upstream"
					}
					detail = fmt.Sprintf("  %s -> %s | ahead:%d behind:%d staged:%d unstaged:%d untracked:%d conflicts:%d", state.Branch, upstream, state.Ahead, state.Behind, state.Staged, state.Unstaged, state.Untracked, state.Conflicts)
				} else {
					detail = "  ok"
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s%s (%s)%s\n", prefix, label, node.Kind, detail)
			for _, child := range children[node.Path] {
				walk(child, prefix+"  ")
			}
		}
		walk(plan.Selected, "")
		return
	}
	for _, result := range results {
		status := "ok"
		if result.Err != nil {
			status = "ERROR: " + result.Err.Error()
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s: %s\n", result.Step.Action, result.Step.Node.Path, status)
		if result.Output != "" {
			fmt.Fprintln(cmd.OutOrStdout(), result.Output)
		}
	}
	if len(results) == 0 && plan.Root != "" {
		fmt.Fprintln(cmd.OutOrStdout(), "No changes applied.")
	}
}
