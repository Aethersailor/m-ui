package core

import (
	"regexp"
	"strings"
)

var runtimeVersionPattern = regexp.MustCompile(`(?i)(?:^|[^0-9a-z])((?:v)?[0-9]+\.[0-9]+\.[0-9]+(?:[-+._][0-9a-z.-]+)?|prerelease-alpha)(?:$|[^0-9a-z])`)

// normalizeRuntimeVersion reduces the different human-readable forms emitted
// by Mihomo's CLI and Controller to the same identity token.  If an upstream
// build uses a non-semver development label, the trimmed value remains the
// comparison token instead of being silently treated as equal to an empty
// value.
func normalizeRuntimeVersion(value string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "" {
		return ""
	}
	match := runtimeVersionPattern.FindStringSubmatch(trimmed)
	if len(match) == 2 {
		return strings.TrimPrefix(match[1], "v")
	}
	return strings.Join(strings.Fields(trimmed), " ")
}

func runtimeVersionsMatch(left, right string) bool {
	left = normalizeRuntimeVersion(left)
	right = normalizeRuntimeVersion(right)
	return left != "" && left == right
}
