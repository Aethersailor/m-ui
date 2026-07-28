package domain

import "time"

const (
	VLESSFlow         = "xtls-rprx-vision"
	PacketEncoding    = "xudp"
	ClientFingerprint = "chrome"
)

type DesiredState struct {
	AsOf              time.Time
	PanelTitle        string
	UILanguage        string
	ControllerAddress string
	ControllerSecret  string
	PublicHost        string
	Listeners         []Listener
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
