package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/Aethersailor/m-ui/internal/domain"
)

func TestR3SecretsStayInsideExplicitRevealAndEncryptedStorageBoundaries(t *testing.T) {
	t.Parallel()
	environment := newManagementTestEnvironment(t)
	cookie, csrf := managementLogin(t, environment.handler)

	const (
		vmessSeed       = "vmess-seed-sensitive-boundary"
		vmessUUID       = "2b26a842-8bd1-493a-978b-ee5e546cf508"
		trojanWrapper   = "trojan-wrapper-sensitive-boundary"
		trojanPassword  = "trojan-user-sensitive-boundary"
		shadowsocksPass = "shadowsocks-user-sensitive-boundary"
	)
	secrets := []string{vmessSeed, vmessUUID, trojanWrapper, trojanPassword, shadowsocksPass}

	vmess := createSensitiveBoundaryNode(t, environment, cookie, csrf, listenerRequest{
		Name: "vmess-sensitive", Enabled: true, ListenAddress: "0.0.0.0", Port: "21001",
		Protocol: domain.ProtocolVMess,
		VMess: &domain.VMessSpec{
			Handler: domain.VLESSHandlerSpec{
				Type: domain.VMessHandlerMKCP,
				MKCP: &domain.MKCPConfig{Seed: vmessSeed},
			},
			Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone},
		},
		Users: []userRequest{{
			Name: "vmess-user", Enabled: true,
			VMess: &domain.VMessCredential{UUID: vmessUUID, Cipher: "auto"},
		}},
	}, secrets)
	trojan := createSensitiveBoundaryNode(t, environment, cookie, csrf, listenerRequest{
		Name: "trojan-sensitive", Enabled: true, ListenAddress: "0.0.0.0", Port: "21002",
		Protocol: domain.ProtocolTrojan,
		Trojan: &domain.TrojanSpec{
			Handler:  domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw},
			Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone},
			Shadowsocks: domain.TrojanShadowsocksSpec{
				Enabled: true, Method: "aes-128-gcm", Password: trojanWrapper,
			},
		},
		Users: []userRequest{{
			Name: "trojan-user", Enabled: true,
			Trojan: &domain.TrojanCredential{Password: trojanPassword},
		}},
	}, secrets)
	shadowsocks := createSensitiveBoundaryNode(t, environment, cookie, csrf, listenerRequest{
		Name: "shadowsocks-sensitive", Enabled: true, ListenAddress: "0.0.0.0", Port: "21003",
		Protocol: domain.ProtocolShadowsocks,
		Shadowsocks: &domain.ShadowsocksSpec{
			Cipher: "aes-128-gcm", UDP: true,
			Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone},
		},
		Users: []userRequest{{
			Name: "shadowsocks-user", Enabled: true,
			Shadowsocks: &domain.ShadowsocksCredential{Password: shadowsocksPass},
		}},
	}, secrets)

	for _, endpoint := range []string{
		"/api/v1/nodes",
		"/api/v1/nodes/" + vmess.Node.ID,
		"/api/v1/nodes/" + trojan.Node.ID,
		"/api/v1/nodes/" + shadowsocks.Node.ID,
		"/api/v1/nodes/" + vmess.Node.ID + "/users",
		"/api/v1/nodes/" + trojan.Node.ID + "/users",
		"/api/v1/nodes/" + shadowsocks.Node.ID + "/users",
	} {
		response := performJSONRequest(t, environment.handler, http.MethodGet, endpoint, nil, cookie, "")
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d; body=%s", endpoint, response.Code, response.Body)
		}
		assertNoSecrets(t, "normal API "+endpoint, response.Body.String(), secrets)
	}
	preview := performJSONRequest(
		t, environment.handler, http.MethodGet, "/api/v1/config/preview", nil, cookie, "",
	)
	if preview.Code != http.StatusOK || preview.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("redacted preview status = %d; body=%s", preview.Code, preview.Body)
	}
	assertNoSecrets(t, "redacted configuration preview", preview.Body.String(), secrets)

	if vmess.Node.VMess == nil || vmess.Node.VMess.Handler.MKCP == nil ||
		vmess.Node.VMess.Handler.MKCP.Seed != "" ||
		!vmess.Node.SecretsSet["vmess.handler.mkcp.seed"] ||
		len(vmess.Node.Users) != 1 || vmess.Node.Users[0].VMess == nil ||
		vmess.Node.Users[0].VMess.UUID != "" || !vmess.Node.Users[0].SecretsSet["vmess.uuid"] {
		t.Fatalf("VMess redacted response = %#v", vmess.Node)
	}
	if trojan.Node.Trojan == nil || trojan.Node.Trojan.Shadowsocks.Password != "" ||
		!trojan.Node.SecretsSet["trojan.shadowsocks.password"] ||
		len(trojan.Node.Users) != 1 || trojan.Node.Users[0].Trojan == nil ||
		trojan.Node.Users[0].Trojan.Password != "" ||
		!trojan.Node.Users[0].SecretsSet["trojan.password"] {
		t.Fatalf("Trojan redacted response = %#v", trojan.Node)
	}
	if len(shadowsocks.Node.Users) != 1 || shadowsocks.Node.Users[0].Shadowsocks == nil ||
		shadowsocks.Node.Users[0].Shadowsocks.Password != "" ||
		!shadowsocks.Node.Users[0].SecretsSet["shadowsocks.password"] {
		t.Fatalf("Shadowsocks redacted response = %#v", shadowsocks.Node)
	}

	updatedVMess := performJSONRequest(
		t, environment.handler, http.MethodPut, "/api/v1/nodes/"+vmess.Node.ID,
		listenerRequest{
			Name: vmess.Node.Name, Enabled: vmess.Node.Enabled, ListenAddress: vmess.Node.ListenAddress,
			Port: vmess.Node.Port, Protocol: vmess.Node.Protocol,
			VMess: vmess.Node.VMess, Generation: vmess.Node.Generation,
		},
		cookie, csrf,
	)
	if updatedVMess.Code != http.StatusOK {
		t.Fatalf("redacted VMess update status = %d; body=%s", updatedVMess.Code, updatedVMess.Body)
	}
	assertNoSecrets(t, "VMess mutation response", updatedVMess.Body.String(), secrets)
	storedVMess, err := environment.manager.Node(context.Background(), vmess.Node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedVMess.VMess == nil || storedVMess.VMess.Handler.MKCP == nil ||
		storedVMess.VMess.Handler.MKCP.Seed != vmessSeed {
		t.Fatalf("redacted VMess update did not preserve seed: %#v", storedVMess.VMess)
	}

	updatedTrojanUser := performJSONRequest(
		t, environment.handler, http.MethodPut,
		"/api/v1/nodes/"+trojan.Node.ID+"/users/"+trojan.Node.Users[0].ID,
		userRequest{
			Name: "trojan-user-renamed", Enabled: true,
			Trojan: &domain.TrojanCredential{Password: ""},
		},
		cookie, csrf,
	)
	if updatedTrojanUser.Code != http.StatusOK {
		t.Fatalf("redacted Trojan user update status = %d; body=%s", updatedTrojanUser.Code, updatedTrojanUser.Body)
	}
	assertNoSecrets(t, "Trojan user mutation response", updatedTrojanUser.Body.String(), secrets)
	storedTrojan, err := environment.manager.Node(context.Background(), trojan.Node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(storedTrojan.Users) != 1 || storedTrojan.Users[0].Trojan == nil ||
		storedTrojan.Users[0].Trojan.Password != trojanPassword {
		t.Fatalf("redacted user update did not preserve credential: %#v", storedTrojan.Users)
	}

	for _, item := range []struct {
		name   string
		nodeID string
		userID string
		secret string
	}{
		{"vmess", vmess.Node.ID, vmess.Node.Users[0].ID, vmessUUID},
		{"trojan", trojan.Node.ID, trojan.Node.Users[0].ID, trojanPassword},
		{"shadowsocks", shadowsocks.Node.ID, shadowsocks.Node.Users[0].ID, shadowsocksPass},
	} {
		path := "/api/v1/nodes/" + item.nodeID + "/users/" + item.userID + "/share"
		unauthenticated := performJSONRequest(t, environment.handler, http.MethodGet, path, nil, nil, "")
		if unauthenticated.Code != http.StatusUnauthorized || strings.Contains(unauthenticated.Body.String(), item.secret) {
			t.Fatalf("unauthenticated share response = %d %q", unauthenticated.Code, unauthenticated.Body)
		}
		revealed := performJSONRequest(t, environment.handler, http.MethodGet, path, nil, cookie, "")
		if revealed.Code != http.StatusOK || revealed.Header().Get("Cache-Control") != "no-store" ||
			!strings.Contains(revealed.Body.String(), item.secret) {
			_, shareErr := environment.manager.Share(context.Background(), item.nodeID, item.userID)
			t.Fatalf("explicit %s share %s response = %d %q; service error=%v", item.name, path, revealed.Code, revealed.Body, shareErr)
		}
	}

	invalid := performJSONRequest(
		t, environment.handler, http.MethodPost,
		"/api/v1/nodes/"+trojan.Node.ID+"/users/batch",
		usersCreateRequest{Users: []userRequest{
			{Name: "duplicate", Enabled: true, Trojan: &domain.TrojanCredential{Password: "error-secret-one"}},
			{Name: "duplicate", Enabled: true, Trojan: &domain.TrojanCredential{Password: "error-secret-two"}},
		}},
		cookie, csrf,
	)
	if invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid secret-bearing request status = %d; body=%s", invalid.Code, invalid.Body)
	}
	allRequestSecrets := append(append([]string(nil), secrets...), "error-secret-one", "error-secret-two")
	assertNoSecrets(
		t,
		"validation error",
		invalid.Body.String(),
		allRequestSecrets,
	)

	auditResponse := performJSONRequest(
		t, environment.handler, http.MethodGet, "/api/v1/audit-logs?limit=200", nil, cookie, "",
	)
	if auditResponse.Code != http.StatusOK {
		t.Fatalf("audit API status = %d; body=%s", auditResponse.Code, auditResponse.Body)
	}
	assertNoSecrets(t, "audit API", auditResponse.Body.String(), allRequestSecrets)
	assertDatabaseHasNoPlaintextSecrets(t, environment.database.DB(), allRequestSecrets)
	assertSQLiteFilesHaveNoPlaintextSecrets(t, environment.databasePath, allRequestSecrets)
}

