package shared

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/versioning"
)

var ErrCommandUnavailable = errors.New("strict disk command unavailable")

type StrictDiskExecConfig struct {
	AgentID    string
	AgentType  string
	WorkingDir string
	Timeout    time.Duration
}

func RunStrictDiskCommand(ctx context.Context, cfg StrictDiskExecConfig, binary string, args []string, extraEnv map[string]string) (string, error) {
	if err := authorizeStrictDiskCommand(ctx, cfg, binary, args); err != nil {
		return "", err
	}
	callCtx, cancel := context.WithTimeout(ctx, normalizeExecTimeout(cfg.Timeout))
	defer cancel()

	broker := purevfs.DefaultExecutionBroker()
	plan, err := strictDiskExecutionPlan(callCtx, broker, cfg.WorkingDir)
	if err != nil {
		return "", normalizeStrictDiskExecError(binary, err)
	}
	result, err := broker.Run(callCtx, purevfs.BrokerRunRequest{
		Plan:      plan,
		Argv:      append([]string{binary}, args...),
		Env:       extraEnv,
		Workspace: purevfs.ReadOnlyExecutionFS(versioning.NewDiskFileAccess(cfg.WorkingDir, true)),
	})
	if err != nil {
		return "", normalizeStrictDiskExecError(binary, err)
	}
	if result.ExitCode == 127 {
		return "", fmt.Errorf("%w: %s", ErrCommandUnavailable, binary)
	}
	return strictDiskExecOutput(binary, args, result)
}

func CommandUnavailable(err error) bool {
	return errors.Is(err, ErrCommandUnavailable)
}

func authorizeStrictDiskCommand(ctx context.Context, cfg StrictDiskExecConfig, binary string, args []string) error {
	_, err := commandapproval.Authorize(ctx, commandapproval.NewEvaluator(nil), commandapproval.Request{
		Command:       strings.Join(append([]string{binary}, args...), " "),
		WorkingDir:    cfg.WorkingDir,
		WorkspaceRoot: cfg.WorkingDir,
		ToolName:      binary,
		AgentID:       cfg.AgentID,
		AgentType:     cfg.AgentType,
	})
	return err
}

func strictDiskExecutionPlan(ctx context.Context, broker purevfs.ExecutionBroker, workingDir string) (purevfs.ExecutionPlan, error) {
	caps, err := broker.Capabilities(ctx)
	if err != nil {
		return purevfs.ExecutionPlan{}, err
	}
	return purevfs.NewExecutionPlanner(nil, caps).Plan(purevfs.ExecutionRequest{
		Mode:          purevfs.ExecutionModeStrictNoDisk,
		Intent:        purevfs.ExecutionIntentCommand,
		WorkspaceRoot: workingDir,
		WorkingDir:    workingDir,
	})
}

func strictDiskExecOutput(binary string, args []string, result *purevfs.BrokerRunResult) (string, error) {
	if result == nil {
		return "", fmt.Errorf("%s %s failed: empty broker result", binary, strings.Join(args, " "))
	}
	stdout := strings.TrimSpace(string(result.Stdout))
	if result.ExitCode == 0 {
		return stdout, nil
	}
	stderr := strings.TrimSpace(string(result.Stderr))
	if stderr == "" {
		stderr = stdout
	}
	return "", fmt.Errorf("%s %s failed: %s", binary, strings.Join(args, " "), stderr)
}

func normalizeStrictDiskExecError(binary string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%w: %s", ErrCommandUnavailable, binary)
	case errors.Is(err, purevfs.ErrStrictExecutionUnavailable):
		return fmt.Errorf("%w: %s", ErrCommandUnavailable, binary)
	default:
		return err
	}
}

func normalizeExecTimeout(timeout time.Duration) time.Duration {
	if timeout > 0 {
		return timeout
	}
	return 20 * time.Second
}
