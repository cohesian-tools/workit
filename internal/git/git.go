// Package git delegates to the installed Git executable without invoking a shell.
package git

import (
	"context"
	"errors"
	"fmt"
	"github.com/cohesian-tools/workit/internal/graph"
	"github.com/cohesian-tools/workit/internal/workspace"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type Client struct {
	// Test seam for exercising clone installation against local bare repositories.
	cloneURL func(string, string) string
}

func command(ctx context.Context, directory string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = directory
	// An inherited GIT_DIR/WORK_TREE could redirect an operation to another repo.
	for _, value := range os.Environ() {
		key := strings.SplitN(value, "=", 2)[0]
		if strings.HasPrefix(key, "GIT_") && key != "GIT_SSH_COMMAND" && key != "GIT_SSH" {
			continue
		}
		cmd.Env = append(cmd.Env, value)
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func ParseOrigin(raw string) (string, error) {
	if strings.HasPrefix(raw, "git@github.com:") {
		return graph.Repository(strings.TrimSuffix(strings.TrimPrefix(raw, "git@github.com:"), ".git"))
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Hostname(), "github.com") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("origin is not an identifiable GitHub repository")
	}
	if u.Scheme == "https" {
		if u.User != nil || (u.Port() != "" && u.Port() != "443") {
			return "", fmt.Errorf("HTTPS origin must not contain credentials or a custom port")
		}
	} else if u.Scheme == "ssh" {
		if u.User == nil || u.User.String() != "git" || (u.Port() != "" && u.Port() != "22") {
			return "", fmt.Errorf("expected a standard GitHub SSH origin")
		}
	} else {
		return "", fmt.Errorf("expected an HTTPS or SSH GitHub origin")
	}
	return graph.Repository(strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), ".git"))
}

func (Client) Identity(ctx context.Context, directory string) (string, error) {
	top, err := command(ctx, directory, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(top)
	if err != nil {
		return "", err
	}
	expected, err := filepath.EvalSymlinks(directory)
	if err != nil || real != expected {
		return "", fmt.Errorf("directory is not a Git checkout root")
	}
	origin, err := command(ctx, directory, "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	return ParseOrigin(origin)
}

func URL(repository, protocol string) string {
	if protocol == "ssh" {
		return "git@github.com:" + repository + ".git"
	}
	return "https://github.com/" + repository + ".git"
}

func (c Client) Clone(ctx context.Context, directory, repository, protocol string) error {
	stage, err := os.MkdirTemp(filepath.Dir(directory), ".workit-clone-*")
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(stage)
		}
	}()
	remote := URL(repository, protocol)
	if c.cloneURL != nil {
		remote = c.cloneURL(repository, protocol)
	}
	if _, err := command(ctx, filepath.Dir(directory), "clone", "--", remote, stage); err != nil {
		return err
	}
	// Reserve the final name exclusively, even if an empty directory collides.
	if err := os.Mkdir(directory, 0755); err != nil {
		return err
	}
	entries, err := os.ReadDir(stage)
	if err != nil {
		_ = os.Remove(directory)
		return err
	}
	var installed []string
	for _, entry := range entries {
		name := entry.Name()
		if err := os.Rename(filepath.Join(stage, name), filepath.Join(directory, name)); err != nil {
			var rollback []error
			for _, previous := range installed {
				rollback = append(rollback, os.Rename(filepath.Join(directory, previous), filepath.Join(stage, previous)))
			}
			if rollbackErr := errors.Join(rollback...); rollbackErr != nil {
				cleanup = false
				return errors.Join(err, rollbackErr, fmt.Errorf("clone recovery content remains at %s and %s", directory, stage))
			}
			_ = os.Remove(directory) // Only removes our reservation if still empty.
			return err
		}
		installed = append(installed, name)
	}
	return nil
}

