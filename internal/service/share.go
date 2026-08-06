package service

import (
	"errors"

	"github.com/Aethersailor/m-ui/internal/domain"
	"github.com/Aethersailor/m-ui/internal/protocol"
)

type Share struct {
	URI        string
	QRContent  string
	ClientYAML string
}

// BuildShare uses the same protocol module and normalized node model as the
// server compiler. It intentionally has no independent protocol defaults.
func BuildShare(
	state domain.DesiredState,
	nodeID, userID string,
) (Share, error) {
	var node *domain.Node
	for index := range state.Nodes {
		if state.Nodes[index].ID == nodeID {
			node = &state.Nodes[index]
			break
		}
	}
	if node == nil {
		return Share{}, errors.New("node not found")
	}
	var user *domain.NodeUser
	for index := range node.Users {
		if node.Users[index].ID == userID {
			user = &node.Users[index]
			break
		}
	}
	if user == nil {
		return Share{}, errors.New("user not found")
	}
	profile, ok := node.DefaultAccessProfile()
	if !ok {
		return Share{}, errors.New("default access profile not found")
	}
	compiled, err := protocol.DefaultRegistry().BuildShare(state, *node, *user, profile)
	if err != nil {
		return Share{}, err
	}
	return Share{URI: compiled.URI, QRContent: compiled.QRContent, ClientYAML: string(compiled.ClientYAML)}, nil
}
