package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Aethersailor/m-ui/internal/domain"
)

const maxBatchMutationItems = 500

// CloneNodeSpec describes the user-visible identity of a cloned node. A new
// listen port is mandatory because desired-state validation reserves ports for
// disabled nodes as well as active ones.
type CloneNodeSpec struct {
	Name         string
	Port         string
	IncludeUsers bool
}

func (manager *Manager) CloneNode(
	ctx context.Context,
	actorAdminID, sourceNodeID string,
	spec CloneNodeSpec,
) (domain.Node, domain.Revision, error) {
	name := strings.TrimSpace(spec.Name)
	port := strings.TrimSpace(spec.Port)
	if name == "" || port == "" {
		return domain.Node{}, domain.Revision{}, ErrValidation
	}
	if _, err := domain.ParsePortRanges(port); err != nil {
		return domain.Node{}, domain.Revision{}, fmt.Errorf("%w: invalid clone port: %v", ErrValidation, err)
	}

	cloneID, err := domain.GenerateUUID()
	if err != nil {
		return domain.Node{}, domain.Revision{}, err
	}
	var cloned domain.Node
	revision, err := manager.mutate(
		ctx,
		actorAdminID,
		"clone node",
		"node.clone",
		"node",
		cloneID,
		"Cloned a node as a disabled draft with a distinct listen port.",
		func(state *domain.DesiredState, now time.Time) error {
			position := nodeIndex(*state, sourceNodeID)
			if position < 0 {
				return ErrNotFound
			}
			if name == state.Nodes[position].Name || port == state.Nodes[position].Port {
				return fmt.Errorf("%w: clone name and port must differ from the source node", ErrValidation)
			}
			encoded, marshalErr := json.Marshal(state.Nodes[position])
			if marshalErr != nil {
				return marshalErr
			}
			if unmarshalErr := json.Unmarshal(encoded, &cloned); unmarshalErr != nil {
				return unmarshalErr
			}

			oldSinglePort, oldIsSingle := domain.SinglePort(cloned.Port)
			newSinglePort, newIsSingle := domain.SinglePort(port)
			cloned.ID = cloneID
			cloned.Name = name
			cloned.Port = port
			cloned.Enabled = false
			cloned.Generation = 1
			cloned.CreatedAt = now
			cloned.UpdatedAt = now

			if !spec.IncludeUsers {
				cloned.Users = nil
			} else {
				for index := range cloned.Users {
					userID, generateErr := domain.GenerateUUID()
					if generateErr != nil {
						return generateErr
					}
					cloned.Users[index].ID = userID
					cloned.Users[index].NodeID = cloneID
					cloned.Users[index].CreatedAt = now
					cloned.Users[index].UpdatedAt = now
				}
			}
			for index := range cloned.AccessProfiles {
				profileID, generateErr := domain.GenerateUUID()
				if generateErr != nil {
					return generateErr
				}
				cloned.AccessProfiles[index].ID = profileID
				cloned.AccessProfiles[index].NodeID = cloneID
				cloned.AccessProfiles[index].CreatedAt = now
				cloned.AccessProfiles[index].UpdatedAt = now
				if oldIsSingle && newIsSingle && cloned.AccessProfiles[index].PublicPort == oldSinglePort {
					cloned.AccessProfiles[index].PublicPort = newSinglePort
				}
			}
			state.Nodes = append(state.Nodes, cloned)
			return nil
		})
	if err != nil {
		return domain.Node{}, domain.Revision{}, err
	}
	return cloned, revision, err
}

