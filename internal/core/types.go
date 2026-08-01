package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

type Repository interface {
	CoreSettings(context.Context) (Settings, error)
	UpdateCoreSettings(context.Context, Settings, time.Time) error
	CoreState(context.Context) (State, error)
	SaveCoreState(context.Context, State) error
	CoreSystemDegraded(context.Context) (bool, error)
	MarkDegraded(context.Context, string, string, time.Time) error
	RecordCoreAudit(
		context.Context,
		string,
		string,
		string,
		string,
		time.Time,
	) error
}

type Upstream interface {
	Resolve(context.Context, Channel, string) (ReleaseIdentity, error)
	Download(context.Context, ReleaseIdentity, io.Writer) (string, int64, error)
}

type Channel string

const (
	ChannelRelease Channel = "release"
	ChannelAlpha   Channel = "alpha"

	DefaultCheckInterval = 24 * time.Hour
	ManagedBinaryPath    = "/var/lib/m-ui/core/current/mihomo"
)

var AllowedCheckIntervals = []time.Duration{
	6 * time.Hour,
	12 * time.Hour,
	24 * time.Hour,
	168 * time.Hour,
}

func ParseChannel(value string) (Channel, error) {
	channel := Channel(strings.ToLower(strings.TrimSpace(value)))
	switch channel {
	case ChannelRelease, ChannelAlpha:
		return channel, nil
	default:
		return "", errors.New("core channel must be release or alpha")
	}
}

func ValidateCheckInterval(value time.Duration) error {
	for _, allowed := range AllowedCheckIntervals {
		if value == allowed {
			return nil
		}
	}
	return errors.New("core check interval must be 6h, 12h, 24h, or 168h")
}

type ReleaseIdentity struct {
	Channel               Channel   `json:"channel"`
	Repository            string    `json:"repository"`
	ReleaseID             int64     `json:"release_id"`
	TagName               string    `json:"tag_name"`
	Prerelease            bool      `json:"prerelease"`
	PublishedAt           time.Time `json:"published_at"`
	TargetCommitish       string    `json:"target_commitish,omitempty"`
	AssetID               int64     `json:"asset_id"`
	AssetName             string    `json:"asset_name"`
	AssetSize             int64     `json:"asset_size"`
	AssetDigestSHA256     string    `json:"asset_digest_sha256"`
	BrowserDownloadURL    string    `json:"-"`
	BinaryReportedVersion string    `json:"binary_reported_version,omitempty"`
}

func (identity ReleaseIdentity) Validate() error {
	switch {
	case identity.Channel != ChannelRelease && identity.Channel != ChannelAlpha:
		return errors.New("release identity channel is invalid")
	case identity.Repository != UpstreamRepository:
		return errors.New("release identity repository is invalid")
	case identity.ReleaseID <= 0:
		return errors.New("release identity release ID is invalid")
	case strings.TrimSpace(identity.TagName) == "":
		return errors.New("release identity tag is required")
	case identity.PublishedAt.IsZero():
		return errors.New("release identity publication time is invalid")
	case identity.Channel == ChannelRelease && identity.Prerelease:
		return errors.New("release channel identity must not be prerelease")
	case identity.Channel == ChannelAlpha && identity.TagName != AlphaTag:
		return errors.New("alpha channel identity must use Prerelease-Alpha")
	case identity.Channel == ChannelAlpha && !identity.Prerelease:
		return errors.New("alpha channel identity must be prerelease")
	case identity.AssetID <= 0 || identity.AssetSize <= 0:
		return errors.New("release identity asset metadata is invalid")
	case strings.TrimSpace(identity.AssetName) == "":
		return errors.New("release identity asset name is required")
	case !validSHA256(identity.AssetDigestSHA256):
		return errors.New("release identity asset digest is invalid")
	default:
		return nil
	}
}

func (identity ReleaseIdentity) SameRelease(other ReleaseIdentity) bool {
	if identity.Channel == ChannelRelease {
		return identity.ReleaseID == other.ReleaseID &&
			identity.TagName == other.TagName &&
			identity.AssetDigestSHA256 == other.AssetDigestSHA256
	}
	return identity.ReleaseID == other.ReleaseID &&
		identity.TagName == other.TagName &&
		identity.PublishedAt.Equal(other.PublishedAt) &&
		identity.AssetID == other.AssetID &&
		identity.AssetDigestSHA256 == other.AssetDigestSHA256
}

