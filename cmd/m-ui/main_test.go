package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"
)

func TestPromptForPasswordConfirmsWithoutEchoingValue(t *testing.T) {
	answers := [][]byte{[]byte("correct horse battery staple"), []byte("correct horse battery staple")}
	var calls int
	var output bytes.Buffer
	password, err := promptForPassword(7, &output, func(fd int) ([]byte, error) {
		if fd != 7 {
			t.Fatalf("fd = %d, want 7", fd)
		}
		answer := answers[calls]
		calls++
		return answer, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if password != "correct horse battery staple" {
		t.Fatalf("password = %q", password)
	}
	if bytes.Contains(output.Bytes(), []byte(password)) {
		t.Fatal("password was written to the prompt output")
	}
}

func TestPromptForPasswordRejectsMismatchAndReadFailure(t *testing.T) {
	for _, test := range []struct {
		name    string
		answers [][]byte
		readErr error
	}{
		{name: "mismatch", answers: [][]byte{[]byte("first password"), []byte("second password")}},
		{name: "read failure", readErr: errors.New("terminal closed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls int
			_, err := promptForPassword(0, &bytes.Buffer{}, func(int) ([]byte, error) {
				if test.readErr != nil {
					return nil, test.readErr
				}
				answer := test.answers[calls]
				calls++
				return answer, nil
			})
			if err == nil {
				t.Fatal("promptForPassword() error = nil")
			}
		})
	}
}

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

func TestNormalHelpHidesDeploymentInternals(t *testing.T) {
	for _, internalCommand := range []string{
		"runtime finalize-mihomo-start",
		"runtime apply-mihomo-start",
		"core bootstrap",
		"rotate-setup-token",
	} {
		if bytes.Contains([]byte(usage), []byte(internalCommand)) {
			t.Fatalf("normal help exposes internal command %q", internalCommand)
		}
	}
	if !bytes.Contains([]byte(usage), []byte("admin reset-password [--password-file PATH]")) {
		t.Fatal("normal help does not advertise interactive password recovery")
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
