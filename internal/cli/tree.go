// Tree rendering is shared by command help, workspace status and operation output.
package cli

import (
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/cohesian-tools/workit/internal/graph"
	"github.com/cohesian-tools/workit/internal/workspace"
	"github.com/spf13/cobra"
)

type treeLine struct {
	label    string
	children []*treeLine
}

func printTree(out io.Writer, root *treeLine) {
	fmt.Fprintln(out, root.label)
	var printChildren func(*treeLine, string)
	printChildren = func(parent *treeLine, indent string) {
		for i, child := range parent.children {
			branch, continuation := "├── ", "│   "
			if i == len(parent.children)-1 {
				branch, continuation = "└── ", "    "
			}
			fmt.Fprintln(out, indent+branch+child.label)
			printChildren(child, indent+continuation)
		}
	}
	printChildren(root, "")
}

func scopeLabel(selected graph.Node) string {
	if selected.Path == "." {
		return "./"
	}
	label := "./" + selected.Path
	if selected.Kind == graph.Namespace {
		label += "/"
	}
	return label
}

type nodeLine struct {
	label  string
	detail []string
}

// printPaths supplies unmarked ancestor directories when only part of a tree
// is reported. Those directories provide context without claiming membership.
func printPaths(out io.Writer, selected graph.Node, nodes map[string]nodeLine, empty string) {
	root := &treeLine{label: scopeLabel(selected)}
	lines := map[string]*treeLine{selected.Path: root}
	var lookup func(string) *treeLine
	lookup = func(relative string) *treeLine {
		if line := lines[relative]; line != nil {
			return line
		}
		parent := lookup(path.Dir(relative))
		line := &treeLine{label: path.Base(relative) + "/"}
		lines[relative] = line
		parent.children = append(parent.children, line)
		return line
	}
	paths := make([]string, 0, len(nodes))
	for relative := range nodes {
		paths = append(paths, relative)
	}
	sort.Strings(paths)
	for _, relative := range paths {
		entry := nodes[relative]
		line := lookup(relative)
		line.label = entry.label
		for _, detail := range entry.detail {
			line.children = append(line.children, &treeLine{label: detail})
		}
	}
	if len(paths) == 0 {
		root.children = append(root.children, &treeLine{label: empty})
	}
	printTree(out, root)
}

func nodeLabel(selected, node graph.Node) string {
	if node.Path == selected.Path {
		return scopeLabel(selected)
	}
	label := path.Base(node.Path)
	if node.Kind == graph.Namespace {
		label += "/"
	}
	return label
}

func printSyncStatus(out io.Writer, status workspace.SyncStatus) {
	for i, group := range []struct {
		title, icon string
		nodes       []graph.Node
	}{
		{"Inner · index & workdir", "✅", status.Inner},
		{"Outer · index only", "☑️", status.IndexOnly},
		{"Outer · workdir only", "⚠️", status.WorkdirOnly},
	} {
		if i > 0 {
			fmt.Fprintln(out)
		}
		fmt.Fprintln(out, group.icon, group.title)
		nodes := make(map[string]nodeLine, len(group.nodes))
		for _, node := range group.nodes {
			label := nodeLabel(status.Selected, node) + " " + group.icon
			if node.Repository != "" {
				label += " " + node.Repository
			}
			nodes[node.Path] = nodeLine{label: label}
		}
		printPaths(out, status.Selected, nodes, "(empty)")
	}
}

