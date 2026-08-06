package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	muicrypto "github.com/Aethersailor/m-ui/internal/crypto"
	"github.com/Aethersailor/m-ui/internal/domain"
)

type storedProtocolConfig struct {
	VLESS       *domain.VLESSSpec       `json:"vless,omitempty"`
	Hysteria2   *domain.Hysteria2Spec   `json:"hysteria2,omitempty"`
	VMess       *domain.VMessSpec       `json:"vmess,omitempty"`
	Trojan      *domain.TrojanSpec      `json:"trojan,omitempty"`
	Shadowsocks *domain.ShadowsocksSpec `json:"shadowsocks,omitempty"`
}

type storedProtocolSecrets struct {
	TLSPrivateKey             string            `json:"tls_private_key,omitempty"`
	VLESSECHKey               string            `json:"vless_ech_key,omitempty"`
	RealityPrivateKey         string            `json:"reality_private_key,omitempty"`
	ShadowTLSPassword         string            `json:"shadow_tls_password,omitempty"`
	ShadowTLSUserPasswords    map[string]string `json:"shadow_tls_user_passwords,omitempty"`
	ResTLSPassword            string            `json:"res_tls_password,omitempty"`
	JLSUserPasswords          map[string]string `json:"jls_user_passwords,omitempty"`
	Hysteria2PrivateKey       string            `json:"hysteria2_private_key,omitempty"`
	Hysteria2ECHKey           string            `json:"hysteria2_ech_key,omitempty"`
	Hysteria2ObfsPassword     string            `json:"hysteria2_obfs_password,omitempty"`
	Hysteria2RealmToken       string            `json:"hysteria2_realm_token,omitempty"`
	Hysteria2RealmKey         string            `json:"hysteria2_realm_private_key,omitempty"`
	TrojanShadowsocksPassword string            `json:"trojan_shadowsocks_password,omitempty"`
	VMessMKCPSeed             string            `json:"vmess_mkcp_seed,omitempty"`
}

func encodeNodeProtocol(
	sealer *muicrypto.Sealer,
	node domain.Node,
) (string, string, error) {
	config, err := cloneProtocolConfig(node)
	if err != nil {
		return "", "", err
	}
	secrets := storedProtocolSecrets{}
	security := storedStreamSecurity(&config)
	if security != nil {
		if security.TLS != nil {
			secrets.TLSPrivateKey = security.TLS.PrivateKey
			secrets.VLESSECHKey = security.TLS.ECHKey
			security.TLS.PrivateKey = ""
			security.TLS.ECHKey = ""
		}
		if security.Reality != nil {
			secrets.RealityPrivateKey = security.Reality.PrivateKey
			security.Reality.PrivateKey = ""
		}
		if security.ShadowTLS != nil {
			secrets.ShadowTLSPassword = security.ShadowTLS.Password
			security.ShadowTLS.Password = ""
			if len(security.ShadowTLS.Users) > 0 {
				secrets.ShadowTLSUserPasswords = make(map[string]string, len(security.ShadowTLS.Users))
				for index := range security.ShadowTLS.Users {
					user := &security.ShadowTLS.Users[index]
					secrets.ShadowTLSUserPasswords[user.Name] = user.Password
					user.Password = ""
				}
			}
		}
		if security.ResTLS != nil {
			secrets.ResTLSPassword = security.ResTLS.Password
			security.ResTLS.Password = ""
		}
		if security.JLS != nil && len(security.JLS.Users) > 0 {
			secrets.JLSUserPasswords = make(map[string]string, len(security.JLS.Users))
			for index := range security.JLS.Users {
				user := &security.JLS.Users[index]
				secrets.JLSUserPasswords[user.Username] = user.Password
				user.Password = ""
			}
		}
	}
	if config.Trojan != nil && config.Trojan.Shadowsocks.Enabled {
		secrets.TrojanShadowsocksPassword = config.Trojan.Shadowsocks.Password
		config.Trojan.Shadowsocks.Password = ""
	}
	if config.VMess != nil && config.VMess.Handler.MKCP != nil {
		secrets.VMessMKCPSeed = config.VMess.Handler.MKCP.Seed
		config.VMess.Handler.MKCP.Seed = ""
	}
	if config.Hysteria2 != nil {
		secrets.Hysteria2PrivateKey = config.Hysteria2.PrivateKey
		secrets.Hysteria2ECHKey = config.Hysteria2.ECHKey
		secrets.Hysteria2ObfsPassword = config.Hysteria2.ObfsPassword
		config.Hysteria2.PrivateKey = ""
		config.Hysteria2.ECHKey = ""
		config.Hysteria2.ObfsPassword = ""
		if config.Hysteria2.Realm != nil {
			secrets.Hysteria2RealmToken = config.Hysteria2.Realm.Token
			secrets.Hysteria2RealmKey = config.Hysteria2.Realm.PrivateKey
			config.Hysteria2.Realm.Token = ""
			config.Hysteria2.Realm.PrivateKey = ""
		}
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		return "", "", errors.New("encode canonical node protocol configuration")
	}
	secretJSON, err := json.Marshal(secrets)
	if err != nil {
		return "", "", errors.New("encode node protocol secrets")
	}
	ciphertext, err := sealer.Encrypt(secretJSON, nodeProtocolPurpose(node))
	if err != nil {
		return "", "", errors.New("encrypt node protocol secrets")
	}
	return string(configJSON), ciphertext, nil
}

