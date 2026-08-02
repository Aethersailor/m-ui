package mihomo

import (
	"context"
	"reflect"
	"testing"
)

func TestOpenRCProcessUsesFixedNonInteractiveLifecycleCommands(t *testing.T) {
	t.Parallel()
	process, err := NewOpenRCProcess(managedServiceName)
	if err != nil {
		t.Fatal(err)
	}
	executor := &fakeCommandExecutor{}
	process.executor = executor
	process.lifecycleMarker = func(action func() error) error { return action() }

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
			name: doasPath,
			arguments: []string{
				"-n",
				rcServicePath,
				openRCServiceName,
				operation.action,
			},
		}
		if !reflect.DeepEqual(last, expected) {
			t.Fatalf("%s command = %#v, want %#v", operation.action, last, expected)
		}
	}
}

func TestOpenRCProcessRejectsArbitraryServiceName(t *testing.T) {
	t.Parallel()
	if _, err := NewOpenRCProcess("other"); err == nil {
		t.Fatal("NewOpenRCProcess() error = nil")
	}
}

func TestOpenRCProcessStatusFallsBackToProcessIdentity(t *testing.T) {
	t.Parallel()
	process, err := newOpenRCProcess(
		managedServiceName,
		"/var/lib/m-ui/core/current/mihomo",
		"/etc/mihomo/config.yaml",
	)
	if err != nil {
		t.Fatal(err)
	}
	executor := &fakeCommandExecutor{err: errCommandExit}
	process.executor = executor
	called := false
	process.processActive = func(
		context.Context,
		string,
		string,
	) (bool, error) {
		called = true
		return true, nil
	}

	active, err := process.IsActive(context.Background())
	if err != nil || !active {
		t.Fatalf("IsActive() = %v, %v", active, err)
	}
	if !called {
		t.Fatal("IsActive() did not inspect the process identity")
	}
}