func (manager *Manager) SetNodesEnabled(
	ctx context.Context,
	actorAdminID string,
	nodeIDs []string,
	enabled bool,
) ([]domain.Node, domain.Revision, error) {
	ids, err := normalizeBatchIDs(nodeIDs)
	if err != nil {
		return nil, domain.Revision{}, err
	}
	updated := make([]domain.Node, 0, len(ids))
	action := "node.batch-disable"
	summary := "Disabled selected nodes in one publication."
	if enabled {
		action = "node.batch-enable"
		summary = "Enabled selected nodes in one publication."
	}
	revision, err := manager.mutate(
		ctx,
		actorAdminID,
		action,
		action,
		"node",
		strings.Join(ids, ","),
		summary,
		func(state *domain.DesiredState, now time.Time) error {
			for _, id := range ids {
				if nodeIndex(*state, id) < 0 {
					return ErrNotFound
				}
			}
			for _, id := range ids {
				position := nodeIndex(*state, id)
				state.Nodes[position].Enabled = enabled
				state.Nodes[position].Generation++
				state.Nodes[position].UpdatedAt = now
				updated = append(updated, state.Nodes[position])
			}
			return nil
		})
	if err != nil {
		return nil, domain.Revision{}, err
	}
	return updated, revision, err
}

func (manager *Manager) CreateUsers(
	ctx context.Context,
	actorAdminID, nodeID string,
	specs []UserSpec,
) ([]domain.NodeUser, domain.Revision, error) {
	if len(specs) == 0 || len(specs) > maxBatchMutationItems {
		return nil, domain.Revision{}, ErrValidation
	}
	created := make([]domain.NodeUser, 0, len(specs))
	revision, err := manager.mutate(
		ctx,
		actorAdminID,
		"create node users in batch",
		"user.batch-create",
		"node",
		nodeID,
		"Created node users in one publication.",
		func(state *domain.DesiredState, now time.Time) error {
			position := nodeIndex(*state, nodeID)
			if position < 0 {
				return ErrNotFound
			}
			pending := make([]domain.NodeUser, 0, len(specs))
			for _, spec := range specs {
				userID, generateErr := domain.GenerateUUID()
				if generateErr != nil {
					return generateErr
				}
				user, buildErr := userFromSpec(
					userID,
					nodeID,
					state.Nodes[position].Protocol,
					spec,
					now,
					shadowsocksCipher(state.Nodes[position]),
				)
				if buildErr != nil {
					return buildErr
				}
				pending = append(pending, user)
			}
			state.Nodes[position].Users = append(state.Nodes[position].Users, pending...)
			created = pending
			state.Nodes[position].Generation++
			state.Nodes[position].UpdatedAt = now
			return nil
		})
	if err != nil {
		return nil, domain.Revision{}, err
	}
	return created, revision, err
}

func (manager *Manager) SetUsersEnabled(
	ctx context.Context,
	actorAdminID, nodeID string,
	userIDs []string,
	enabled bool,
) ([]domain.NodeUser, domain.Revision, error) {
	ids, err := normalizeBatchIDs(userIDs)
	if err != nil {
		return nil, domain.Revision{}, err
	}
	updated := make([]domain.NodeUser, 0, len(ids))
	action := "user.batch-disable"
	summary := "Disabled selected node users in one publication."
	if enabled {
		action = "user.batch-enable"
		summary = "Enabled selected node users in one publication."
	}
	revision, err := manager.mutate(
		ctx,
		actorAdminID,
		action,
		action,
		"node",
		nodeID,
		summary,
		func(state *domain.DesiredState, now time.Time) error {
			nodePosition := nodeIndex(*state, nodeID)
			if nodePosition < 0 {
				return ErrNotFound
			}
			for _, id := range ids {
				if userIndex(state.Nodes[nodePosition], id) < 0 {
					return ErrNotFound
				}
			}
			for _, id := range ids {
				position := userIndex(state.Nodes[nodePosition], id)
				state.Nodes[nodePosition].Users[position].Enabled = enabled
				state.Nodes[nodePosition].Users[position].UpdatedAt = now
				updated = append(updated, state.Nodes[nodePosition].Users[position])
			}
			state.Nodes[nodePosition].Generation++
			state.Nodes[nodePosition].UpdatedAt = now
			return nil
		})
	if err != nil {
		return nil, domain.Revision{}, err
	}
	return updated, revision, err
}

func normalizeBatchIDs(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > maxBatchMutationItems {
		return nil, ErrValidation
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		id := strings.TrimSpace(value)
		if id == "" {
			return nil, ErrValidation
		}
		if _, exists := seen[id]; exists {
			return nil, ErrValidation
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	if len(result) == 0 {
		return nil, ErrValidation
	}
	return result, nil
}
