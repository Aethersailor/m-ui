package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/Aethersailor/m-ui/internal/app"
	"github.com/Aethersailor/m-ui/internal/config"
	"github.com/Aethersailor/m-ui/internal/version"
	"golang.org/x/term"
)

const usage = `m-ui manages one dedicated Mihomo service.

Usage:
  m-ui server [--config /etc/m-ui/config.toml]
  m-ui version
  m-ui status
  m-ui update|reinstall|uninstall|purge [options]
  m-ui doctor [panel|database] [--config /etc/m-ui/config.toml]
  m-ui admin reset-password [--password-file PATH] [--username admin]
  m-ui core status [--json] [--config /etc/m-ui/config.toml]
  m-ui core check [--json] [--config /etc/m-ui/config.toml]
  m-ui core update [--json] [--config /etc/m-ui/config.toml]
  m-ui core rollback [--json] [--config /etc/m-ui/config.toml]
`

const runtimeCommandTimeout = 120 * time.Second

const managementScriptPath = "/usr/lib/m-ui/manage.sh"

var managementCommand = exec.Command

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

	if isManagementCommand(args[0]) {
		return runManagement(args)
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
	case "runtime":
		return runRuntime(args[1:])
	case "core":
		return runCore(args[1:])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func isManagementCommand(command string) bool {
	switch command {
	case "status", "update", "reinstall", "uninstall", "purge":
		return true
	default:
		return false
	}
}

// runManagement keeps the installed command surface small without giving the
// shell script control over arbitrary command construction. The script still
// owns package lifecycle behavior; the Go binary only passes the already
// parsed argv directly to it.
func runManagement(args []string) error {
	if len(args) == 0 {
		return errors.New("management command is required")
	}
	command := managementCommand(managementScriptPath, args...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("m-ui %s failed: %w", args[0], err)
	}
	return nil
}

func runRuntime(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return errors.New(
			"runtime requires apply-mihomo-start, restart-mihomo, finalize-mihomo-start, or wait-ready",
		)
	}
	command := args[0]
	flags := flag.NewFlagSet("runtime "+command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String(
		"config",
		"",
		"path to the m-ui TOML configuration",
	)
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("runtime commands accept no positional arguments")
	}
	if command == "wait-ready" {
		ctx, cancel := context.WithTimeout(context.Background(), runtimeCommandTimeout)
		defer cancel()
		return app.WaitForRuntimeReady(ctx)
	}
	action := ""
	switch command {
	case "apply-mihomo-start":
		action = "start"
	case "restart-mihomo":
		action = "restart"
	case "finalize-mihomo-start":
		action = "finalize"
	default:
		return fmt.Errorf("unknown runtime command %q", command)
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	// Every restricted runtime command may need to wait for m-ui startup
	// recovery before it can touch the runtime coordinator. Keep this longer
	// than Publisher recovery plus the Mihomo health window; the native
	// finalizer and the manage/package callers must share the same bound.
	runtimeTimeout := runtimeCommandTimeout
	ctx, cancel := context.WithTimeout(context.Background(), runtimeTimeout)
	defer cancel()
	return app.ApplyMihomoRuntime(ctx, cfg, action)
}

func runCore(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return errors.New("core requires status, check, update, rollback, or bootstrap")
	}
	command := args[0]
	flags := flag.NewFlagSet("core "+command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", "", "path to the m-ui TOML configuration")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	binaryPath := flags.String(
		"binary",
		"/usr/lib/m-ui/bootstrap/mihomo",
		"verified bootstrap Mihomo binary",
	)
	manifestPath := flags.String(
		"manifest",
		"/usr/share/m-ui/bootstrap/manifest.json",
		"verified bootstrap Mihomo manifest",
	)
	channelValue := flags.String(
		"channel",
		"",
		"managed core channel: release or alpha (preserve current when omitted)",
	)
	autoUpdateValue := flags.String(
		"auto-update",
		"",
		"managed core automatic update: on or off (preserve current when omitted)",
	)
	checkIntervalValue := flags.String(
		"check-interval",
		"",
		"managed core check interval: 6h, 12h, 24h, or 168h (preserve current when omitted)",
	)
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("core commands accept no positional arguments")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	timeout := 60 * time.Second
	if command == "update" {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var output any
	switch command {
	case "status":
		output, err = app.CoreStatus(ctx, cfg)
	case "check":
		output, err = app.CoreCheck(ctx, cfg)
	case "update":
		var changed bool
		var manifest any
		manifest, changed, err = app.CoreUpdate(ctx, cfg)
		output = struct {
			Changed  bool `json:"changed"`
			Manifest any  `json:"manifest"`
		}{Changed: changed, Manifest: manifest}
	case "rollback":
		output, err = app.CoreRollback(ctx, cfg)
	case "bootstrap":
		var changed bool
		var manifest any
		manifest, changed, err = app.CoreBootstrap(
			ctx,
			cfg,
			*binaryPath,
			*manifestPath,
		)
		output = struct {
			Changed  bool `json:"changed"`
			Manifest any  `json:"manifest"`
		}{Changed: changed, Manifest: manifest}
		if err == nil {
			err = app.ConfigureCoreOptions(
				ctx,
				cfg,
				*channelValue,
				*autoUpdateValue,
				*checkIntervalValue,
			)
		}
	default:
		return fmt.Errorf("unknown core command %q", command)
	}
	if err != nil {
		return err
	}
	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(output)
	}
	switch value := output.(type) {
	case interface{ String() string }:
		fmt.Println(value.String())
	default:
		encoded, encodeErr := json.MarshalIndent(output, "", "  ")
		if encodeErr != nil {
			return encodeErr
		}
		fmt.Println(string(encoded))
	}
	return nil
}

