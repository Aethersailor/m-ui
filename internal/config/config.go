package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Aethersailor/m-ui/internal/domain"
	"github.com/pelletier/go-toml/v2"
)

const DefaultPath = "/etc/m-ui/config.toml"

type Config struct {
	Server   Server   `toml:"server"`
	Logging  Logging  `toml:"logging"`
	Storage  Storage  `toml:"storage"`
	Security Security `toml:"security"`
	Panel    Panel    `toml:"panel"`
	Mihomo   Mihomo   `toml:"mihomo"`
}

type Server struct {
	ListenAddress     string `toml:"listen_address"`
	Port              uint16 `toml:"port"`
	ReadHeaderTimeout string `toml:"read_header_timeout"`
	ShutdownTimeout   string `toml:"shutdown_timeout"`
}

type Logging struct {
	Level  string `toml:"level"`
	Format string `toml:"format"`
}

type Storage struct {
	DatabasePath  string `toml:"database_path"`
	MasterKeyPath string `toml:"master_key_path"`
}

type Security struct {
	SessionTTL   string `toml:"session_ttl"`
	CookieSecure bool   `toml:"cookie_secure"`
}

type Panel struct {
	Title      string `toml:"title"`
	UILanguage string `toml:"ui_language"`
	PublicHost string `toml:"public_host"`
}

type Mihomo struct {
	BinaryPath                    string   `toml:"binary_path"`
	ManagedCore                   bool     `toml:"managed_core"`
	ProcessMode                   string   `toml:"process_mode"`
	ConfigDirectory               string   `toml:"config_directory"`
	ConfigPath                    string   `toml:"config_path"`
	ExternalControllerAddress     string   `toml:"external_controller_address"`
	ControllerConnectAddress      string   `toml:"controller_connect_address"`
	ExternalControllerCORSOrigins []string `toml:"external_controller_cors_origins"`
	ControllerAddress             string   `toml:"controller_address"` // Deprecated.
	ControllerSecret              string   `toml:"controller_secret"`
	ServiceName                   string   `toml:"service_name"`
	RevisionDirectory             string   `toml:"revision_directory"`
	HistoryLimit                  int      `toml:"history_limit"`
}

func Default() Config {
	return Config{
		Server: Server{
			ListenAddress:     "127.0.0.1",
			Port:              2095,
			ReadHeaderTimeout: "5s",
			ShutdownTimeout:   "10s",
		},
		Logging: Logging{
			Level:  "info",
			Format: "text",
		},
		Storage: Storage{
			DatabasePath:  "/var/lib/m-ui/m-ui.db",
			MasterKeyPath: "/var/lib/m-ui/master.key",
		},
		Security: Security{
			SessionTTL: "12h",
		},
		Panel: Panel{
			Title:      "m-ui",
			UILanguage: "auto",
			PublicHost: "localhost",
		},
		Mihomo: Mihomo{
			BinaryPath:                    "/var/lib/m-ui/core/current/mihomo",
			ManagedCore:                   true,
			ProcessMode:                   "auto",
			ConfigDirectory:               "/etc/mihomo",
			ConfigPath:                    "/etc/mihomo/config.yaml",
			ExternalControllerAddress:     "127.0.0.1:9090",
			ControllerConnectAddress:      "127.0.0.1:9090",
			ControllerAddress:             "127.0.0.1:9090",
			ExternalControllerCORSOrigins: []string{},
			ServiceName:                   "mihomo.service",
			RevisionDirectory:             "/var/lib/m-ui/revisions",
			HistoryLimit:                  20,
		},
	}
}

