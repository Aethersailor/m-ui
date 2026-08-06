package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Aethersailor/m-ui/internal/domain"
)

func TestManagementClassicProtocolRequestsAndResponses(t *testing.T) {
	t.Parallel()
	environment := newManagementTestEnvironment(t)
	sessionCookie, csrfToken := managementLogin(t, environment.handler)

	tests := []struct {
		name       string
		port       string
		protocol   domain.ProtocolKind
		forbidden  string
		configure  func(*listenerRequest)
		assertNode func(*testing.T, listenerResponse)
		assertUser func(*testing.T, userResponse)
	}{
		{
			name: "vmess", port: "10001", protocol: domain.ProtocolVMess,
			forbidden: "vmess-mkcp-sensitive-seed",
			configure: func(input *listenerRequest) {
				input.VMess = &domain.VMessSpec{
					Handler: domain.VLESSHandlerSpec{
						Type: domain.VMessHandlerMKCP,
						MKCP: &domain.MKCPConfig{Seed: "vmess-mkcp-sensitive-seed"},
					},
					Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone},
				}
			},
			assertNode: func(t *testing.T, node listenerResponse) {
				t.Helper()
				if node.VMess == nil || node.VMess.Handler.MKCP == nil ||
					node.VMess.Handler.MKCP.Seed != "" ||
					!node.SecretsSet["vmess.handler.mkcp.seed"] ||
					node.VLESS != nil || node.Trojan != nil || node.Shadowsocks != nil {
					t.Fatalf("VMess response = %#v", node)
				}
			},
			assertUser: func(t *testing.T, user userResponse) {
				t.Helper()
				if user.VMess == nil || user.VMess.UUID == "" || user.VMess.Cipher != "auto" {
					t.Fatalf("VMess user response = %#v", user)
				}
			},
		},
		{
			name: "trojan", port: "10002", protocol: domain.ProtocolTrojan,
			configure: func(input *listenerRequest) {
				input.Trojan = &domain.TrojanSpec{
					Handler:  domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw},
					Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone},
				}
			},
			assertNode: func(t *testing.T, node listenerResponse) {
				t.Helper()
				if node.Trojan == nil || node.VLESS != nil || node.VMess != nil || node.Shadowsocks != nil {
					t.Fatalf("Trojan response = %#v", node)
				}
			},
			assertUser: func(t *testing.T, user userResponse) {
				t.Helper()
				if user.Trojan == nil || user.Trojan.Password == "" {
					t.Fatalf("Trojan user response = %#v", user)
				}
			},
		},
		{
			name: "shadowsocks", port: "10003", protocol: domain.ProtocolShadowsocks,
			configure: func(input *listenerRequest) {
				input.Shadowsocks = &domain.ShadowsocksSpec{
					Cipher: "2022-blake3-aes-128-gcm", UDP: true,
					Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone},
				}
			},
			assertNode: func(t *testing.T, node listenerResponse) {
				t.Helper()
				if node.Shadowsocks == nil || node.VLESS != nil || node.VMess != nil || node.Trojan != nil {
					t.Fatalf("Shadowsocks response = %#v", node)
				}
			},
			assertUser: func(t *testing.T, user userResponse) {
				t.Helper()
				if user.Shadowsocks == nil {
					t.Fatalf("Shadowsocks user response = %#v", user)
				}
				key, err := base64.StdEncoding.DecodeString(user.Shadowsocks.Password)
				if err != nil || len(key) != 16 {
					t.Fatalf("Shadowsocks 2022 AES-128 password = %q, bytes=%d, err=%v", user.Shadowsocks.Password, len(key), err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := listenerRequest{
				Name: test.name, ListenAddress: "0.0.0.0", Port: test.port,
				Protocol: test.protocol,
			}
			test.configure(&input)
			createdResponse := performJSONRequest(
				t, environment.handler, http.MethodPost, "/api/v1/nodes",
				input, sessionCookie, csrfToken,
			)
			if createdResponse.Code != http.StatusCreated {
				t.Fatalf("create %s status = %d; body=%s", test.name, createdResponse.Code, createdResponse.Body)
			}
			if test.forbidden != "" && strings.Contains(createdResponse.Body.String(), test.forbidden) {
				t.Fatalf("create %s response exposed secret %q", test.name, test.forbidden)
			}
			var created listenerMutationResponse
			decodeOperationResponse(t, createdResponse.Body.Bytes(), &created)
			test.assertNode(t, created.Node)

			usersResponse := performJSONRequest(
				t, environment.handler, http.MethodPost,
				"/api/v1/nodes/"+created.Node.ID+"/users/batch",
				usersCreateRequest{Users: []userRequest{{Name: test.name + "-user", Enabled: true}}},
				sessionCookie, csrfToken,
			)
			if usersResponse.Code != http.StatusCreated {
				t.Fatalf("create %s user status = %d; body=%s", test.name, usersResponse.Code, usersResponse.Body)
			}
			var users usersMutationResponse
			decodeOperationResponse(t, usersResponse.Body.Bytes(), &users)
			if len(users.Users) != 1 {
				t.Fatalf("%s users = %#v", test.name, users.Users)
			}
			test.assertUser(t, users.Users[0])
		})
	}
}

