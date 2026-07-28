package mihomo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const maxControllerResponse = 4 * 1024 * 1024

type Controller struct {
	baseURL *url.URL
	secret  string
	client  *http.Client
}

func NewController(address, secret string) (*Controller, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return nil, errors.New("Mihomo Controller address must use host:port syntax")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return nil, errors.New("Mihomo Controller port must be between 1 and 65535")
	}
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return nil, errors.New("Mihomo Controller must use a loopback address")
	}
	if strings.TrimSpace(secret) == "" {
		return nil, errors.New("Mihomo Controller secret is required")
	}
	baseURL, err := url.Parse("http://" + address)
	if err != nil {
		return nil, errors.New("Mihomo Controller address is invalid")
	}
	return &Controller{
		baseURL: baseURL,
		secret:  secret,
		client:  &http.Client{Timeout: 5 * time.Second},
	}, nil
}

func (controller *Controller) Version(ctx context.Context) (Version, error) {
	var result Version
	if err := controller.requestJSON(ctx, http.MethodGet, "/version", nil, &result); err != nil {
		return Version{}, fmt.Errorf("read Mihomo Controller version: %w", err)
	}
	if result.Version == "" {
		return Version{}, errors.New("Mihomo Controller returned an empty version")
	}
	return result, nil
}

func (controller *Controller) Traffic(ctx context.Context) (TrafficSnapshot, error) {
	var result TrafficSnapshot
	if err := controller.requestJSON(ctx, http.MethodGet, "/traffic", nil, &result); err != nil {
		return TrafficSnapshot{}, fmt.Errorf("read Mihomo traffic: %w", err)
	}
	return result, nil
}

func (controller *Controller) Memory(ctx context.Context) (MemorySnapshot, error) {
	var result MemorySnapshot
	if err := controller.requestJSON(ctx, http.MethodGet, "/memory", nil, &result); err != nil {
		return MemorySnapshot{}, fmt.Errorf("read Mihomo memory: %w", err)
	}
	return result, nil
}

func (controller *Controller) Connections(ctx context.Context) (ConnectionsSnapshot, error) {
	var result ConnectionsSnapshot
	if err := controller.requestJSON(ctx, http.MethodGet, "/connections", nil, &result); err != nil {
		return ConnectionsSnapshot{}, fmt.Errorf("read Mihomo connections: %w", err)
	}
	return result, nil
}

func (controller *Controller) Reload(ctx context.Context, configPath string) error {
	if !filepath.IsAbs(configPath) {
		return errors.New("Mihomo configuration path must be absolute")
	}
	if err := controller.requestJSON(
		ctx,
		http.MethodPut,
		"/configs?force=true",
		struct {
			Path string `json:"path"`
		}{Path: configPath},
		nil,
	); err != nil {
		return fmt.Errorf("reload Mihomo configuration: %w", err)
	}
	return nil
}

func (controller *Controller) Restart(ctx context.Context, _ string) error {
	if err := controller.requestJSON(ctx, http.MethodPost, "/restart", nil, nil); err != nil {
		return fmt.Errorf("restart Mihomo through Controller: %w", err)
	}
	return nil
}

func (controller *Controller) requestJSON(
	ctx context.Context,
	method string,
	path string,
	requestBody any,
	responseBody any,
) error {
	var body io.Reader
	if requestBody != nil {
		var encoded bytes.Buffer
		if err := json.NewEncoder(&encoded).Encode(requestBody); err != nil {
			return errors.New("encode Mihomo Controller request")
		}
		body = &encoded
	}
	reference, err := url.Parse(path)
	if err != nil {
		return errors.New("build Mihomo Controller endpoint")
	}
	endpoint := controller.baseURL.ResolveReference(reference)
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return errors.New("create Mihomo Controller request")
	}
	request.Header.Set("Authorization", "Bearer "+controller.secret)
	if requestBody != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := controller.client.Do(request)
	if err != nil {
		return errors.New("Mihomo Controller is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxControllerResponse))
		return fmt.Errorf("Mihomo Controller returned HTTP %d", response.StatusCode)
	}
	if responseBody == nil || response.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxControllerResponse))
		return nil
	}
	if err := json.NewDecoder(
		io.LimitReader(response.Body, maxControllerResponse+1),
	).Decode(responseBody); err != nil {
		return errors.New("decode Mihomo Controller response")
	}
	return nil
}
