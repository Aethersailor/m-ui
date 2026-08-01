package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	UpstreamRepository = "MetaCubeX/mihomo"
	AlphaTag           = "Prerelease-Alpha"
	defaultAPIBase     = "https://api.github.com"
	maxAPIResponse     = 4 << 20
	maxAssetSize       = 128 << 20
)

type GitHubClientOptions struct {
	HTTPClient    *http.Client
	APIBase       string
	Token         string
	UserAgent     string
	AllowTestHTTP bool
}

type GitHubClient struct {
	client        *http.Client
	apiBase       string
	token         string
	userAgent     string
	allowTestHTTP bool
	mutex         sync.Mutex
	etag          map[string]string
	cache         map[string]ReleaseIdentity
}

type githubRelease struct {
	ID              int64         `json:"id"`
	TagName         string        `json:"tag_name"`
	TargetCommitish string        `json:"target_commitish"`
	Draft           bool          `json:"draft"`
	Prerelease      bool          `json:"prerelease"`
	PublishedAt     time.Time     `json:"published_at"`
	Assets          []githubAsset `json:"assets"`
}

type githubAsset struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func NewGitHubClient(options GitHubClientOptions) (*GitHubClient, error) {
	apiBase := strings.TrimRight(options.APIBase, "/")
	if apiBase == "" {
		apiBase = defaultAPIBase
	}
	parsedBase, err := url.Parse(apiBase)
	if err != nil || parsedBase.Host == "" {
		return nil, errors.New("invalid fixed GitHub API base")
	}
	if !options.AllowTestHTTP &&
		(parsedBase.Scheme != "https" || parsedBase.Host != "api.github.com") {
		return nil, errors.New("GitHub API base must be the fixed official endpoint")
	}
	userAgent := strings.TrimSpace(options.UserAgent)
	if userAgent == "" {
		userAgent = "m-ui/0.1 core-updater"
	}
	token := options.Token
	if token == "" {
		token = os.Getenv("M_UI_GITHUB_TOKEN")
	}
	client := options.HTTPClient
	if client == nil {
		transport := &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			IdleConnTimeout:       30 * time.Second,
		}
		client = &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		}
	} else {
		clone := *client
		client = &clone
	}
	originalRedirect := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if options.AllowTestHTTP &&
			request.URL.Host == parsedBase.Host {
			return nil
		}
		if request.URL.Scheme != "https" || !trustedGitHubHost(request.URL.Hostname()) {
			return errors.New("redirect target is not a trusted GitHub HTTPS host")
		}
		if originalRedirect != nil {
			return originalRedirect(request, via)
		}
		return nil
	}
	return &GitHubClient{
		client:        client,
		apiBase:       apiBase,
		token:         token,
		userAgent:     userAgent,
		allowTestHTTP: options.AllowTestHTTP,
		etag:          make(map[string]string),
		cache:         make(map[string]ReleaseIdentity),
	}, nil
}

func (client *GitHubClient) Resolve(
	ctx context.Context,
	channel Channel,
	architecture string,
) (ReleaseIdentity, error) {
	if _, err := ParseChannel(string(channel)); err != nil {
		return ReleaseIdentity{}, err
	}
	if architecture == "" {
		architecture = runtime.GOARCH
	}
	if architecture != "amd64" && architecture != "arm64" {
		return ReleaseIdentity{}, errors.New("only linux amd64 and arm64 are supported")
	}
	endpoint := "/repos/MetaCubeX/mihomo/releases/latest"
	if channel == ChannelAlpha {
		endpoint = "/repos/MetaCubeX/mihomo/releases/tags/" + AlphaTag
	}
	cacheKey := string(channel) + ":" + architecture
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		client.apiBase+endpoint,
		nil,
	)
	if err != nil {
		return ReleaseIdentity{}, errors.New("create GitHub release request")
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", client.userAgent)
	if client.token != "" {
		request.Header.Set("Authorization", "Bearer "+client.token)
	}
	client.mutex.Lock()
	etag := client.etag[cacheKey]
	client.mutex.Unlock()
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}

	response, err := client.client.Do(request)
	if err != nil {
		return ReleaseIdentity{}, errors.New("query Mihomo release metadata")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotModified {
		client.mutex.Lock()
		cached, ok := client.cache[cacheKey]
		client.mutex.Unlock()
		if !ok {
			return ReleaseIdentity{}, errors.New("GitHub returned 304 without cached release metadata")
		}
		return cached, nil
	}
	if response.StatusCode == http.StatusForbidden ||
		response.StatusCode == http.StatusTooManyRequests {
		return ReleaseIdentity{}, errors.New("GitHub API rate limit prevented the core check")
	}
	if response.StatusCode != http.StatusOK {
		return ReleaseIdentity{}, fmt.Errorf(
			"GitHub release metadata returned HTTP %d",
			response.StatusCode,
		)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAPIResponse+1))
	if err != nil {
		return ReleaseIdentity{}, errors.New("read Mihomo release metadata")
	}
	if len(body) > maxAPIResponse {
		return ReleaseIdentity{}, errors.New("mihomo release metadata is too large")
	}
	var release githubRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return ReleaseIdentity{}, errors.New("decode Mihomo release metadata")
	}
	if release.Draft || (channel == ChannelRelease && release.Prerelease) {
		return ReleaseIdentity{}, errors.New("mihomo release metadata does not match the requested channel")
	}
	if channel == ChannelAlpha && release.TagName != AlphaTag {
		return ReleaseIdentity{}, errors.New("mihomo alpha release tag is invalid")
	}
	asset, err := selectAsset(release.Assets, architecture)
	if err != nil {
		return ReleaseIdentity{}, err
	}
	digest := strings.TrimPrefix(strings.ToLower(asset.Digest), "sha256:")
	if !validSHA256(digest) {
		return ReleaseIdentity{}, errors.New("target Mihomo asset has no trusted SHA-256 digest")
	}
	if asset.Size <= 0 || asset.Size > maxAssetSize {
		return ReleaseIdentity{}, errors.New("target Mihomo asset size is invalid")
	}
	if err := validateDownloadURL(asset.BrowserDownloadURL, client.allowTestHTTP, client.apiBase); err != nil {
		return ReleaseIdentity{}, err
	}
	identity := ReleaseIdentity{
		Channel:            channel,
		Repository:         UpstreamRepository,
		ReleaseID:          release.ID,
		TagName:            release.TagName,
		Prerelease:         release.Prerelease,
		PublishedAt:        release.PublishedAt.UTC(),
		TargetCommitish:    release.TargetCommitish,
		AssetID:            asset.ID,
		AssetName:          asset.Name,
		AssetSize:          asset.Size,
		AssetDigestSHA256:  digest,
		BrowserDownloadURL: asset.BrowserDownloadURL,
	}
	if err := identity.Validate(); err != nil {
		return ReleaseIdentity{}, err
	}
	client.mutex.Lock()
	if value := response.Header.Get("ETag"); value != "" {
		client.etag[cacheKey] = value
	}
	client.cache[cacheKey] = identity
	client.mutex.Unlock()
	return identity, nil
}