// Load overlays one TOML file on safe defaults. An empty path uses the default
// system location and tolerates a missing file so a fresh binary can expose its
// loopback health endpoint. An explicitly supplied path must exist.
func Load(path string) (Config, error) {
	cfg := Default()
	explicit := path != ""
	if !explicit {
		path = DefaultPath
	}

	content, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, os.ErrNotExist) && !explicit {
		return cfg, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	if err := toml.Unmarshal(content, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode %s: %w", path, err)
	}
	var fileEndpoints struct {
		Mihomo struct {
			ExternalControllerAddress *string `toml:"external_controller_address"`
			ControllerConnectAddress  *string `toml:"controller_connect_address"`
			ControllerAddress         *string `toml:"controller_address"`
		} `toml:"mihomo"`
	}
	if err := toml.Unmarshal(content, &fileEndpoints); err != nil {
		return Config{}, fmt.Errorf("decode endpoint compatibility fields in %s: %w", path, err)
	}
	if fileEndpoints.Mihomo.ControllerAddress != nil {
		legacyBind, legacyConnect, splitErr := splitLegacyControllerAddress(
			*fileEndpoints.Mihomo.ControllerAddress,
		)
		if splitErr != nil && (fileEndpoints.Mihomo.ExternalControllerAddress == nil ||
			fileEndpoints.Mihomo.ControllerConnectAddress == nil) {
			return Config{}, fmt.Errorf("migrate mihomo.controller_address: %w", splitErr)
		}
		if fileEndpoints.Mihomo.ExternalControllerAddress == nil {
			cfg.Mihomo.ExternalControllerAddress = legacyBind
		}
		if fileEndpoints.Mihomo.ControllerConnectAddress == nil {
			cfg.Mihomo.ControllerConnectAddress = legacyConnect
		}
	}
	if fileEndpoints.Mihomo.ExternalControllerAddress != nil &&
		fileEndpoints.Mihomo.ControllerConnectAddress == nil {
		bind, connect, splitErr := splitLegacyControllerAddress(
			*fileEndpoints.Mihomo.ExternalControllerAddress,
		)
		if splitErr != nil {
			return Config{}, fmt.Errorf("derive Mihomo controller connect endpoint: %w", splitErr)
		}
		cfg.Mihomo.ExternalControllerAddress = bind
		cfg.Mihomo.ControllerConnectAddress = connect
	}
	if fileEndpoints.Mihomo.ControllerConnectAddress != nil &&
		fileEndpoints.Mihomo.ExternalControllerAddress == nil {
		bind, connect, splitErr := splitLegacyControllerAddress(
			*fileEndpoints.Mihomo.ControllerConnectAddress,
		)
		if splitErr != nil {
			return Config{}, fmt.Errorf("derive Mihomo external-controller endpoint: %w", splitErr)
		}
		cfg.Mihomo.ExternalControllerAddress = bind
		cfg.Mihomo.ControllerConnectAddress = connect
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate %s: %w", path, err)
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.Mihomo.ExternalControllerAddress == "" ||
		c.Mihomo.ControllerConnectAddress == "" {
		legacyAddress := c.Mihomo.ControllerAddress
		if legacyAddress == "" {
			switch {
			case c.Mihomo.ExternalControllerAddress != "":
				legacyAddress = c.Mihomo.ExternalControllerAddress
			case c.Mihomo.ControllerConnectAddress != "":
				legacyAddress = c.Mihomo.ControllerConnectAddress
			}
		}
		if legacyAddress != "" {
			legacyBind, legacyConnect, err := splitLegacyControllerAddress(legacyAddress)
			if err != nil {
				return fmt.Errorf("migrate Mihomo controller endpoint: %w", err)
			}
			if c.Mihomo.ExternalControllerAddress == "" {
				c.Mihomo.ExternalControllerAddress = legacyBind
			}
			if c.Mihomo.ControllerConnectAddress == "" {
				c.Mihomo.ControllerConnectAddress = legacyConnect
			}
		}
	}
	if ip := net.ParseIP(c.Server.ListenAddress); ip == nil {
		return fmt.Errorf("server.listen_address must be an IP address")
	}
	if c.Server.Port == 0 {
		return fmt.Errorf("server.port must be between 1 and 65535")
	}
	if _, err := c.ReadHeaderTimeout(); err != nil {
		return err
	}
	if _, err := c.ShutdownTimeout(); err != nil {
		return err
	}

	switch strings.ToLower(c.Logging.Level) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("logging.level must be debug, info, warn, or error")
	}
	switch strings.ToLower(c.Logging.Format) {
	case "text", "json":
	default:
		return fmt.Errorf("logging.format must be text or json")
	}
	if strings.TrimSpace(c.Storage.DatabasePath) == "" {
		return fmt.Errorf("storage.database_path is required")
	}
	if strings.TrimSpace(c.Storage.MasterKeyPath) == "" {
		return fmt.Errorf("storage.master_key_path is required")
	}
	if _, err := c.SessionTTL(); err != nil {
		return err
	}
	if strings.TrimSpace(c.Panel.Title) == "" || len(c.Panel.Title) > 80 {
		return fmt.Errorf("panel.title must contain between 1 and 80 bytes")
	}
	switch c.Panel.UILanguage {
	case "auto", "en-US", "zh-CN":
	default:
		return fmt.Errorf("panel.ui_language must be auto, en-US, or zh-CN")
	}
	if err := validateHost(c.Panel.PublicHost); err != nil {
		return fmt.Errorf("panel.public_host: %w", err)
	}
	for _, item := range []struct {
		field string
		value string
	}{
		{"mihomo.binary_path", c.Mihomo.BinaryPath},
		{"mihomo.config_directory", c.Mihomo.ConfigDirectory},
		{"mihomo.config_path", c.Mihomo.ConfigPath},
		{"mihomo.revision_directory", c.Mihomo.RevisionDirectory},
	} {
		if !isConfiguredPathAbsolute(item.value) {
			return fmt.Errorf("%s must be an absolute path", item.field)
		}
	}
	if !configuredPathWithin(c.Mihomo.ConfigDirectory, c.Mihomo.ConfigPath) {
		return fmt.Errorf("mihomo.config_path must be inside mihomo.config_directory")
	}
	if err := validateEndpoint(
		"mihomo.external_controller_address",
		c.Mihomo.ExternalControllerAddress,
		false,
	); err != nil {
		return err
	}
	if err := validateEndpoint(
		"mihomo.controller_connect_address",
		c.Mihomo.ControllerConnectAddress,
		true,
	); err != nil {
		return err
	}
	bind, err := domain.ParseEndpoint(c.Mihomo.ExternalControllerAddress)
	if err != nil {
		return fmt.Errorf("mihomo.external_controller_address: %w", err)
	}
	connect, err := domain.ParseEndpoint(c.Mihomo.ControllerConnectAddress)
	if err != nil {
		return fmt.Errorf("mihomo.controller_connect_address: %w", err)
	}
	if err := domain.ValidateControllerEndpointPair(bind, connect); err != nil {
		return err
	}
	if err := validateCORSOrigins(c.Mihomo.ExternalControllerCORSOrigins); err != nil {
		return err
	}
	if c.Mihomo.ControllerSecret != "" {
		if strings.TrimSpace(c.Mihomo.ControllerSecret) != c.Mihomo.ControllerSecret ||
			len(c.Mihomo.ControllerSecret) < 32 ||
			len(c.Mihomo.ControllerSecret) > 128 {
			return fmt.Errorf(
				"mihomo.controller_secret must contain between 32 and 128 non-whitespace bytes",
			)
		}
	}
	if c.Mihomo.ServiceName != "mihomo.service" {
		return fmt.Errorf("mihomo.service_name must be mihomo.service")
	}
	switch c.Mihomo.ProcessMode {
	case "auto", "systemd", "openrc", "managed":
	default:
		return fmt.Errorf(
			"mihomo.process_mode must be auto, systemd, openrc, or managed",
		)
	}
	if c.Mihomo.ManagedCore &&
		filepath.Clean(c.Mihomo.BinaryPath) != filepath.Clean(
			"/var/lib/m-ui/core/current/mihomo",
		) {
		return fmt.Errorf(
			"mihomo.binary_path must use the managed core path when managed_core is true",
		)
	}
	if c.Mihomo.HistoryLimit < 1 || c.Mihomo.HistoryLimit > 100 {
		return fmt.Errorf("mihomo.history_limit must be between 1 and 100")
	}
	return nil
}

