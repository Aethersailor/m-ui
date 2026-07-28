package mihomo

import (
	"context"
	"time"

	"github.com/Aethersailor/m-ui/internal/domain"
)

type CoreCLI interface {
	Validate(ctx context.Context, configPath string) error
	Version(ctx context.Context) (string, error)
	GenerateRealityKeypair(ctx context.Context) (domain.Keypair, error)
}

type CoreController interface {
	Version(ctx context.Context) (Version, error)
	Traffic(ctx context.Context) (TrafficSnapshot, error)
	Memory(ctx context.Context) (MemorySnapshot, error)
	Connections(ctx context.Context) (ConnectionsSnapshot, error)
	Reload(ctx context.Context, configPath string) error
	Restart(ctx context.Context, configPath string) error
}

type CoreProcess interface {
	IsActive(ctx context.Context) (bool, error)
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Restart(ctx context.Context) error
	Reload(ctx context.Context) error
	RecentLogs(ctx context.Context, limit int) ([]LogEntry, error)
}

type Version struct {
	Meta    bool   `json:"meta"`
	Version string `json:"version"`
}

type TrafficSnapshot struct {
	Up        int64 `json:"up"`
	Down      int64 `json:"down"`
	UpTotal   int64 `json:"upTotal"`
	DownTotal int64 `json:"downTotal"`
}

type MemorySnapshot struct {
	InUse   uint64 `json:"inuse"`
	OSLimit uint64 `json:"oslimit"`
}

type ConnectionsSnapshot struct {
	DownloadTotal int64        `json:"downloadTotal"`
	UploadTotal   int64        `json:"uploadTotal"`
	Connections   []Connection `json:"connections"`
}

type Connection struct {
	ID       string `json:"id"`
	Upload   int64  `json:"upload"`
	Download int64  `json:"download"`
}

type LogEntry struct {
	Timestamp time.Time
	Message   string
}
