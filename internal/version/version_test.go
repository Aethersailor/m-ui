package version

import "testing"

func TestDisplayCommitShortensHexIdentity(t *testing.T) {
	t.Parallel()

	if got, want := displayCommit("0123456789abcdef0123456789abcdef01234567"), "01234567"; got != want {
		t.Fatalf("displayCommit() = %q, want %q", got, want)
	}
}

func TestDisplayCommitPreservesShortAndDescriptiveValues(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"unknown", "dirty-worktree"} {
		if got := displayCommit(value); got != value {
			t.Fatalf("displayCommit(%q) = %q", value, got)
		}
	}
}
