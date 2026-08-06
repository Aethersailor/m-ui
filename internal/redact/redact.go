package redact

import "regexp"

var patterns = []struct {
	expression  *regexp.Regexp
	replacement string
}{
	{
		regexp.MustCompile(`(?i)\bvless://[^\s"'<>]+`),
		"[redacted-vless-uri]",
	},
	{
		regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\b`),
		"[redacted-uuid]",
	},
	{
		regexp.MustCompile(`\b[A-Za-z0-9_-]{43}\b`),
		"[redacted-key]",
	},
	{
		regexp.MustCompile(`(?i)\b(secret|password|token|seed|private[-_ ]?key)["']?\s*[:=]\s*["']?[^"'\s,;]+`),
		"$1: [redacted]",
	},
	{
		regexp.MustCompile(`(?i)\bauthorization\s*:\s*Bearer\s+[^\s,;]+`),
		"Authorization: Bearer [redacted]",
	},
}

func Text(value string) string {
	for _, pattern := range patterns {
		value = pattern.expression.ReplaceAllString(value, pattern.replacement)
	}
	return value
}
