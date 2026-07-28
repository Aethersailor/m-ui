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
  m-ui doctor [--config /etc/m-ui/config.toml]
  m-ui admin reset-password --password-file <path> [--username admin]
  m-ui config validate [--config /etc/m-ui/config.toml]
  m-ui config rollback --config /etc/m-ui/config.toml <revision-id>
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
	case "doctor":
		return runDoctor(args[1:])
	case "config":
		return runConfig(args[1:])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runDoctor(args []string) error {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", "", "path to the m-ui TOML configuration")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("doctor accepts no positional arguments")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return app.Doctor(ctx, cfg, os.Stdout)
}

func runConfig(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return errors.New("config requires validate or rollback")
	}
	flags := flag.NewFlagSet("config "+args[0], flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", "", "path to the m-ui TOML configuration")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	switch args[0] {
	case "validate":
		if flags.NArg() != 0 {
			return errors.New("config validate accepts no positional arguments")
		}
		hash, err := app.ValidateConfiguration(ctx, cfg)
		if err != nil {
			return err
		}
		fmt.Printf("Configuration is valid (sha256:%s).\n", hash)
		return nil
	case "rollback":
		if flags.NArg() != 1 {
			return errors.New("config rollback requires one revision ID")
		}
		revision, err := app.RollbackConfiguration(
			ctx,
			cfg,
			flags.Arg(0),
		)
		if err != nil {
			return err
		}
		fmt.Printf(
			"Rollback published as revision %d (%s).\n",
			revision.RevisionNumber,
			revision.ID,
		)
		return nil
	default:
		return fmt.Errorf("unknown config command %q", args[0])
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
