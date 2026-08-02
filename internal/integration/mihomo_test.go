package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aethersailor/m-ui/internal/domain"
	"github.com/Aethersailor/m-ui/internal/mihomo"
	"github.com/Aethersailor/m-ui/internal/publisher"
	"github.com/Aethersailor/m-ui/internal/service"
)

func TestGeneratedServerAndClientConfigurationsWithRealMihomo(t *testing.T) {
	binary := os.Getenv("M_UI_TEST_MIHOMO_BINARY")
	if binary == "" {
		t.Skip("M_UI_TEST_MIHOMO_BINARY is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	t.Setenv("USERPROFILE", t.TempDir())
	cli, err := mihomo.NewCLI(binary)
	if err != nil {
		t.Fatalf("NewCLI() error = %v", err)
	}
	keypair, err := cli.GenerateRealityKeypair(ctx)
	if err != nil {
		t.Fatalf("GenerateRealityKeypair() error = %v", err)
	}
	version, err := cli.Version(ctx)
	if err != nil || version == "" {
		t.Fatalf("Version() = %q, %v", version, err)
	}
	scenarios := []struct {
		name        string
		bindHost    string
		connectHost string
	}{
		{name: "loopback-ipv4", bindHost: "127.0.0.1", connectHost: "127.0.0.1"},
		{name: "wildcard-ipv4", bindHost: "0.0.0.0", connectHost: "127.0.0.1"},
		{name: "loopback-ipv6", bindHost: "::1", connectHost: "::1"},
		{name: "wildcard-ipv6", bindHost: "::", connectHost: "::1"},
	}
	for _, scenario := range scenarios {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			scenarioContext, scenarioCancel := context.WithTimeout(ctx, 30*time.Second)
			defer scenarioCancel()
			controllerPort := availableTCPPortForHost(t, scenario.bindHost)
			listenerPort := availableTCPPortForHost(t, scenario.bindHost)
			for listenerPort == controllerPort {
				listenerPort = availableTCPPortForHost(t, scenario.bindHost)
			}
			listenerID := "f8bb1f0d-a396-42fd-a221-c382c8ef9526"
			userID := "eeed3560-deb6-453c-8d56-9a2f5b66defc"
			state := domain.DesiredState{
				AsOf: time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC),
				MihomoExternalControllerBind: domain.Endpoint{
					Host: scenario.bindHost,
					Port: uint16(controllerPort),
				},
				MihomoControllerConnect: domain.Endpoint{
					Host: scenario.connectHost,
					Port: uint16(controllerPort),
				},
				ExternalControllerCORSOrigins: []string{"https://dashboard.example.com"},
				ControllerSecret:              "integration-test-controller-secret",
				PublicHost:                    "node.example.com",
				Listeners: []domain.Listener{{
					ID:                listenerID,
					Name:              "integration-node",
					Enabled:           true,
					ListenAddress:     "127.0.0.1",
					ListenPort:        uint16(listenerPort),
					ServerName:        "www.example.com",
					RealityDest:       "www.example.com:443",
					RealityPrivateKey: keypair.PrivateKey,
					RealityPublicKey:  keypair.PublicKey,
					ShortID:           "0123456789abcdef",
					UDPEnabled:        true,
					Users: []domain.User{{
						ID:         userID,
						ListenerID: listenerID,
						Name:       "integration-user",
						Enabled:    true,
						UUID:       "2bf189fe-ec56-497d-9069-68bf32c4425b",
					}},
				}},
			}

			serverYAML, err := (publisher.YAMLCompiler{}).Compile(scenarioContext, state)
			if err != nil {
				t.Fatalf("compile server YAML: %v", err)
			}
			share, err := service.BuildShare(state, listenerID, userID)
			if err != nil {
				t.Fatalf("build client YAML: %v", err)
			}

			directory := t.TempDir()
			sensitiveValues := []string{
				keypair.PrivateKey,
				keypair.PublicKey,
				state.ControllerSecret,
				state.Listeners[0].Users[0].UUID,
			}
			validateWithMihomo(t, scenarioContext, cli, directory, "server.yaml", serverYAML, sensitiveValues)
			clientYAML := []byte(share.ClientYAML + "rules:\n  - MATCH,DIRECT\n")
			validateWithMihomo(t, scenarioContext, cli, directory, "client.yaml", clientYAML, sensitiveValues)
			startAndReloadMihomo(
				t,
				scenarioContext,
				binary,
				directory,
				filepath.Join(directory, "server.yaml"),
				state.MihomoControllerConnect.Address(),
				state.ControllerSecret,
				sensitiveValues,
			)
		})
	}
}

func validateWithMihomo(
	t *testing.T,
	ctx context.Context,
	cli *mihomo.CLI,
	directory string,
	name string,
	content []byte,
	sensitiveValues []string,
) {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	if err := cli.Validate(ctx, path); err != nil {
		t.Fatalf(
			"Mihomo rejected %s: %s",
			name,
			redactedOutput([]byte(err.Error()), sensitiveValues),
		)
	}
}

func redactedOutput(output []byte, sensitiveValues []string) string {
	if len(output) > 4096 {
		output = []byte(fmt.Sprintf("%s\n[output truncated]", output[:4096]))
	}
	result := string(output)
	for _, value := range sensitiveValues {
		if value != "" {
			result = strings.ReplaceAll(result, value, "[redacted]")
		}
	}
	return result
}

func availableTCPPortForHost(t *testing.T, host string) int {
	t.Helper()
	network := "tcp4"
	if strings.Contains(host, ":") {
		network = "tcp6"
	}
	listener, err := net.Listen(network, net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatalf("reserve TCP port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release TCP port: %v", err)
	}
	return port
}

func startAndReloadMihomo(
	t *testing.T,
	ctx context.Context,
	binary, directory, configPath, controllerAddress, controllerSecret string,
	sensitiveValues []string,
) {
	t.Helper()
	command := exec.CommandContext(
		ctx,
		binary,
		"-d",
		directory,
		"-f",
		configPath,
	)
	command.Env = append(
		os.Environ(),
		"HOME="+directory,
		"SAFE_PATHS="+directory,
	)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatalf("start Mihomo: %v", err)
	}
	stopped := false
	defer func() {
		if !stopped {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()

	controller, err := mihomo.NewController(controllerAddress, controllerSecret)
	if err != nil {
		t.Fatalf("NewController() error = %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err = controller.Version(ctx); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"Mihomo Controller did not become ready: %s",
				redactedOutput(output.Bytes(), sensitiveValues),
			)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for Mihomo Controller: %v", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err := controller.Reload(ctx, configPath); err != nil {
		t.Fatalf("reload Mihomo configuration: %v", err)
	}
	if _, err := controller.Version(ctx); err != nil {
		t.Fatalf("Mihomo Controller unhealthy after reload: %v", err)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatalf("stop Mihomo: %v", err)
	}
	_ = command.Wait()
	stopped = true
}
