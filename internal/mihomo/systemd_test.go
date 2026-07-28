package mihomo

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type recordedCommand struct {
	name      string
	arguments []string
}

type fakeCommandExecutor struct {
	commands []recordedCommand
	output   []byte
	err      error
}

func (executor *fakeCommandExecutor) Run(
	_ context.Context,
	_ time.Duration,
	_ int,
	name string,
	arguments ...string,
) ([]byte, error) {
	executor.commands = append(executor.commands, recordedCommand{
		name:      name,
		arguments: append([]string(nil), arguments...),
	})
	return executor.output, executor.err
}

func TestSystemdProcessUsesFixedNonInteractiveLifecycleCommands(t *testing.T) {
	t.Parallel()
	process, err := NewSystemdProcess(managedServiceName)
	if err != nil {
		t.Fatal(err)
	}
	executor := &fakeCommandExecutor{}
	process.executor = executor

	for _, operation := range []struct {
		action string
		run    func(context.Context) error
	}{
		{"start", process.Start},
		{"stop", process.Stop},
		{"restart", process.Restart},
		{"reload", process.Reload},
	} {
		if err := operation.run(context.Background()); err != nil {
			t.Fatalf("%s() error = %v", operation.action, err)
		}
		last := executor.commands[len(executor.commands)-1]
		expected := recordedCommand{
			name:      "sudo",
			arguments: []string{"-n", "systemctl", operation.action, managedServiceName},
		}
		if !reflect.DeepEqual(last, expected) {
			t.Fatalf("%s command = %#v, want %#v", operation.action, last, expected)
		}
	}
}

func TestSystemdProcessRejectsOtherServiceNames(t *testing.T) {
	t.Parallel()
	if _, err := NewSystemdProcess("other.service"); err == nil {
		t.Fatal("NewSystemdProcess() error = nil")
	}
}

func TestSystemdProcessStatusAndBoundedLogs(t *testing.T) {
	t.Parallel()
	process, err := NewSystemdProcess(managedServiceName)
	if err != nil {
		t.Fatal(err)
	}
	executor := &fakeCommandExecutor{}
	process.executor = executor
	active, err := process.IsActive(context.Background())
	if err != nil || !active {
		t.Fatalf("IsActive() = %v, %v", active, err)
	}
	executor.err = errCommandExit
	active, err = process.IsActive(context.Background())
	if err != nil || active {
		t.Fatalf("inactive IsActive() = %v, %v", active, err)
	}
	executor.err = nil
	executor.output = []byte(
		"first 2b26a842-8bd1-493a-978b-ee5e546cf508\nsecond\n",
	)
	logs, err := process.RecentLogs(context.Background(), 2)
	if err != nil || len(logs) != 2 || logs[1].Message != "second" {
		t.Fatalf("RecentLogs() = %#v, %v", logs, err)
	}
	if strings.Contains(logs[0].Message, "2b26a842-8bd1-493a-978b-ee5e546cf508") {
		t.Fatalf("RecentLogs() leaked UUID: %#v", logs)
	}
	if _, err := process.RecentLogs(context.Background(), 0); err == nil {
		t.Fatal("RecentLogs() error = nil for invalid limit")
	}
	executor.err = errors.New("unexpected execution failure")
	if err := process.Restart(context.Background()); err == nil {
		t.Fatal("Restart() error = nil")
	}
}
