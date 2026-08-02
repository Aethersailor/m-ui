package main

import (
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
