package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const DefaultPath = "/etc/m-ui/config.toml"

type Config struct {
	Server   Server   `toml:"server"`
	Logging  Logging  `toml:"logging"`
	Storage  Storage  `toml:"storage"`
	Security Security `toml:"security"`
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
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate %s: %w", path, err)
	}
	return cfg, nil
}

func (c Config) Validate() error {
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
	return nil
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
