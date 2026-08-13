package adapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const (
	defaultAgentCLICommandTimeout = 30 * time.Second
	defaultAgentCLIOutputLimit    = 64 * 1024
)

type agentCLIResult struct {
	Path            string
	Stdout          string
	Stderr          string
	ExitCode        int
	Started         bool
	TimedOut        bool
	StdoutTruncated bool
	StderrTruncated bool
}

type agentCLIRunner interface {
	Run(context.Context, string, []string, string) (agentCLIResult, error)
}

type agentCLICommandRunner struct {
	timeout     time.Duration
	outputLimit int
	lookPath    func(string) (string, error)
}

func newDefaultAgentCLIRunner() *agentCLICommandRunner {
	return &agentCLICommandRunner{
		timeout:     defaultAgentCLICommandTimeout,
		outputLimit: defaultAgentCLIOutputLimit,
		lookPath:    exec.LookPath,
	}
}

func (runner *agentCLICommandRunner) Run(ctx context.Context, executable string, args []string, stdin string) (agentCLIResult, error) {
	result := agentCLIResult{ExitCode: -1}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	path, err := runner.lookPath(executable)
	if err != nil {
		return result, fmt.Errorf("resolve Agent CLI %q: %w", executable, errors.Join(ErrAgentCLIUnavailable, err))
	}
	result.Path = path
	commandCtx, cancel := context.WithTimeout(ctx, runner.timeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, path, args...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		return nil
	}
	stdout := newBoundedWriter(runner.outputLimit)
	stderr := newBoundedWriter(runner.outputLimit)
	command.Stdout = stdout
	command.Stderr = stderr
	if stdin != "" {
		command.Stdin = strings.NewReader(stdin)
	}
	if err := command.Start(); err != nil {
		return result, fmt.Errorf("start Agent CLI %q: %w", executable, errors.Join(ErrAgentCLIUnavailable, err))
	}
	result.Started = true
	err = command.Wait()
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	result.StdoutTruncated = stdout.Truncated()
	result.StderrTruncated = stderr.Truncated()
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
	}
	if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
		result.TimedOut = true
		return result, fmt.Errorf("Agent CLI %q timed out after %s: %w", executable, runner.timeout, context.DeadlineExceeded)
	}
	if err != nil {
		return result, fmt.Errorf("Agent CLI %q exited with code %d: %w", executable, result.ExitCode, err)
	}
	return result, nil
}

type boundedWriter struct {
	remaining int
	contents  strings.Builder
	truncated bool
}

func newBoundedWriter(limit int) *boundedWriter {
	if limit < 0 {
		limit = 0
	}
	return &boundedWriter{remaining: limit}
}

func (writer *boundedWriter) Write(contents []byte) (int, error) {
	original := len(contents)
	if len(contents) > writer.remaining {
		writer.truncated = true
		contents = contents[:writer.remaining]
	}
	if len(contents) > 0 {
		_, _ = io.WriteString(&writer.contents, string(contents))
		writer.remaining -= len(contents)
	}
	return original, nil
}

func (writer *boundedWriter) String() string {
	return writer.contents.String()
}

func (writer *boundedWriter) Truncated() bool {
	return writer.truncated
}

var _ agentCLIRunner = (*agentCLICommandRunner)(nil)