func createSensitiveBoundaryNode(
	t *testing.T,
	environment managementTestEnvironment,
	cookie *http.Cookie,
	csrf string,
	input listenerRequest,
	secrets []string,
) listenerMutationResponse {
	t.Helper()
	response := performJSONRequest(
		t, environment.handler, http.MethodPost, "/api/v1/nodes",
		input, cookie, csrf,
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("create %s status = %d; body=%s", input.Protocol, response.Code, response.Body)
	}
	assertNoSecrets(t, "create "+string(input.Protocol)+" response", response.Body.String(), secrets)
	var result listenerMutationResponse
	decodeOperationResponse(t, response.Body.Bytes(), &result)
	return result
}

func assertDatabaseHasNoPlaintextSecrets(t *testing.T, database *sql.DB, secrets []string) {
	t.Helper()
	queries := []string{
		"SELECT protocol_config_json || protocol_secret_ciphertext FROM nodes",
		"SELECT credential_ciphertext FROM node_users",
		"SELECT action || resource_type || COALESCE(resource_id, '') || summary_redacted FROM audit_logs",
		"SELECT reason || COALESCE(error_message_redacted, '') FROM config_revisions",
	}
	for _, query := range queries {
		rows, err := database.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			assertNoSecrets(t, "database query "+query, value, secrets)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
	}
}

func assertSQLiteFilesHaveNoPlaintextSecrets(t *testing.T, databasePath string, secrets []string) {
	t.Helper()
	for _, path := range []string{databasePath, databasePath + "-wal", databasePath + "-shm"} {
		content, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		assertNoSecrets(t, "SQLite file "+path, string(content), secrets)
	}
}

func assertNoSecrets(t *testing.T, boundary, content string, secrets []string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(content, secret) {
			t.Fatalf("%s exposed secret %q", boundary, secret)
		}
	}
}