func TestNodeOperationsRequireCSRFAndPublishAtomically(t *testing.T) {
	t.Parallel()
	environment := newManagementTestEnvironment(t)
	sessionCookie, csrfToken := managementLogin(t, environment.handler)

	unauthenticated := performJSONRequest(
		t, environment.handler, http.MethodPost, "/api/v1/nodes/batch-enabled",
		nodesEnabledRequest{NodeIDs: []string{"missing"}}, nil, "",
	)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated batch status = %d", unauthenticated.Code)
	}

	source := createOperationTestNode(
		t, environment, sessionCookie, csrfToken, "source", "443", true,
		[]userRequest{{Name: "source-user", Enabled: true}},
	)
	blockedClone := performJSONRequest(
		t, environment.handler, http.MethodPost,
		"/api/v1/nodes/"+source.Node.ID+"/clone",
		cloneNodeRequest{Name: "clone-without-csrf", Port: "8443"},
		sessionCookie, "",
	)
	if blockedClone.Code != http.StatusForbidden {
		t.Fatalf("clone without CSRF status = %d", blockedClone.Code)
	}
	for _, invalid := range []cloneNodeRequest{
		{Name: source.Node.Name, Port: "7443"},
		{Name: "clone-same-port", Port: source.Node.Port},
	} {
		response := performJSONRequest(
			t, environment.handler, http.MethodPost,
			"/api/v1/nodes/"+source.Node.ID+"/clone",
			invalid, sessionCookie, csrfToken,
		)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid clone %#v status = %d; body=%s", invalid, response.Code, response.Body)
		}
	}
	if nodes, err := environment.manager.Nodes(context.Background()); err != nil || len(nodes) != 1 {
		t.Fatalf("invalid clones changed state: nodes=%#v err=%v", nodes, err)
	}

	withoutUsersResponse := performJSONRequest(
		t, environment.handler, http.MethodPost,
		"/api/v1/nodes/"+source.Node.ID+"/clone",
		cloneNodeRequest{Name: "clone-empty", Port: "8443"},
		sessionCookie, csrfToken,
	)
	if withoutUsersResponse.Code != http.StatusCreated {
		t.Fatalf("clone without users status = %d; body=%s", withoutUsersResponse.Code, withoutUsersResponse.Body)
	}
	var withoutUsers listenerMutationResponse
	decodeOperationResponse(t, withoutUsersResponse.Body.Bytes(), &withoutUsers)
	if withoutUsers.Node.Enabled || withoutUsers.Node.Port != "8443" || len(withoutUsers.Node.Users) != 0 {
		t.Fatalf("clone defaults = %#v", withoutUsers.Node)
	}
	if withoutUsers.Node.ID == source.Node.ID ||
		withoutUsers.Node.AccessProfiles[0].ID == source.Node.AccessProfiles[0].ID ||
		withoutUsers.Node.AccessProfiles[0].PublicPort != 8443 {
		t.Fatalf("clone identity/access profile = %#v", withoutUsers.Node)
	}

	withUsersResponse := performJSONRequest(
		t, environment.handler, http.MethodPost,
		"/api/v1/nodes/"+source.Node.ID+"/clone",
		cloneNodeRequest{Name: "clone-users", Port: "9443", IncludeUsers: true},
		sessionCookie, csrfToken,
	)
	if withUsersResponse.Code != http.StatusCreated {
		t.Fatalf("clone with users status = %d; body=%s", withUsersResponse.Code, withUsersResponse.Body)
	}
	var withUsers listenerMutationResponse
	decodeOperationResponse(t, withUsersResponse.Body.Bytes(), &withUsers)
	if withUsers.Node.Enabled || len(withUsers.Node.Users) != 1 ||
		withUsers.Node.Users[0].ID == source.Node.Users[0].ID ||
		withUsers.Node.Users[0].NodeID != withUsers.Node.ID {
		t.Fatalf("clone copied users = %#v", withUsers.Node)
	}
	if withUsers.Revision.RevisionNumber != withoutUsers.Revision.RevisionNumber+1 {
		t.Fatalf("clone revision numbers = %d, %d", withoutUsers.Revision.RevisionNumber, withUsers.Revision.RevisionNumber)
	}

	beforeNodeBatch := revisionCount(t, environment, sessionCookie)
	nodeBatchResponse := performJSONRequest(
		t, environment.handler, http.MethodPost, "/api/v1/nodes/batch-enabled",
		nodesEnabledRequest{NodeIDs: []string{source.Node.ID, withUsers.Node.ID}, Enabled: true},
		sessionCookie, csrfToken,
	)
	if nodeBatchResponse.Code != http.StatusOK {
		t.Fatalf("node batch status = %d; body=%s", nodeBatchResponse.Code, nodeBatchResponse.Body)
	}
	var nodeBatch nodesMutationResponse
	decodeOperationResponse(t, nodeBatchResponse.Body.Bytes(), &nodeBatch)
	if len(nodeBatch.Nodes) != 2 || !nodeBatch.Nodes[0].Enabled || !nodeBatch.Nodes[1].Enabled {
		t.Fatalf("node batch response = %#v", nodeBatch)
	}
	if after := revisionCount(t, environment, sessionCookie); after != beforeNodeBatch+1 {
		t.Fatalf("node batch added %d revisions, want 1", after-beforeNodeBatch)
	}

	beforeUserBatch := revisionCount(t, environment, sessionCookie)
	userBatchResponse := performJSONRequest(
		t, environment.handler, http.MethodPost,
		"/api/v1/nodes/"+withoutUsers.Node.ID+"/users/batch",
		usersCreateRequest{Users: []userRequest{
			{Name: "batch-a", Enabled: true},
			{Name: "batch-b", Enabled: true},
		}},
		sessionCookie, csrfToken,
	)
	if userBatchResponse.Code != http.StatusCreated {
		t.Fatalf("user batch status = %d; body=%s", userBatchResponse.Code, userBatchResponse.Body)
	}
	var userBatch usersMutationResponse
	decodeOperationResponse(t, userBatchResponse.Body.Bytes(), &userBatch)
	if len(userBatch.Users) != 2 || userBatch.Users[0].ID == userBatch.Users[1].ID ||
		userBatch.Users[0].VLESS == nil || userBatch.Users[1].VLESS == nil ||
		userBatch.Users[0].VLESS.UUID == userBatch.Users[1].VLESS.UUID {
		t.Fatalf("created batch users = %#v", userBatch.Users)
	}
	if after := revisionCount(t, environment, sessionCookie); after != beforeUserBatch+1 {
		t.Fatalf("user batch added %d revisions, want 1", after-beforeUserBatch)
	}

	beforeFailedState, err := environment.manager.Node(context.Background(), withoutUsers.Node.ID)
	if err != nil {
		t.Fatal(err)
	}
	failedBatch := performJSONRequest(
		t, environment.handler, http.MethodPost,
		"/api/v1/nodes/"+withoutUsers.Node.ID+"/users/batch",
		usersCreateRequest{Users: []userRequest{
			{Name: "duplicate", Enabled: true},
			{Name: "duplicate", Enabled: true},
		}},
		sessionCookie, csrfToken,
	)
	if failedBatch.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid user batch status = %d; body=%s", failedBatch.Code, failedBatch.Body)
	}
	afterFailedState, err := environment.manager.Node(context.Background(), withoutUsers.Node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterFailedState.Users) != len(beforeFailedState.Users) ||
		afterFailedState.Generation != beforeFailedState.Generation {
		t.Fatalf("failed batch mutated node: before=%#v after=%#v", beforeFailedState, afterFailedState)
	}

	beforeToggle := revisionCount(t, environment, sessionCookie)
	toggleResponse := performJSONRequest(
		t, environment.handler, http.MethodPost,
		"/api/v1/nodes/"+withoutUsers.Node.ID+"/users/batch-enabled",
		usersEnabledRequest{UserIDs: []string{userBatch.Users[0].ID, userBatch.Users[1].ID}, Enabled: false},
		sessionCookie, csrfToken,
	)
	if toggleResponse.Code != http.StatusOK {
		t.Fatalf("user toggle batch status = %d; body=%s", toggleResponse.Code, toggleResponse.Body)
	}
	var toggled usersMutationResponse
	decodeOperationResponse(t, toggleResponse.Body.Bytes(), &toggled)
	if len(toggled.Users) != 2 || toggled.Users[0].Enabled || toggled.Users[1].Enabled {
		t.Fatalf("user toggle response = %#v", toggled)
	}
	if after := revisionCount(t, environment, sessionCookie); after != beforeToggle+1 {
		t.Fatalf("user toggle added %d revisions, want 1", after-beforeToggle)
	}
}

