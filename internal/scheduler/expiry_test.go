package scheduler

import (
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	muicrypto "github.com/Aethersailor/m-ui/internal/crypto"
	"github.com/Aethersailor/m-ui/internal/domain"
	"github.com/Aethersailor/m-ui/internal/mihomo"
	"github.com/Aethersailor/m-ui/internal/publisher"
	"github.com/Aethersailor/m-ui/internal/store"
)

func TestExpiryBatchDisablesUsersAndOnlyAffectedEmptyListeners(t *testing.T) {
	t.Parallel()
	fixture := newExpiryFixture(t)

	result, err := fixture.scheduler.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.UsersDisabled != 2 || result.ListenersDisabled != 1 {
		t.Fatalf("batch result = %#v", result)
	}
	state, err := fixture.store.ReadDesiredState(
		context.Background(),
		fixture.batchTime,
	)
	if err != nil {
		t.Fatal(err)
	}
	mixed := expiryListenerByID(
		t,
		state,
		"11111111-1111-4111-8111-111111111111",
	)
	empty := expiryListenerByID(
		t,
		state,
		"22222222-2222-4222-8222-222222222222",
	)
	unrelated := expiryListenerByID(
		t,
		state,
		"33333333-3333-4333-8333-333333333333",
	)
	if !mixed.Enabled {
		t.Fatal("listener with a remaining effective user was disabled")
	}
	if expiryUserByName(t, mixed, "expired").Enabled {
		t.Fatal("expired user remained enabled")
	}
	if !expiryUserByName(t, mixed, "active").Enabled {
		t.Fatal("unexpired user was disabled")
	}
	if empty.Enabled {
		t.Fatal("affected listener with no effective users remained enabled")
	}
	if unrelated.Enabled {
		t.Fatal("unrelated disabled listener changed state")
	}
	entries, err := fixture.store.AuditEntries(context.Background(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 ||
		entries[0].Action != "scheduler.expiry_batch" ||
		!strings.Contains(entries[0].SummaryRedacted, "2 expired") ||
		!strings.Contains(entries[0].SummaryRedacted, "1 affected") {
		t.Fatalf("audit entries = %#v", entries)
	}
}

func TestExpiryFailureRollsBackAndRetriesSameBatch(t *testing.T) {
	t.Parallel()
	fixture := newExpiryFixture(t)
	fixture.controller.reloadErr = errors.New("synthetic reload failure")
	fixture.process.restartErr = errors.New("synthetic restart failure")

	if _, err := fixture.scheduler.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce() succeeded during synthetic runtime failure")
	}
	state, err := fixture.store.ReadDesiredState(
		context.Background(),
		fixture.batchTime,
	)
	if err != nil {
		t.Fatal(err)
	}
	empty := expiryListenerByID(
		t,
		state,
		"22222222-2222-4222-8222-222222222222",
	)
	if !empty.Enabled || !expiryUserByName(t, empty, "last-expired").Enabled {
		t.Fatal("failed expiry batch changed structured state")
	}
	entries, err := fixture.store.AuditEntries(context.Background(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed batch wrote success audit entries: %#v", entries)
	}

	fixture.controller.reloadErr = nil
	fixture.process.restartErr = nil
	result, err := fixture.scheduler.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("retry RunOnce() error = %v", err)
	}
	if result.UsersDisabled != 2 || result.ListenersDisabled != 1 {
		t.Fatalf("retry batch result = %#v", result)
	}
}

type expiryFixture struct {
	store      *store.ManagedStore
	scheduler  *Expiry
	controller *expiryController
	process    *expiryProcess
	batchTime  time.Time
}

func newExpiryFixture(t *testing.T) expiryFixture {
	t.Helper()
	ctx := context.Background()
	directory := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(directory, "m-ui.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	sealer, err := muicrypto.NewSealer(muicrypto.MasterKey{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	managed, err := store.NewManagedStore(database, sealer)
	if err != nil {
		t.Fatal(err)
	}
	batchTime := time.Date(2026, 7, 28, 4, 0, 0, 0, time.UTC)
	initialState := expiryState(batchTime)
	transaction, err := managed.BeginImmediate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.ReplaceDesiredState(
		ctx,
		initialState,
	); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	controller := &expiryController{}
	process := &expiryProcess{active: true}
	configurationPublisher, err := publisher.New(
		managed,
		publisher.YAMLCompiler{},
		expiryCLI{},
		controller,
		process,
		publisher.Options{
			ConfigPath: filepath.Join(
				directory,
				"mihomo",
				"config.yaml",
			),
			RevisionDirectory: filepath.Join(directory, "revisions"),
			HistoryLimit:      20,
			HealthTimeout:     50 * time.Millisecond,
			HealthInterval:    time.Millisecond,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configurationPublisher.Publish(ctx, publisher.Request{
		Reason:      "seed active expiry revision",
		EffectiveAt: &initialState.AsOf,
		Mutate: func(
			ctx context.Context,
			transaction store.PublicationTransaction,
		) error {
			return transaction.ReplaceDesiredState(ctx, initialState)
		},
	}); err != nil {
		t.Fatalf("seed active expiry revision: %v", err)
	}
	instance, err := NewExpiry(configurationPublisher, Options{
		Interval: time.Minute,
		Clock:    func() time.Time { return batchTime },
	})
	if err != nil {
		t.Fatal(err)
	}
	return expiryFixture{
		store:      managed,
		scheduler:  instance,
		controller: controller,
		process:    process,
		batchTime:  batchTime,
	}
}

func expiryState(batchTime time.Time) domain.DesiredState {
	privateKey := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	publicBytes := make([]byte, 32)
	publicBytes[0] = 1
	publicKey := base64.RawURLEncoding.EncodeToString(publicBytes)
	expired := batchTime.Add(-time.Minute)
	future := batchTime.Add(time.Hour)
	return domain.DesiredState{
		AsOf:              batchTime.Add(-time.Hour),
		ControllerAddress: "127.0.0.1:9090",
		ControllerSecret:  strings.Repeat("s", 32),
		PublicHost:        "vpn.example.com",
		Listeners: []domain.Listener{
			expiryListener(
				"11111111-1111-4111-8111-111111111111",
				"mixed",
				443,
				privateKey,
				publicKey,
				true,
				[]domain.User{
					expiryUser(
						"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
						"expired",
						"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab",
						&expired,
						batchTime,
					),
					expiryUser(
						"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
						"active",
						"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbc",
						&future,
						batchTime,
					),
				},
				batchTime,
			),
			expiryListener(
				"22222222-2222-4222-8222-222222222222",
				"empty-after-expiry",
				8443,
				privateKey,
				publicKey,
				true,
				[]domain.User{
					expiryUser(
						"cccccccc-cccc-4ccc-8ccc-cccccccccccc",
						"last-expired",
						"cccccccc-cccc-4ccc-8ccc-cccccccccccd",
						&expired,
						batchTime,
					),
				},
				batchTime,
			),
			expiryListener(
				"33333333-3333-4333-8333-333333333333",
				"unrelated-disabled",
				9443,
				privateKey,
				publicKey,
				false,
				nil,
				batchTime,
			),
		},
	}
}

func expiryListener(
	id, name string,
	port uint16,
	privateKey, publicKey string,
	enabled bool,
	users []domain.User,
	now time.Time,
) domain.Listener {
	for index := range users {
		users[index].ListenerID = id
	}
	return domain.Listener{
		ID:                id,
		Name:              name,
		Enabled:           enabled,
		ListenAddress:     "0.0.0.0",
		ListenPort:        port,
		ServerName:        "www.example.com",
		RealityDest:       "www.example.com:443",
		RealityPrivateKey: privateKey,
		RealityPublicKey:  publicKey,
		ShortID:           "0123456789abcdef",
		UDPEnabled:        true,
		Users:             users,
		CreatedAt:         now.Add(-time.Hour),
		UpdatedAt:         now.Add(-time.Hour),
	}
}

func expiryUser(
	id, name, protocolUUID string,
	expiresAt *time.Time,
	now time.Time,
) domain.User {
	return domain.User{
		ID:        id,
		Name:      name,
		Enabled:   true,
		UUID:      protocolUUID,
		ExpiresAt: expiresAt,
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now.Add(-time.Hour),
	}
}

func expiryListenerByID(
	t *testing.T,
	state domain.DesiredState,
	id string,
) domain.Listener {
	t.Helper()
	for _, listener := range state.Listeners {
		if listener.ID == id {
			return listener
		}
	}
	t.Fatalf("listener %s not found", id)
	return domain.Listener{}
}

func expiryUserByName(
	t *testing.T,
	listener domain.Listener,
	name string,
) domain.User {
	t.Helper()
	for _, user := range listener.Users {
		if user.Name == name {
			return user
		}
	}
	t.Fatalf("user %s not found", name)
	return domain.User{}
}

type expiryCLI struct{}

func (expiryCLI) Validate(context.Context, string) error { return nil }
func (expiryCLI) Version(context.Context) (string, error) {
	return "Mihomo Meta test", nil
}
func (expiryCLI) GenerateRealityKeypair(
	context.Context,
) (domain.Keypair, error) {
	return domain.Keypair{}, nil
}

type expiryController struct {
	reloadErr error
}

func (controller *expiryController) Version(
	context.Context,
) (mihomo.Version, error) {
	return mihomo.Version{Meta: true, Version: "test"}, nil
}
func (controller *expiryController) Traffic(
	context.Context,
) (mihomo.TrafficSnapshot, error) {
	return mihomo.TrafficSnapshot{}, nil
}
func (controller *expiryController) Memory(
	context.Context,
) (mihomo.MemorySnapshot, error) {
	return mihomo.MemorySnapshot{}, nil
}
func (controller *expiryController) Connections(
	context.Context,
) (mihomo.ConnectionsSnapshot, error) {
	return mihomo.ConnectionsSnapshot{}, nil
}
func (controller *expiryController) Reload(context.Context, string) error {
	err := controller.reloadErr
	controller.reloadErr = nil
	return err
}
func (controller *expiryController) Restart(context.Context, string) error {
	return nil
}

type expiryProcess struct {
	active     bool
	restartErr error
}

func (process *expiryProcess) IsActive(context.Context) (bool, error) {
	return process.active, nil
}
func (process *expiryProcess) Start(context.Context) error { return nil }
func (process *expiryProcess) Stop(context.Context) error  { return nil }
func (process *expiryProcess) Restart(context.Context) error {
	err := process.restartErr
	process.restartErr = nil
	return err
}
func (process *expiryProcess) Reload(context.Context) error { return nil }
func (process *expiryProcess) RecentLogs(
	context.Context,
	int,
) ([]mihomo.LogEntry, error) {
	return nil, nil
}