func (Client) Inspect(ctx context.Context, directory string) (workspace.RepoState, error) {
	var state workspace.RepoState
	raw, err := command(ctx, directory, "--no-optional-locks", "status", "--porcelain=v2", "--branch", "--ahead-behind", "--untracked-files=normal")
	if err != nil {
		return state, err
	}
	for _, line := range strings.Split(raw, "\n") {
		switch {
		case strings.HasPrefix(line, "# branch.oid "):
			state.HasCommit = line != "# branch.oid (initial)"
		case strings.HasPrefix(line, "# branch.head "):
			state.Branch = strings.TrimPrefix(line, "# branch.head ")
		case strings.HasPrefix(line, "# branch.upstream "):
			state.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
		case strings.HasPrefix(line, "# branch.ab "):
			fields := strings.Fields(line)
			if len(fields) == 4 {
				ahead, aheadErr := strconv.Atoi(strings.TrimPrefix(fields[2], "+"))
				behind, behindErr := strconv.Atoi(strings.TrimPrefix(fields[3], "-"))
				if aheadErr == nil && behindErr == nil {
					state.Ahead, state.Behind, state.HasComparison = ahead, behind, true
				}
			}
		case strings.HasPrefix(line, "? "):
			state.Untracked++
		case strings.HasPrefix(line, "u "):
			state.Conflicts++
		case strings.HasPrefix(line, "1 ") || strings.HasPrefix(line, "2 "):
			if len(line) > 3 {
				if line[2] != '.' {
					state.Staged++
				}
				if line[3] != '.' {
					state.Unstaged++
				}
			}
		}
	}
	if state.Ahead > 0 && state.Behind > 0 {
		if _, err := command(ctx, directory, "merge-base", "HEAD", "@{upstream}"); err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 {
				return state, err
			}
			state.Unrelated = true
		}
	}
	return state, nil
}

func (c Client) Preflight(ctx context.Context, directory, operation string, args []string) error {
	if operation == "status" || operation == "fetch" {
		return nil
	}
	state, err := c.Inspect(ctx, directory)
	if err != nil {
		return err
	}
	if state.Branch == "(detached)" || state.Branch == "" || state.Conflicts > 0 {
		return fmt.Errorf("an attached branch without conflicts is required")
	}
	if _, err := command(ctx, directory, "rev-parse", "--verify", "HEAD"); err != nil {
		return fmt.Errorf("pull/push requires an existing commit: %w", err)
	}
	if operation == "pull" {
		if len(args) != 2 {
			return fmt.Errorf("pull requires local and remote branches")
		}
		for _, branch := range args {
			if _, err := command(ctx, directory, "check-ref-format", "--branch", branch); err != nil {
				return err
			}
		}
		if state.Branch != args[0] {
			return fmt.Errorf("current branch is %s; pull requires %s and never switches branches", state.Branch, args[0])
		}
		if state.Staged+state.Unstaged+state.Untracked != 0 {
			return fmt.Errorf("pull requires a clean worktree, including untracked files")
		}
		_, err = command(ctx, directory, "ls-remote", "--exit-code", "--heads", "origin", "refs/heads/"+args[1])
		return err
	}
	if operation == "push" {
		remote, err := command(ctx, directory, "config", "--get", "branch."+state.Branch+".remote")
		if err != nil || remote != "origin" || state.Upstream == "" {
			return fmt.Errorf("push requires a configured origin upstream")
		}
		merge, err := command(ctx, directory, "config", "--get", "branch."+state.Branch+".merge")
		if err != nil || !strings.HasPrefix(merge, "refs/heads/") {
			return fmt.Errorf("push requires a branch upstream")
		}
		fetchURL, err := command(ctx, directory, "remote", "get-url", "origin")
		if err != nil {
			return err
		}
		pushURLs, err := command(ctx, directory, "remote", "get-url", "--push", "--all", "origin")
		if err != nil {
			return err
		}
		for _, pushURL := range strings.Split(pushURLs, "\n") {
			if pushURL == fetchURL {
				continue
			}
			fetchID, fetchErr := ParseOrigin(fetchURL)
			pushID, pushErr := ParseOrigin(pushURL)
			if fetchErr != nil || pushErr != nil || fetchID != pushID {
				return fmt.Errorf("origin push URL targets a different repository")
			}
		}
		return nil
	}
	return fmt.Errorf("unknown repository operation %q", operation)
}

func (c Client) Run(ctx context.Context, directory, operation string, args []string) (string, error) {
	if err := c.Preflight(ctx, directory, operation, args); err != nil {
		return "", err
	}
	switch operation {
	case "fetch":
		return command(ctx, directory, "fetch", "--prune", "origin")
	case "pull":
		if _, err := command(ctx, directory, "fetch", "--no-tags", "origin", "refs/heads/"+args[1]+":refs/remotes/origin/"+args[1]); err != nil {
			return "", err
		}
		return command(ctx, directory, "merge", "--ff-only", "--no-edit", "refs/remotes/origin/"+args[1])
	case "push":
		state, err := c.Inspect(ctx, directory)
		if err != nil {
			return "", err
		}
		merge, err := command(ctx, directory, "config", "--get", "branch."+state.Branch+".merge")
		if err != nil {
			return "", err
		}
		return command(ctx, directory, "push", "--porcelain", "origin", "HEAD:"+merge)
	}
	return "", fmt.Errorf("unknown repository operation %q", operation)
}