func splitLegacyControllerAddress(address string) (string, string, error) {
	endpoint, err := domain.ParseEndpoint(address)
	if err != nil {
		return "", "", err
	}
	bind, connect, err := domain.SplitLegacyControllerEndpoint(endpoint)
	if err != nil {
		return "", "", err
	}
	return bind.Address(), connect.Address(), nil
}

func (c Config) Address() string {
	return net.JoinHostPort(c.Server.ListenAddress, fmt.Sprint(c.Server.Port))
}

func (c Config) ReadHeaderTimeout() (time.Duration, error) {
	return positiveDuration("server.read_header_timeout", c.Server.ReadHeaderTimeout)
}

func (c Config) ShutdownTimeout() (time.Duration, error) {
	return positiveDuration("server.shutdown_timeout", c.Server.ShutdownTimeout)
}

func (c Config) SessionTTL() (time.Duration, error) {
	duration, err := positiveDuration("security.session_ttl", c.Security.SessionTTL)
	if err != nil {
		return 0, err
	}
	if duration > 24*time.Hour {
		return 0, fmt.Errorf("security.session_ttl must not exceed 24h")
	}
	return duration, nil
}

func positiveDuration(field, value string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s is invalid: %w", field, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s must be positive", field)
	}
	return duration, nil
}

