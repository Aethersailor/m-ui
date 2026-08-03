package main

import (
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"
)

func TestRuntimeCommandTimeoutCoversStartupRecovery(t *testing.T) {
	const minimumRecoveryWindow = 55 * time.Second
	if runtimeCommandTimeout < minimumRecoveryWindow {
		t.Fatalf(
			"runtime command timeout = %s, want at least %s",
			runtimeCommandTimeout,
			minimumRecoveryWindow,
		)
	}
	if runtimeCommandTimeout != 120*time.Second {
		t.Fatalf("runtime command timeout = %s, want 120s", runtimeCommandTimeout)
	}
}

func TestManagementCommandAliasesRemainExplicit(t *testing.T) {
	for _, command := range []string{
		"status",
		"update",
		"reinstall",
		"uninstall",
		"purge",
	} {
		if !isManagementCommand(command) {
			t.Fatalf("%q is not registered as a management command", command)
		}
	}
	for _, command := range []string{"server", "version", "doctor", "admin", "config", "runtime", "core"} {
		if isManagementCommand(command) {
			t.Fatalf("%q must remain a native m-ui command", command)
		}
	}
}

func TestRunManagementPassesTheOriginalArgvToTheAbsoluteScript(t *testing.T) {
	original := managementCommand
	t.Cleanup(func() { managementCommand = original })

	var gotPath string
	var gotArgs []string
	managementCommand = func(path string, args ...string) *exec.Cmd {
		gotPath = path
		gotArgs = append([]string(nil), args...)
		command := exec.Command(os.Args[0], "-test.run=TestManagementCommandHelper")
		command.Env = append(os.Environ(), "M_UI_MANAGEMENT_HELPER=1")
		return command
	}

	if err := runManagement([]string{
		"reinstall",
		"--version",
		"v9.9.9",
		"--archive",
		"/tmp/release with spaces.tar.gz",
	}); err != nil {
		t.Fatalf("runManagement() error = %v", err)
	}
	if gotPath != managementScriptPath {
		t.Fatalf("management script path = %q, want %q", gotPath, managementScriptPath)
	}
	wantArgs := []string{
		"reinstall",
		"--version",
		"v9.9.9",
		"--archive",
		"/tmp/release with spaces.tar.gz",
	}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("management argv = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestManagementCommandHelper(t *testing.T) {
	if os.Getenv("M_UI_MANAGEMENT_HELPER") != "1" {
		return
	}
}
