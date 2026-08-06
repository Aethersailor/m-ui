package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Aethersailor/m-ui/internal/service"
)

type cloneNodeRequest struct {
	Name         string `json:"name"`
	Port         string `json:"port"`
	IncludeUsers bool   `json:"include_users"`
}

type nodesEnabledRequest struct {
	NodeIDs []string `json:"node_ids"`
	Enabled bool     `json:"enabled"`
}

type nodesMutationResponse struct {
	Nodes    []listenerResponse `json:"nodes"`
	Revision revisionResponse   `json:"revision"`
}

type usersCreateRequest struct {
	Users []userRequest `json:"users"`
}

type usersEnabledRequest struct {
	UserIDs []string `json:"user_ids"`
	Enabled bool     `json:"enabled"`
}

type usersMutationResponse struct {
	Users    []userResponse   `json:"users"`
	Revision revisionResponse `json:"revision"`
}

func (input userRequest) userSpec() service.UserSpec {
	return service.UserSpec{
		Name: input.Name, Enabled: input.Enabled,
		VLESS: input.VLESS, Hysteria2: input.Hysteria2,
		VMess: input.VMess, Trojan: input.Trojan,
		Shadowsocks: input.Shadowsocks,
		ExpiresAt:   input.ExpiresAt,
	}
}

func (handler managementHandler) cloneNode(
	response http.ResponseWriter,
	request *http.Request,
) {
	var input cloneNodeRequest
	if decodeJSON(response, request, &input) != nil {
		writeInvalidRequest(response, request)
		return
	}
	node, revision, err := handler.manager.CloneNode(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
		chi.URLParam(request, "nodeID"),
		service.CloneNodeSpec{
			Name: input.Name, Port: input.Port, IncludeUsers: input.IncludeUsers,
		},
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusCreated, listenerMutationResponse{
		Node: newListenerResponse(node), Revision: newRevisionResponse(revision),
	})
}

func (handler managementHandler) setNodesEnabled(
	response http.ResponseWriter,
	request *http.Request,
) {
	var input nodesEnabledRequest
	if decodeJSON(response, request, &input) != nil {
		writeInvalidRequest(response, request)
		return
	}
	nodes, revision, err := handler.manager.SetNodesEnabled(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
		input.NodeIDs,
		input.Enabled,
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	result := make([]listenerResponse, 0, len(nodes))
	for _, node := range nodes {
		result = append(result, newListenerResponse(node))
	}
	writePrivateJSON(response, http.StatusOK, nodesMutationResponse{
		Nodes: result, Revision: newRevisionResponse(revision),
	})
}

func (handler managementHandler) createUsers(
	response http.ResponseWriter,
	request *http.Request,
) {
	var input usersCreateRequest
	if decodeJSON(response, request, &input) != nil {
		writeInvalidRequest(response, request)
		return
	}
	specs := make([]service.UserSpec, 0, len(input.Users))
	for _, user := range input.Users {
		specs = append(specs, user.userSpec())
	}
	users, revision, err := handler.manager.CreateUsers(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
		chi.URLParam(request, "nodeID"),
		specs,
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	result := make([]userResponse, 0, len(users))
	for _, user := range users {
		result = append(result, newUserResponse(user))
	}
	writePrivateJSON(response, http.StatusCreated, usersMutationResponse{
		Users: result, Revision: newRevisionResponse(revision),
	})
}

func (handler managementHandler) setUsersEnabled(
	response http.ResponseWriter,
	request *http.Request,
) {
	var input usersEnabledRequest
	if decodeJSON(response, request, &input) != nil {
		writeInvalidRequest(response, request)
		return
	}
	users, revision, err := handler.manager.SetUsersEnabled(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
		chi.URLParam(request, "nodeID"),
		input.UserIDs,
		input.Enabled,
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	result := make([]userResponse, 0, len(users))
	for _, user := range users {
		result = append(result, newUserResponse(user))
	}
	writePrivateJSON(response, http.StatusOK, usersMutationResponse{
		Users: result, Revision: newRevisionResponse(revision),
	})
}
