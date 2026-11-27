// Package app is the composition root shared by the two executable entry points.
package app

import (
	"context"
	"fmt"
	"github.com/cohesian-tools/workit/internal/cli"
	gitadapter "github.com/cohesian-tools/workit/internal/git"
	"github.com/cohesian-tools/workit/internal/index"
	"github.com/cohesian-tools/workit/internal/void"
	"github.com/cohesian-tools/workit/internal/workdir"
	"github.com/cohesian-tools/workit/internal/workspace"
	"os"
	"os/signal"
	"syscall"
)

var Version = "0.1.0"

func Main() {
	git := gitadapter.Client{}
	service := workspace.Service{Store: index.Store{}, Disk: workdir.Disk{Git: git}, Git: git, Void: void.Vault{}}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := cli.New(service, Version).ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "workit:", err)
		os.Exit(1)
	}
}
