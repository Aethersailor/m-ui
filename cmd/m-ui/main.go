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
	"time"

	"github.com/Aethersailor/m-ui/internal/app"
	"github.com/Aethersailor/m-ui/internal/config"
	"github.com/Aethersailor/m-ui/internal/version"
)

const usage = `m-ui manages one dedicated Mihomo service.

Usage:
  m-ui server [--config /etc/m-ui/config.toml]
  m-ui version
  m-ui admin reset-password --password-file <path> [--username admin]
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
	case "admin":
		return runAdmin(args[1:])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runAdmin(args []string) error {
	if len(args) == 0 || args[0] != "reset-password" {
		fmt.Fprint(os.Stderr, usage)
		return errors.New("admin requires the reset-password command")
	}
	flags := flag.NewFlagSet("admin reset-password", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", "", "path to the m-ui TOML configuration")
	username := flags.String("username", "admin", "administrator username")
	passwordFile := flags.String(
		"password-file",
		"",
		"path to a file containing the new password",
	)
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("admin reset-password accepts no positional arguments")
	}
	if *passwordFile == "" {
		return errors.New("--password-file is required")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	created, err := app.ResetAdminPassword(
		ctx,
		cfg,
		*username,
		*passwordFile,
	)
	if err != nil {
		return err
	}
	if created {
		fmt.Println("Administrator created.")
	} else {
		fmt.Println("Administrator password updated and sessions revoked.")
	}
	return nil
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
