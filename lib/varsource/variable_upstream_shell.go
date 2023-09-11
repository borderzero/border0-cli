package varsource

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
)

// variableUpstream implementation for fetching values from environment variables
type shellVariableUpstream struct{}

// ensure shellVariableUpstream implements variableUpstream at compile-time
var _ variableUpstream = (*shellVariableUpstream)(nil)

// GetVariable gets a variable from the environment
func (vg *shellVariableUpstream) GetVariable(ctx context.Context, varDefn string) (string, error) {
	shellScript := varDefn

	// Split the command string into the command and its arguments
	parts := strings.Fields(shellScript)
	command := parts[0]
	args := parts[1:]

	// Create a new command with context
	cmd := exec.CommandContext(ctx, command, args...)

	// Create a buffer to capture the command's standard output
	var stdout bytes.Buffer

	// Redirect the command's output to the buffer
	cmd.Stdout = &stdout

	// Execute the command
	err := cmd.Run()

	// Check for errors
	if err != nil {
		return "", errors.New("failed to execute command: " + err.Error())
	}

	// Capture the command's output as a string
	// and remove the trailing newline
	output := strings.TrimSuffix(stdout.String(), "\n")

	// Return the output
	return output, nil

}
