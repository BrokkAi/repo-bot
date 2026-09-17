package repobot

import (
	"context"
	"log/slog"

	"github.com/BrokkAi/acp-go"
	"github.com/BrokkAi/acp-go/runner"
)

type Agent interface {
	Execute(context.Context, string) (string, error)
}

type agentProcess struct {
	config Config
	log    *slog.Logger
}

func (a agentProcess) Execute(ctx context.Context, prompt string) (string, error) {
	r := runner.Runner{Config: runner.Config{
		Directory: a.config.Directory, StateDirectory: a.config.StateDirectory, Agent: a.config.Agent,
		AutoApprove: true, ClientInfo: acp.ClientInfo{Name: "repo-bot", Version: Version},
	}, Log: a.log}
	return r.Execute(ctx, prompt)
}
