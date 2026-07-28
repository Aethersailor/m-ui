package service

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/url"

	"github.com/Aethersailor/m-ui/internal/domain"
	"gopkg.in/yaml.v3"
)

type Share struct {
	URI        string
	QRContent  string
	ClientYAML string
}

func BuildShare(state domain.DesiredState, listenerID, userID string) (Share, error) {
	if err := state.Validate(); err != nil {
		return Share{}, fmt.Errorf("validate desired state: %w", err)
	}
	var selectedListener *domain.Listener
	for index := range state.Listeners {
		if state.Listeners[index].ID == listenerID {
			selectedListener = &state.Listeners[index]
			break
		}
	}
	if selectedListener == nil {
		return Share{}, errors.New("listener not found")
	}
	if !selectedListener.Enabled {
		return Share{}, errors.New("cannot share a disabled listener")
	}
	var selectedUser *domain.User
	for index := range selectedListener.Users {
		if selectedListener.Users[index].ID == userID {
			selectedUser = &selectedListener.Users[index]
			break
		}
	}
	if selectedUser == nil {
		return Share{}, errors.New("user not found")
	}
	if !selectedUser.Enabled ||
		(selectedUser.ExpiresAt != nil && !selectedUser.ExpiresAt.After(state.AsOf)) {
		return Share{}, errors.New("cannot share a disabled or expired user")
	}

	host, port := selectedListener.PublicEndpoint(state.PublicHost)
	parameters := url.Values{
		"encryption": {"none"},
		"flow":       {domain.VLESSFlow},
		"fp":         {domain.ClientFingerprint},
		"pbk":        {selectedListener.RealityPublicKey},
		"security":   {"reality"},
		"sid":        {selectedListener.ShortID},
		"sni":        {selectedListener.ServerName},
		"type":       {"tcp"},
	}
	if selectedListener.UDPEnabled {
		parameters.Set("packetEncoding", domain.PacketEncoding)
	}
	shareURL := url.URL{
		Scheme:   "vless",
		User:     url.User(selectedUser.UUID),
		Host:     net.JoinHostPort(host, fmt.Sprint(port)),
		RawQuery: parameters.Encode(),
		Fragment: selectedListener.Name + " - " + selectedUser.Name,
	}
	uri := shareURL.String()
	clientYAML, err := compileClientYAML(*selectedListener, *selectedUser, host, port)
	if err != nil {
		return Share{}, err
	}
	return Share{URI: uri, QRContent: uri, ClientYAML: string(clientYAML)}, nil
}

func compileClientYAML(listener domain.Listener, user domain.User, host string, port uint16) ([]byte, error) {
	document := clientConfiguration{
		Proxies: []clientProxy{{
			Name:              listener.Name + " - " + user.Name,
			Type:              "vless",
			Server:            host,
			Port:              port,
			UDP:               listener.UDPEnabled,
			UUID:              user.UUID,
			Flow:              domain.VLESSFlow,
			PacketEncoding:    domain.PacketEncoding,
			TLS:               true,
			ServerName:        listener.ServerName,
			ClientFingerprint: domain.ClientFingerprint,
			Reality: clientReality{
				PublicKey: listener.RealityPublicKey,
				ShortID:   listener.ShortID,
			},
			Encryption: "",
			Network:    "tcp",
		}},
	}
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return nil, fmt.Errorf("encode Mihomo client YAML: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("finish Mihomo client YAML: %w", err)
	}
	return output.Bytes(), nil
}

type clientConfiguration struct {
	Proxies []clientProxy `yaml:"proxies"`
}

type clientProxy struct {
	Name              string        `yaml:"name"`
	Type              string        `yaml:"type"`
	Server            string        `yaml:"server"`
	Port              uint16        `yaml:"port"`
	UDP               bool          `yaml:"udp"`
	UUID              string        `yaml:"uuid"`
	Flow              string        `yaml:"flow"`
	PacketEncoding    string        `yaml:"packet-encoding"`
	TLS               bool          `yaml:"tls"`
	ServerName        string        `yaml:"servername"`
	ClientFingerprint string        `yaml:"client-fingerprint"`
	Reality           clientReality `yaml:"reality-opts"`
	Encryption        string        `yaml:"encryption"`
	Network           string        `yaml:"network"`
}

type clientReality struct {
	PublicKey string `yaml:"public-key"`
	ShortID   string `yaml:"short-id"`
}