func runDoctor(args []string) error {
	panelOnly := len(args) > 0 && args[0] == "panel"
	databaseOnly := len(args) > 0 && args[0] == "database"
	if panelOnly || databaseOnly {
		args = args[1:]
	}
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", "", "path to the m-ui TOML configuration")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("doctor accepts only the optional panel argument")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if panelOnly {
		if err := app.PanelHealth(ctx, cfg); err != nil {
			return err
		}
		fmt.Println("m-ui panel health is OK.")
		return nil
	}
	if databaseOnly {
		if err := app.DatabaseHealth(ctx, cfg); err != nil {
			return err
		}
		fmt.Println("m-ui database health is OK.")
		return nil
	}
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
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return errors.New("admin requires reset-password")
	}
	command := args[0]
	if command != "reset-password" {
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown admin command %q", command)
	}
	flags := flag.NewFlagSet("admin "+command, flag.ContinueOnError)
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
		return errors.New("admin commands accept no positional arguments")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var created bool
	if *passwordFile != "" {
		created, err = app.ResetAdminPassword(ctx, cfg, *username, *passwordFile)
	} else {
		password, passwordErr := readConfirmedPassword(os.Stdin, os.Stderr)
		if passwordErr != nil {
			return passwordErr
		}
		created, err = app.ResetAdminPasswordValue(ctx, cfg, *username, password)
	}
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

func readConfirmedPassword(input *os.File, output io.Writer) (string, error) {
	fd := int(input.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New(
			"interactive password reset requires a terminal; use --password-file for automation",
		)
	}
	return promptForPassword(fd, output, term.ReadPassword)
}

func promptForPassword(
	fd int,
	output io.Writer,
	readPassword func(int) ([]byte, error),
) (string, error) {
	if _, err := fmt.Fprint(output, "New administrator password: "); err != nil {
		return "", fmt.Errorf("write administrator password prompt: %w", err)
	}
	first, err := readPassword(fd)
	if _, outputErr := fmt.Fprintln(output); outputErr != nil && err == nil {
		return "", fmt.Errorf("finish administrator password prompt: %w", outputErr)
	}
	if err != nil {
		return "", fmt.Errorf("read administrator password: %w", err)
	}
	if _, err := fmt.Fprint(output, "Confirm administrator password: "); err != nil {
		return "", fmt.Errorf("write administrator confirmation prompt: %w", err)
	}
	second, err := readPassword(fd)
	if _, outputErr := fmt.Fprintln(output); outputErr != nil && err == nil {
		return "", fmt.Errorf("finish administrator confirmation prompt: %w", outputErr)
	}
	if err != nil {
		return "", fmt.Errorf("confirm administrator password: %w", err)
	}
	if string(first) != string(second) {
		return "", errors.New("administrator passwords do not match")
	}
	return string(first), nil
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
