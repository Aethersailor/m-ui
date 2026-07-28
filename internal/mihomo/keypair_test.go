package mihomo

import (
	"strings"
	"testing"
)

func TestParseRealityKeypairToleratesWhitespaceVariations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		output string
	}{
		{
			name: "compact labels",
			output: "\nPrivateKey: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n" +
				"PublicKey: AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE\n",
		},
		{
			name: "spaced labels and values",
			output: "  Private Key :   AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA  \r\n" +
				"\tPublic Key:\tAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE \r\n",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			keypair, err := parseRealityKeypair([]byte(test.output))
			if err != nil {
				t.Fatalf("parseRealityKeypair() error = %v", err)
			}
			if keypair.PrivateKey != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" {
				t.Fatalf("private key was not parsed")
			}
			if keypair.PublicKey != "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE" {
				t.Fatalf("public key was not parsed")
			}
		})
	}
}

func TestParseRealityKeypairRejectsUnknownOrInvalidOutputWithoutEchoingSecrets(t *testing.T) {
	t.Parallel()
	secret := "this-must-not-appear-in-the-error"
	for _, output := range []string{
		"unrelated output " + secret,
		"PrivateKey: " + secret + "\nPublicKey: also-invalid",
	} {
		_, err := parseRealityKeypair([]byte(output))
		if err == nil {
			t.Fatal("parseRealityKeypair() error = nil")
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaks command output: %v", err)
		}
	}
}

func TestLimitedBufferRejectsOversizedOutput(t *testing.T) {
	t.Parallel()
	buffer := limitedBuffer{limit: 4}
	written, err := buffer.Write([]byte("oversized"))
	if err == nil || written != 4 || string(buffer.Bytes()) != "over" {
		t.Fatalf("Write() = (%d, %v), content %q", written, err, buffer.Bytes())
	}
}
