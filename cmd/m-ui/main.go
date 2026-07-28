package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Aethersailor/m-ui/internal/app"
	"github.com/Aethersailor/m-ui/internal/config"
	"github.com/Aethersailor/m-ui/internal/version"
)

const usage = `m-ui manages one dedicated Mihomo service.

Usage:
  m-ui server [--config /etc/m-ui/config.toml]
  m-ui version
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("m-ui stopped", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return errors.New("a command is required")
	}

	switch args[0] {
	case "server":
		return runServer(args[1:])
	case "version":
		fmt.Println(version.Current().String())
		return nil
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runServer(args []string) error {
	flags := flag.NewFlagSet("server", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", "", "path to the m-ui TOML configuration")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("server accepts no positional arguments")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	return app.Run(ctx, cfg, version.Current())
}