func validateHost(value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return errors.New("host is required and must not have surrounding whitespace")
	}
	if net.ParseIP(value) != nil {
		return nil
	}
	if len(value) > 253 {
		return errors.New("host is too long")
	}
	for _, label := range strings.Split(strings.TrimSuffix(value, "."), ".") {
		if label == "" || len(label) > 63 ||
			strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return errors.New("host is not a valid DNS name")
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') &&
				(character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') &&
				character != '-' {
				return errors.New("host is not a valid DNS name")
			}
		}
	}
	return nil
}

func validateEndpoint(field, address string, loopbackOnly bool) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return fmt.Errorf("%s must use host:port syntax", field)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("%s host must be an IPv4 or IPv6 address", field)
	}
	if loopbackOnly && !ip.IsLoopback() {
		return fmt.Errorf("%s host must use a loopback address", field)
	}
	parsedPort, err := strconv.Atoi(port)
	if err != nil || parsedPort < 1 || parsedPort > 65535 {
		return fmt.Errorf("%s port must be between 1 and 65535", field)
	}
	return nil
}

func validateCORSOrigins(origins []string) error {
	for index, origin := range origins {
		parsed, err := url.Parse(origin)
		if origin == "" || origin == "*" || strings.TrimSpace(origin) != origin ||
			err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.Host == "" || parsed.User != nil || parsed.Path != "" ||
			parsed.RawQuery != "" || parsed.Fragment != "" ||
			strings.HasSuffix(parsed.Host, ":") ||
			net.ParseIP(parsed.Hostname()) == nil && !validDNSHost(parsed.Hostname()) {
			return fmt.Errorf("mihomo.external_controller_cors_origins[%d] must be an exact HTTP(S) origin", index)
		}
		if port := parsed.Port(); port != "" {
			parsedPort, err := strconv.Atoi(port)
			if err != nil || parsedPort < 1 || parsedPort > 65535 {
				return fmt.Errorf("mihomo.external_controller_cors_origins[%d] must be an exact HTTP(S) origin", index)
			}
		}
	}
	return nil
}

func validDNSHost(value string) bool {
	if value == "" || len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(value, "."), ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') &&
				(character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func isConfiguredPathAbsolute(value string) bool {
	return filepath.IsAbs(value) || path.IsAbs(value)
}

func configuredPathWithin(directory, file string) bool {
	if filepath.IsAbs(directory) && filepath.IsAbs(file) {
		relative, err := filepath.Rel(directory, file)
		return err == nil &&
			relative != ".." &&
			!strings.HasPrefix(relative, ".."+string(filepath.Separator))
	}
	if path.IsAbs(directory) && path.IsAbs(file) {
		directory = path.Clean(directory)
		file = path.Clean(file)
		if directory == "/" {
			return true
		}
		return file == directory ||
			strings.HasPrefix(file, strings.TrimSuffix(directory, "/")+"/")
	}
	return false
}
