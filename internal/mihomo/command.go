package mihomo

import (
	"context"
	"errors"
	"os/exec"
	"time"
)

var errCommandExit = errors.New("command exited unsuccessfully")
var errCommandStart = errors.New("command could not be started")

type commandExecutor interface {
	Run(
		ctx context.Context,
		timeout time.Duration,
		limit int,
		name string,
		arguments ...string,
	) ([]byte, error)
}

type osCommandExecutor struct{}

func (osCommandExecutor) Run(
	ctx context.Context,
	timeout time.Duration,
	limit int,
	name string,
	arguments ...string,
) ([]byte, error) {
	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var output limitedBuffer
	output.limit = limit
	command := exec.CommandContext(commandContext, name, arguments...)
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		switch {
		case errors.Is(commandContext.Err(), context.DeadlineExceeded):
			return nil, context.DeadlineExceeded
		case errors.Is(output.err, errOutputLimit):
			return nil, errOutputLimit
		case errors.As(err, &exitError):
			return nil, errCommandExit
		default:
			return nil, errCommandStart
		}
	}
	return output.Bytes(), nil
}