func decodeNodeProtocol(
	sealer *muicrypto.Sealer,
	node *domain.Node,
	configJSON, ciphertext string,
) error {
	var config storedProtocolConfig
	if err := decodeStrictJSON([]byte(configJSON), &config); err != nil {
		return errors.New("decode canonical node protocol configuration")
	}
	plaintext, err := sealer.Decrypt(ciphertext, nodeProtocolPurpose(*node))
	if err != nil {
		return errors.New("decrypt node protocol secrets")
	}
	var secrets storedProtocolSecrets
	if err := decodeStrictJSON(plaintext, &secrets); err != nil {
		return errors.New("decode node protocol secrets")
	}
	security := storedStreamSecurity(&config)
	if security != nil {
		if security.TLS != nil {
			security.TLS.PrivateKey = secrets.TLSPrivateKey
			security.TLS.ECHKey = secrets.VLESSECHKey
		}
		if security.Reality != nil {
			security.Reality.PrivateKey = secrets.RealityPrivateKey
		}
		if security.ShadowTLS != nil {
			security.ShadowTLS.Password = secrets.ShadowTLSPassword
			for index := range security.ShadowTLS.Users {
				user := &security.ShadowTLS.Users[index]
				user.Password = secrets.ShadowTLSUserPasswords[user.Name]
			}
		}
		if security.ResTLS != nil {
			security.ResTLS.Password = secrets.ResTLSPassword
		}
		if security.JLS != nil {
			for index := range security.JLS.Users {
				user := &security.JLS.Users[index]
				user.Password = secrets.JLSUserPasswords[user.Username]
			}
		}
	}
	if config.Trojan != nil && config.Trojan.Shadowsocks.Enabled {
		config.Trojan.Shadowsocks.Password = secrets.TrojanShadowsocksPassword
	}
	if config.VMess != nil && config.VMess.Handler.MKCP != nil {
		config.VMess.Handler.MKCP.Seed = secrets.VMessMKCPSeed
	}
	if config.Hysteria2 != nil {
		config.Hysteria2.PrivateKey = secrets.Hysteria2PrivateKey
		config.Hysteria2.ECHKey = secrets.Hysteria2ECHKey
		config.Hysteria2.ObfsPassword = secrets.Hysteria2ObfsPassword
		if config.Hysteria2.Realm != nil {
			config.Hysteria2.Realm.Token = secrets.Hysteria2RealmToken
			config.Hysteria2.Realm.PrivateKey = secrets.Hysteria2RealmKey
		}
	}
	node.VLESS = config.VLESS
	node.Hysteria2 = config.Hysteria2
	node.VMess = config.VMess
	node.Trojan = config.Trojan
	node.Shadowsocks = config.Shadowsocks
	return nil
}