func printPlan(cmd *cobra.Command, plan workspace.Plan) {
	if plan.Root == "" {
		return
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Workspace: %s\n", plan.Root)
	nodes := make(map[string]nodeLine, len(plan.Steps))
	for _, step := range plan.Steps {
		label := nodeLabel(plan.Selected, step.Node) + " · " + step.Action
		nodes[step.Node.Path] = nodeLine{label: label}
	}
	printPaths(out, plan.Selected, nodes, "No changes.")
}

func printResults(cmd *cobra.Command, plan workspace.Plan, results []workspace.Result) {
	if plan.Root == "" {
		return
	}
	nodes := make(map[string]nodeLine, len(plan.Nodes)+len(results))
	for _, node := range plan.Nodes {
		nodes[node.Path] = nodeLine{label: nodeLabel(plan.Selected, node)}
	}
	for _, result := range results {
		node := result.Step.Node
		label := nodeLabel(plan.Selected, node)
		switch {
		case result.Err != nil:
			label += " · ⚠️ " + result.Step.Action + " failed"
		case result.State != nil:
			label += " · " + repoStatus(*result.State)
		default:
			label += " · ✅ " + result.Step.Action
		}
		var detail []string
		if result.Output != "" {
			detail = strings.Split(result.Output, "\n")
		}
		if result.Err != nil {
			detail = append(detail, strings.Split(result.Err.Error(), "\n")...)
		}
		nodes[node.Path] = nodeLine{label: label, detail: detail}
	}
	printPaths(cmd.OutOrStdout(), plan.Selected, nodes, "No changes applied.")
}

func repoStatus(state workspace.RepoState) string {
	head := "✅ synced"
	switch {
	case !state.HasCommit:
		head = "⚠️ no commits"
	case state.Branch == "(detached)":
		head = "⚠️ detached"
	case state.Upstream == "":
		head = "⚠️ no upstream"
	case !state.HasComparison:
		head = "⚠️ upstream unavailable"
	case state.Unrelated:
		head = fmt.Sprintf("↔️ no common ancestor visible (+%d −%d)", state.Ahead, state.Behind)
	case state.Ahead > 0 && state.Behind > 0:
		head = fmt.Sprintf("↔️ unsynced (+%d −%d)", state.Ahead, state.Behind)
	case state.Ahead > 0:
		head = fmt.Sprintf("⬆️ ahead %d", state.Ahead)
	case state.Behind > 0:
		head = fmt.Sprintf("⬇️ behind %d", state.Behind)
	}
	if state.Upstream != "" {
		head += " (" + state.Branch + " → " + state.Upstream + ")"
	} else if state.Branch != "" && state.Branch != "(detached)" {
		head += " (" + state.Branch + ")"
	}

	var changes []string
	if state.Staged > 0 {
		changes = append(changes, fmt.Sprintf("☑️ %d staged", state.Staged))
	}
	if state.Unstaged > 0 || state.Untracked > 0 {
		var outsideStage []string
		if state.Unstaged > 0 {
			outsideStage = append(outsideStage, fmt.Sprintf("%d unstaged", state.Unstaged))
		}
		if state.Untracked > 0 {
			outsideStage = append(outsideStage, fmt.Sprintf("%d untracked", state.Untracked))
		}
		changes = append(changes, "⚠️ "+strings.Join(outsideStage, ", "))
	}
	if len(changes) == 0 {
		changes = append(changes, "✅ no changes")
	}
	conflicts := "✅ none"
	if state.Conflicts > 0 {
		conflicts = fmt.Sprintf("⚠️ %d", state.Conflicts)
	}
	return fmt.Sprintf("head %s | stage %s | conflicts %s", head, strings.Join(changes, " "), conflicts)
}

func printHelp(cmd *cobra.Command, _ []string) {
	out := cmd.OutOrStdout()
	description := cmd.Long
	if description == "" {
		description = cmd.Short
	}
	if description != "" {
		fmt.Fprintln(out, description)
		fmt.Fprintln(out)
	}
	fmt.Fprintln(out, "Usage:", cmd.UseLine())
	var commands func(*cobra.Command) []*treeLine
	commands = func(parent *cobra.Command) []*treeLine {
		var lines []*treeLine
		for _, child := range parent.Commands() {
			if !child.IsAvailableCommand() && child.Name() != "help" {
				continue
			}
			children := commands(child)
			icon := "▸"
			if len(children) > 0 {
				icon = "📁"
			}
			label := icon + " " + child.Use
			if child.Short != "" {
				label += " · " + child.Short
			}
			lines = append(lines, &treeLine{label: label, children: children})
		}
		return lines
	}
	if children := commands(cmd); len(children) > 0 {
		fmt.Fprintln(out)
		printTree(out, &treeLine{label: cmd.CommandPath(), children: children})
	}
	if flags := cmd.LocalFlags().FlagUsages(); flags != "" {
		fmt.Fprintf(out, "\nFlags:\n%s", flags)
	}
	if flags := cmd.InheritedFlags().FlagUsages(); flags != "" {
		fmt.Fprintf(out, "\nGlobal flags:\n%s", flags)
	}
}
