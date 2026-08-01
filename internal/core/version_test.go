package core

import "testing"

func TestRuntimeVersionsMatchNormalizesCLIAndControllerOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		left, right string
		want        bool
	}{
		{left: "Mihomo Meta v1.19.29", right: "1.19.29", want: true},
		{left: "v1.19.29", right: "mihomo 1.19.29", want: true},
		{left: "1.19.29-alpha", right: "1.19.29", want: false},
		{left: "", right: "1.19.29", want: false},
	}
	for _, test := range tests {
		if got := runtimeVersionsMatch(test.left, test.right); got != test.want {
			t.Errorf("runtimeVersionsMatch(%q, %q) = %v, want %v", test.left, test.right, got, test.want)
		}
	}
}