func encodeUserCredential(
	sealer *muicrypto.Sealer,
	user domain.NodeUser,
	kind domain.ProtocolKind,
) (string, error) {
	var credential any
	switch kind {
	case domain.ProtocolVLESS:
		credential = user.VLESS
	case domain.ProtocolHysteria2:
		credential = user.Hysteria2
	case domain.ProtocolVMess:
		credential = user.VMess
	case domain.ProtocolTrojan:
		credential = user.Trojan
	case domain.ProtocolShadowsocks:
		credential = user.Shadowsocks
	default:
		return "", fmt.Errorf("unsupported credential kind %q", kind)
	}
	plaintext, err := json.Marshal(credential)
	if err != nil || string(plaintext) == "null" {
		return "", errors.New("encode node user credential")
	}
	ciphertext, err := sealer.Encrypt(plaintext, nodeUserCredentialPurpose(user, kind))
	if err != nil {
		return "", errors.New("encrypt node user credential")
	}
	return ciphertext, nil
}

func decodeUserCredential(
	sealer *muicrypto.Sealer,
	user *domain.NodeUser,
	kind domain.ProtocolKind,
	ciphertext string,
) error {
	plaintext, err := sealer.Decrypt(ciphertext, nodeUserCredentialPurpose(*user, kind))
	if err != nil {
		return errors.New("decrypt node user credential")
	}
	switch kind {
	case domain.ProtocolVLESS:
		var credential domain.VLESSCredential
		if err := decodeStrictJSON(plaintext, &credential); err != nil {
			return errors.New("decode VLESS user credential")
		}
		user.VLESS = &credential
	case domain.ProtocolHysteria2:
		var credential domain.Hysteria2Credential
		if err := decodeStrictJSON(plaintext, &credential); err != nil {
			return errors.New("decode Hysteria2 user credential")
		}
		user.Hysteria2 = &credential
	case domain.ProtocolVMess:
		var credential domain.VMessCredential
		if err := decodeStrictJSON(plaintext, &credential); err != nil {
			return errors.New("decode VMess user credential")
		}
		user.VMess = &credential
	case domain.ProtocolTrojan:
		var credential domain.TrojanCredential
		if err := decodeStrictJSON(plaintext, &credential); err != nil {
			return errors.New("decode Trojan user credential")
		}
		user.Trojan = &credential
	case domain.ProtocolShadowsocks:
		var credential domain.ShadowsocksCredential
		if err := decodeStrictJSON(plaintext, &credential); err != nil {
			return errors.New("decode Shadowsocks user credential")
		}
		user.Shadowsocks = &credential
	default:
		return fmt.Errorf("unsupported credential kind %q", kind)
	}
	return nil
}

func cloneProtocolConfig(node domain.Node) (storedProtocolConfig, error) {
	encoded, err := json.Marshal(storedProtocolConfig{
		VLESS: node.VLESS, Hysteria2: node.Hysteria2, VMess: node.VMess,
		Trojan: node.Trojan, Shadowsocks: node.Shadowsocks,
	})
	if err != nil {
		return storedProtocolConfig{}, err
	}
	var cloned storedProtocolConfig
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return storedProtocolConfig{}, err
	}
	return cloned, nil
}

func storedStreamSecurity(config *storedProtocolConfig) *domain.VLESSSecuritySpec {
	switch {
	case config.VLESS != nil:
		return &config.VLESS.Security
	case config.VMess != nil:
		return &config.VMess.Security
	case config.Trojan != nil:
		return &config.Trojan.Security
	case config.Shadowsocks != nil:
		return &config.Shadowsocks.Security
	default:
		return nil
	}
}

func decodeStrictJSON(content []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON data")
	}
	return nil
}

func nodeProtocolPurpose(node domain.Node) string {
	return fmt.Sprintf("node:%s:%s:v%d:protocol", node.ID, node.Protocol, node.SchemaVersion)
}

func nodeUserCredentialPurpose(user domain.NodeUser, kind domain.ProtocolKind) string {
	return fmt.Sprintf("node:%s:user:%s:%s:v%d:credential", user.NodeID, user.ID, kind, domain.NodeSchemaVersion)
}
