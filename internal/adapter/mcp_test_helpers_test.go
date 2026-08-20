package adapter

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"apm/internal/domain"
)

type recordedMCPCommand struct {
	executable string
	args       []string
	stdin      string
}

type queuedMCPResponse struct {
	result agentCLIResult
	err    error
}

type recordingMCPRunner struct {
	commands  []recordedMCPCommand
	responses []queuedMCPResponse
}

func (runner *recordingMCPRunner) Run(_ context.Context, executable string, args []string, stdin string) (agentCLIResult, error) {
	runner.commands = append(runner.commands, recordedMCPCommand{
		executable: executable,
		args:       append([]string(nil), args...),
		stdin:      stdin,
	})
	if len(runner.responses) == 0 {
		return agentCLIResult{Path: "/fake/" + executable, Started: true, ExitCode: 0}, nil
	}
	response := runner.responses[0]
	runner.responses = runner.responses[1:]
	return response.result, response.err
}

func successfulMCPResponse(stdout string) queuedMCPResponse {
	return queuedMCPResponse{result: agentCLIResult{Path: "/fake/agent", Stdout: stdout, Started: true, ExitCode: 0}}
}

func failedMCPResponse(stderr string) queuedMCPResponse {
	return queuedMCPResponse{
		result: agentCLIResult{Path: "/fake/agent", Stderr: stderr, Started: true, ExitCode: 1},
		err:    errors.New("exit status 1"),
	}
}

func truncatedMCPResponse(stdout string) queuedMCPResponse {
	response := successfulMCPResponse(stdout)
	response.result.StdoutTruncated = true
	return response
}

func mcpRef(name string) domain.PowerRef {
	return domain.PowerRef{Name: name, Type: domain.PowerTypeMCP, Version: "v1.0.0"}
}

func stdioServer(command string, args ...string) domain.MCPServer {
	return domain.MCPServer{Transport: domain.MCPTransportStdio, Command: command, Args: args}
}

func httpServer(url string) domain.MCPServer {
	return domain.MCPServer{Transport: domain.MCPTransportHTTP, URL: url}
}

func assertMCPCommands(t *testing.T, runner *recordingMCPRunner, executable string, args [][]string) {
	t.Helper()
	want := make([]recordedMCPCommand, len(args))
	for index, commandArgs := range args {
		want[index] = recordedMCPCommand{executable: executable, args: commandArgs}
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
}
