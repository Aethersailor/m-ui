package redact

import (
	"strings"
	"testing"
)

func TestTextRemovesSensitiveIdentifiersAndCredentials(t *testing.T) {
	t.Parallel()
	sensitive := []string{
		"2b26a842-8bd1-493a-978b-ee5e546cf508",
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"controller-secret-value",
		"vless://example-sensitive-share",
		"trojan-password-value",
		"vmess-mkcp-seed-value",
	}
	input := "uuid=" + sensitive[0] +
		" private-key: " + sensitive[1] +
		` "secret": "` + sensitive[2] + `"` +
		" uri=" + sensitive[3] +
		" password: " + sensitive[4] +
		" seed: " + sensitive[5]
	output := Text(input)
	for _, value := range sensitive {
		if strings.Contains(output, value) {
			t.Fatalf("Text() leaked %q in %q", value, output)
		}
	}
}
