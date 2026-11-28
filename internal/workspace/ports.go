// Package workspace coordinates selection, plans and execution through adapters.
package workspace

import (
	"context"
	"github.com/cohesian-tools/workit/internal/graph"
)

type Store interface {
	Find(string) (string, error)
	Load(string) (graph.Index, []byte, error)
	Create(string, graph.Index) error
	Save(string, graph.Index, []byte) error
	Lock(string) (func(), error)
}

type Disk interface {
	Check(string, graph.Node) (bool, error)
	Discover(context.Context, string, graph.Node, []string) ([]graph.Node, error)
	CreateNamespace(string, graph.Node) error
}

type Git interface {
	Identity(context.Context, string) (string, error)
	Clone(context.Context, string, string, string) error
	Inspect(context.Context, string) (RepoState, error)
	Preflight(context.Context, string, string, []string) error
	Run(context.Context, string, string, []string) (string, error)
}

type Quarantine interface {
	Move(string, graph.Node, []byte) (Receipt, error)
	Restore(string, Receipt) error
	Entries(string) ([]string, error)
	Clean(string, []string) error
}

type Receipt struct{ Original, Payload, Entry string }

type RepoState struct {
	Branch, Upstream                                      string
	HasCommit, HasComparison, Unrelated                   bool
	Ahead, Behind, Staged, Unstaged, Untracked, Conflicts int
}

type Step struct {
	Action string
	Node   graph.Node
}

type Plan struct {
	Root        string
	Selected    graph.Node
	Nodes       []graph.Node
	Steps       []Step
	Destructive bool
}

type Result struct {
	Step   Step
	Output string
	State  *RepoState
	Err    error
}

type Options struct {
	DryRun  bool
	Approve func(Plan) error
}

type Service struct {
	Store Store
	Disk  Disk
	Git   Git
	Void  Quarantine
}
