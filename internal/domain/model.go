package domain

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"
)

const (
	VLESSFlow         = "xtls-rprx-vision"
	PacketEncoding    = "xudp"
	ClientFingerprint = "chrome"
)

type DesiredState struct {
	AsOf                          time.Time
	PanelTitle                    string
	UILanguage                    string
	PanelUIBind                   Endpoint
	MihomoExternalControllerBind  Endpoint
	MihomoControllerConnect       Endpoint
	ExternalControllerCORSOrigins []string
	EndpointGeneration            int64
	ControllerAddress             string // Deprecated: accepted only for legacy state migration.
	ControllerSecret              string
	PublicHost                    string
	Listeners                     []Listener
}

// Endpoint is a typed host/port pair. Hosts are stored without IPv6 brackets;
// Address formats them with net.JoinHostPort at the process boundary.
type Endpoint struct {
	Host string
	Port uint16
}

func (endpoint Endpoint) Address() string {
	return net.JoinHostPort(endpoint.Host, strconv.Itoa(int(endpoint.Port)))
}

func (endpoint Endpoint) Equal(other Endpoint) bool {
	return endpoint.Host == other.Host && endpoint.Port == other.Port
}

// SplitLegacyControllerEndpoint converts the one controller_address value
// used by older releases into the two endpoint roles used now. A wildcard is
// valid for Mihomo's listener, but never for m-ui's client target.
func SplitLegacyControllerEndpoint(endpoint Endpoint) (Endpoint, Endpoint, error) {
	if endpoint.Port == 0 {
		return Endpoint{}, Endpoint{}, errors.New("legacy controller endpoint port is invalid")
	}
	switch endpoint.Host {
	case "127.0.0.1":
		return endpoint, endpoint, nil
	case "0.0.0.0":
		return endpoint, Endpoint{Host: "127.0.0.1", Port: endpoint.Port}, nil
	case "::1":
		return endpoint, endpoint, nil
	case "::":
		return endpoint, Endpoint{Host: "::1", Port: endpoint.Port}, nil
	default:
		return Endpoint{}, Endpoint{}, fmt.Errorf(
			"legacy controller endpoint host %q cannot be safely migrated; use 127.0.0.1, 0.0.0.0, ::1, or ::",
			endpoint.Host,
		)
	}
}

// NormalizeLegacy fills the endpoint fields for states created by versions
// that used one controller_address value for both Mihomo's bind address and
// m-ui's client target. It is intentionally deterministic and only accepts a
// legacy value that can be safely split by SplitLegacyControllerEndpoint.
func (state DesiredState) NormalizeLegacy() (DesiredState, error) {
	if state.PanelUIBind.Port == 0 && state.PanelUIBind.Host == "" {
		state.PanelUIBind = Endpoint{Host: "127.0.0.1", Port: 2095}
	}
	var err error
	var legacyBind, legacyConnect Endpoint
	var hasLegacy bool
	if state.ControllerAddress != "" &&
		(state.MihomoExternalControllerBind.Port == 0 ||
			state.MihomoExternalControllerBind.Host == "" ||
			state.MihomoControllerConnect.Port == 0 ||
			state.MihomoControllerConnect.Host == "") {
		legacy, parseErr := ParseEndpoint(state.ControllerAddress)
		if parseErr != nil {
			return DesiredState{}, parseErr
		}
		legacyBind, legacyConnect, err = SplitLegacyControllerEndpoint(legacy)
		if err != nil {
			return DesiredState{}, err
		}
		hasLegacy = true
	}
	if state.MihomoExternalControllerBind.Port == 0 &&
		state.MihomoExternalControllerBind.Host == "" && hasLegacy {
		state.MihomoExternalControllerBind = legacyBind
	}
	if state.MihomoControllerConnect.Port == 0 &&
		state.MihomoControllerConnect.Host == "" {
		switch {
		case hasLegacy:
			state.MihomoControllerConnect = legacyConnect
		case state.MihomoExternalControllerBind.Port != 0 ||
			state.MihomoExternalControllerBind.Host != "":
			_, state.MihomoControllerConnect, err = SplitLegacyControllerEndpoint(
				state.MihomoExternalControllerBind,
			)
			if err != nil {
				return DesiredState{}, err
			}
		}
	}
	if state.EndpointGeneration == 0 {
		state.EndpointGeneration = 1
	}
	return state, nil
}

func ParseEndpoint(address string) (Endpoint, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return Endpoint{}, err
	}
	parsedPort, err := strconv.ParseUint(port, 10, 16)
	if err != nil || parsedPort == 0 {
		return Endpoint{}, strconv.ErrRange
	}
	return Endpoint{Host: host, Port: uint16(parsedPort)}, nil
}

type Listener struct {
	ID                 string
	Name               string
	Enabled            bool
	ListenAddress      string
	ListenPort         uint16
	PublicHostOverride string
	PublicPortOverride *uint16
	ServerName         string
	RealityDest        string
	RealityPrivateKey  string
	RealityPublicKey   string
	ShortID            string
	UDPEnabled         bool
	Users              []User
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type User struct {
	ID         string
	ListenerID string
	Name       string
	Enabled    bool
	UUID       string
	ExpiresAt  *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Keypair struct {
	PrivateKey string
	PublicKey  string
}

func (listener Listener) EffectiveUsers(asOf time.Time) []User {
	users := make([]User, 0, len(listener.Users))
	for _, user := range listener.Users {
		if !user.Enabled {
			continue
		}
		if user.ExpiresAt != nil && !user.ExpiresAt.After(asOf) {
			continue
		}
		users = append(users, user)
	}
	return users
}

func (listener Listener) PublicEndpoint(defaultHost string) (string, uint16) {
	host := defaultHost
	if listener.PublicHostOverride != "" {
		host = listener.PublicHostOverride
	}
	port := listener.ListenPort
	if listener.PublicPortOverride != nil {
		port = *listener.PublicPortOverride
	}
	return host, port
}