func (client *GitHubClient) Download(
	ctx context.Context,
	identity ReleaseIdentity,
	destination io.Writer,
) (string, int64, error) {
	if err := identity.Validate(); err != nil {
		return "", 0, err
	}
	if err := validateDownloadURL(
		identity.BrowserDownloadURL,
		client.allowTestHTTP,
		client.apiBase,
	); err != nil {
		return "", 0, err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		identity.BrowserDownloadURL,
		nil,
	)
	if err != nil {
		return "", 0, errors.New("create Mihomo asset request")
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", client.userAgent)
	response, err := client.client.Do(request)
	if err != nil {
		return "", 0, errors.New("download Mihomo asset")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf(
			"Mihomo asset download returned HTTP %d",
			response.StatusCode,
		)
	}
	if response.ContentLength > identity.AssetSize ||
		response.ContentLength > maxAssetSize {
		return "", 0, errors.New("Mihomo asset response is larger than expected")
	}
	hasher := sha256.New()
	limited := &io.LimitedReader{
		R: response.Body,
		N: identity.AssetSize + 1,
	}
	written, err := io.Copy(io.MultiWriter(destination, hasher), limited)
	if err != nil {
		return "", 0, errors.New("store Mihomo asset")
	}
	if written != identity.AssetSize || limited.N <= 0 {
		return "", 0, errors.New("Mihomo asset size does not match release metadata")
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if digest != identity.AssetDigestSHA256 {
		return "", 0, errors.New("Mihomo asset SHA-256 verification failed")
	}
	return digest, written, nil
}

func selectAsset(assets []githubAsset, architecture string) (githubAsset, error) {
	prefix := "mihomo-linux-arm64-"
	if architecture == "amd64" {
		prefix = "mihomo-linux-amd64-compatible-"
	}
	var matches []githubAsset
	for _, asset := range assets {
		name := strings.ToLower(asset.Name)
		if strings.HasPrefix(name, prefix) &&
			strings.HasSuffix(name, ".gz") &&
			!strings.Contains(name, "-go") &&
			!strings.Contains(name, "debug") {
			matches = append(matches, asset)
		}
	}
	if len(matches) != 1 {
		return githubAsset{}, fmt.Errorf(
			"Mihomo release contains %d matching linux/%s gzip assets; expected exactly one",
			len(matches),
			architecture,
		)
	}
	return matches[0], nil
}

func trustedGitHubHost(host string) bool {
	host = strings.ToLower(host)
	return host == "github.com" ||
		host == "api.github.com" ||
		host == "objects.githubusercontent.com" ||
		host == "release-assets.githubusercontent.com" ||
		strings.HasSuffix(host, ".githubusercontent.com")
}

func validateDownloadURL(value string, allowTestHTTP bool, apiBase string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return errors.New("Mihomo asset download URL is invalid")
	}
	if allowTestHTTP {
		base, _ := url.Parse(apiBase)
		if parsed.Host == base.Host {
			return nil
		}
	}
	if parsed.Scheme != "https" || !trustedGitHubHost(parsed.Hostname()) {
		return errors.New("Mihomo asset download URL is not a trusted GitHub HTTPS URL")
	}
	return nil
}
