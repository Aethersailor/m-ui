package mihomo

import (
	"os"
	"strings"
	"testing"
)

func TestRuntimeLifecycleMarkerProbeDistinguishesLiveAndStaleOwners(t *testing.T) {
	markerPath := t.TempDir() + "/mihomo-lifecycle.marker"
	cleanup, err := beginRuntimeLifecycleMarkerAt(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	probe, live, err := ProbeRuntimeLifecycleMarkerAt(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if probe != nil || !live {
		t.Fatalf("live probe = (%#v, %v), want (nil, true)", probe, live)
	}
	cleanup()

	if err := os.WriteFile(markerPath, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	probe, live, err = ProbeRuntimeLifecycleMarkerAt(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if probe == nil || live {
		t.Fatalf("stale probe = (%#v, %v), want (probe, false)", probe, live)
	}
	if err := probe.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("persistent marker stat error = %v", err)
	}
}

func TestBeginRuntimeLifecycleMarkerRejectsLiveOwner(t *testing.T) {
	markerPath := t.TempDir() + "/mihomo-lifecycle.marker"
	cleanup, err := beginRuntimeLifecycleMarkerAt(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	_, err = beginRuntimeLifecycleMarkerAt(markerPath)
	if err == nil || !strings.Contains(err.Error(), "held by another owner") {
		t.Fatalf("second marker begin error = %v, want live-owner error", err)
	}
}

func TestRuntimeLifecycleMarkerAllowsConcurrentObserversWithoutUnlinking(t *testing.T) {
	markerPath := t.TempDir() + "/mihomo-lifecycle.marker"
	if err := os.WriteFile(markerPath, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	first, live, err := ProbeRuntimeLifecycleMarkerAt(markerPath)
	if err != nil || live || first == nil {
		t.Fatalf("first stale probe = (%#v, %v, %v)", first, live, err)
	}
	second, live, err := ProbeRuntimeLifecycleMarkerAt(markerPath)
	if err != nil || live || second == nil {
		t.Fatalf("second stale probe = (%#v, %v, %v)", second, live, err)
	}
	if _, err := beginRuntimeLifecycleMarkerAt(markerPath); err == nil {
		t.Fatal("marker owner acquired while shared observers were active")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}

	cleanup, err := beginRuntimeLifecycleMarkerAt(markerPath)
	if err != nil {
		t.Fatalf("begin after observers = %v", err)
	}
	cleanup()
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("persistent marker stat error = %v", err)
	}
}