type Manifest struct {
	SchemaVersion         int             `json:"schema_version"`
	Source                string          `json:"source"`
	VerifiedSource        bool            `json:"verified_source"`
	Identity              ReleaseIdentity `json:"identity"`
	CompressedSHA256      string          `json:"compressed_sha256"`
	BinarySHA256          string          `json:"binary_sha256"`
	BinarySize            int64           `json:"binary_size"`
	BinaryReportedVersion string          `json:"binary_reported_version"`
	InstalledAt           time.Time       `json:"installed_at"`
}

func (manifest Manifest) Validate() error {
	if manifest.SchemaVersion != 1 {
		return errors.New("core manifest schema version is unsupported")
	}
	switch manifest.Source {
	case "downloaded", "bootstrap":
		if !manifest.VerifiedSource {
			return errors.New("downloaded core manifest must be verified")
		}
		if err := manifest.Identity.Validate(); err != nil {
			return fmt.Errorf("validate core release identity: %w", err)
		}
	case "adopted":
		if manifest.VerifiedSource {
			return errors.New("adopted core manifest must be unverified-source")
		}
	default:
		return errors.New("core manifest source is invalid")
	}
	if !validSHA256(manifest.BinarySHA256) || manifest.BinarySize <= 0 {
		return errors.New("core manifest binary metadata is invalid")
	}
	if manifest.Source != "adopted" &&
		manifest.CompressedSHA256 != manifest.Identity.AssetDigestSHA256 {
		return errors.New("core manifest compressed digest does not match identity")
	}
	if strings.TrimSpace(manifest.BinaryReportedVersion) == "" {
		return errors.New("core manifest binary version is required")
	}
	return nil
}

type Settings struct {
	Channel       Channel       `json:"channel"`
	AutoUpdate    bool          `json:"auto_update"`
	CheckInterval time.Duration `json:"check_interval"`
	Managed       bool          `json:"managed"`
	ExternalPath  string        `json:"external_path,omitempty"`
}

func (settings Settings) MarshalJSON() ([]byte, error) {
	type encodedSettings struct {
		Channel       Channel `json:"channel"`
		AutoUpdate    bool    `json:"auto_update"`
		CheckInterval string  `json:"check_interval"`
		Managed       bool    `json:"managed"`
		ExternalPath  string  `json:"external_path,omitempty"`
	}
	return json.Marshal(encodedSettings{
		Channel:       settings.Channel,
		AutoUpdate:    settings.AutoUpdate,
		CheckInterval: settings.CheckInterval.String(),
		Managed:       settings.Managed,
		ExternalPath:  settings.ExternalPath,
	})
}

func (settings Settings) Validate() error {
	if _, err := ParseChannel(string(settings.Channel)); err != nil {
		return err
	}
	if err := ValidateCheckInterval(settings.CheckInterval); err != nil {
		return err
	}
	if settings.Managed && settings.ExternalPath != "" {
		return errors.New("managed core settings must not contain an external path")
	}
	if !settings.Managed && strings.TrimSpace(settings.ExternalPath) == "" {
		return errors.New("external core settings require a binary path")
	}
	return nil
}

type State struct {
	Current           *Manifest        `json:"current,omitempty"`
	Available         *ReleaseIdentity `json:"available,omitempty"`
	LastCheckAt       *time.Time       `json:"last_check_at,omitempty"`
	LastCheckResult   string           `json:"last_check_result,omitempty"`
	LastUpdateAt      *time.Time       `json:"last_update_at,omitempty"`
	LastUpdateResult  string           `json:"last_update_result,omitempty"`
	LastErrorRedacted string           `json:"last_error_redacted,omitempty"`
	NextCheckAt       *time.Time       `json:"next_check_at,omitempty"`
	UpdateInProgress  bool             `json:"update_in_progress"`
}

type Status struct {
	Settings              Settings `json:"settings"`
	State                 State    `json:"state"`
	ActualVersion         string   `json:"actual_version"`
	ControllerVersion     string   `json:"controller_version,omitempty"`
	ProcessActive         bool     `json:"process_active"`
	ControllerReachable   bool     `json:"controller_reachable"`
	CurrentBinarySHA256   string   `json:"current_binary_sha256,omitempty"`
	Managed               bool     `json:"managed"`
	UpdateAvailable       bool     `json:"update_available"`
	RuntimeVersionMatches bool     `json:"runtime_version_matches"`
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