func createOperationTestNode(
	t *testing.T,
	environment managementTestEnvironment,
	sessionCookie *http.Cookie,
	csrfToken, name, port string,
	enabled bool,
	users []userRequest,
) listenerMutationResponse {
	t.Helper()
	response := performJSONRequest(
		t, environment.handler, http.MethodPost, "/api/v1/nodes",
		listenerRequest{
			Name: name, Enabled: enabled, ListenAddress: "0.0.0.0", Port: port,
			Protocol: domain.ProtocolVLESS,
			VLESS: &domain.VLESSSpec{
				Decryption: "none",
				Handler:    domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw},
				Security:   domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone},
			},
			Users: users,
		},
		sessionCookie, csrfToken,
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("create operation test node status = %d; body=%s", response.Code, response.Body)
	}
	var result listenerMutationResponse
	decodeOperationResponse(t, response.Body.Bytes(), &result)
	return result
}

func revisionCount(
	t *testing.T,
	environment managementTestEnvironment,
	sessionCookie *http.Cookie,
) int {
	t.Helper()
	response := performJSONRequest(
		t, environment.handler, http.MethodGet, "/api/v1/config/revisions?limit=100",
		nil, sessionCookie, "",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("list revisions status = %d; body=%s", response.Code, response.Body)
	}
	var result revisionsResponse
	decodeOperationResponse(t, response.Body.Bytes(), &result)
	return len(result.Revisions)
}

func decodeOperationResponse(t *testing.T, payload []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(payload, target); err != nil {
		t.Fatal(err)
	}
}
