package mihomo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/Aethersailor/m-ui/internal/domain"
)

const (
	defaultCommandTimeout = 10 * time.Second
	defaultOutputLimit    = 16 * 1024
)

var errOutputLimit = errors.New("command output exceeded the configured limit")

type CLI struct {
	binaryPath  string
	timeout     time.Duration
	outputLimit int
}

func NewCLI(binaryPath string) (*CLI, error) {
	if strings.TrimSpace(binaryPath) == "" {
		return nil, errors.New("Mihomo binary path is required")
	}
	return &CLI{
		binaryPath:  binaryPath,
		timeout:     defaultCommandTimeout,
		outputLimit: defaultOutputLimit,
	}, nil
}

func (cli *CLI) GenerateRealityKeypair(ctx context.Context) (domain.Keypair, error) {
	commandContext, cancel := context.WithTimeout(ctx, cli.timeout)
	defer cancel()

	var output limitedBuffer
	output.limit = cli.outputLimit
	command := exec.CommandContext(commandContext, cli.binaryPath, "generate", "reality-keypair")
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		if errors.Is(commandContext.Err(), context.DeadlineExceeded) {
			return domain.Keypair{}, errors.New("Mihomo REALITY key generation timed out")
		}
		if errors.Is(output.err, errOutputLimit) {
			return domain.Keypair{}, errors.New("Mihomo REALITY key generation output is too large")
		}
		return domain.Keypair{}, errors.New("Mihomo REALITY key generation failed")
	}
	keypair, err := parseRealityKeypair(output.Bytes())
	if err != nil {
		return domain.Keypair{}, err
	}
	return keypair, nil
}

func parseRealityKeypair(output []byte) (domain.Keypair, error) {
	var keypair domain.Keypair
	for line := range strings.SplitSeq(string(output), "\n") {
		label, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		normalizedLabel := strings.ToLower(strings.Join(strings.Fields(label), ""))
		switch normalizedLabel {
		case "privatekey":
			if keypair.PrivateKey != "" {
				return domain.Keypair{}, errors.New("Mihomo REALITY key output contains a duplicate private key")
			}
			keypair.PrivateKey = strings.TrimSpace(value)
		case "publickey":
			if keypair.PublicKey != "" {
				return domain.Keypair{}, errors.New("Mihomo REALITY key output contains a duplicate public key")
			}
			keypair.PublicKey = strings.TrimSpace(value)
		}
	}
	if keypair.PrivateKey == "" || keypair.PublicKey == "" {
		return domain.Keypair{}, errors.New("Mihomo REALITY key output format is not recognized")
	}
	if err := domain.ValidateRealityKey(keypair.PrivateKey); err != nil {
		return domain.Keypair{}, fmt.Errorf("Mihomo returned an invalid REALITY private key: %w", err)
	}
	if err := domain.ValidateRealityKey(keypair.PublicKey); err != nil {
		return domain.Keypair{}, fmt.Errorf("Mihomo returned an invalid REALITY public key: %w", err)
	}
	return keypair, nil
}

type limitedBuffer struct {
	buffer bytes.Buffer
	limit  int
	err    error
}

func (buffer *limitedBuffer) Write(value []byte) (int, error) {
	if buffer.err != nil {
		return 0, buffer.err
	}
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining <= 0 {
		buffer.err = errOutputLimit
		return 0, buffer.err
	}
	if len(value) > remaining {
		_, _ = buffer.buffer.Write(value[:remaining])
		buffer.err = errOutputLimit
		return remaining, buffer.err
	}
	return buffer.buffer.Write(value)
}

func (buffer *limitedBuffer) Bytes() []byte {
	return buffer.buffer.Bytes()
}

var _ io.Writer = (*limitedBuffer)(nil)
