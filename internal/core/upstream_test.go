package core

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGitHubClientResolvesReleaseAndUsesETag(t *testing.T) {
	t.Parallel()
	payload := gzipPayload(t, []byte("synthetic-mihomo"))
	digest := sha256Hex(payload)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != "/repos/MetaCubeX/mihomo/releases/latest" {
			t.Fatalf("path = %s", request.URL.Path)
		}
		if request.Header.Get("User-Agent") != "m-ui-test" {
			t.Fatal("missing explicit User-Agent")
		}
		if calls.Add(1) == 2 {
			if request.Header.Get("If-None-Match") != `"release-etag"` {
				t.Fatal("missing If-None-Match")
			}
			response.WriteHeader(http.StatusNotModified)
			return
		}
		response.Header().Set("ETag", `"release-etag"`)
		writeRelease(t, response, releaseFixture{
			ID:          101,
			Tag:         "v1.2.3",
			PublishedAt: time.Unix(100, 0).UTC(),
			Assets: []assetFixture{{
				ID:      201,
				Name:    "mihomo-linux-amd64-compatible-v1.2.3.gz",
				Content: payload,
				Digest:  digest,
				BaseURL: serverURL(request),
			}},
		})
	}))
	defer server.Close()
	client := newTestGitHubClient(t, server.URL, "m-ui-test", "")

	first, err := client.Resolve(context.Background(), ChannelRelease, "amd64")
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.Resolve(context.Background(), ChannelRelease, "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if !first.SameRelease(second) || first.AssetDigestSHA256 != digest {
		t.Fatalf("release identities differ: %#v %#v", first, second)
	}
}

func TestAlphaIdentityChangesWhenRollingAssetChanges(t *testing.T) {
	t.Parallel()
	firstPayload := gzipPayload(t, []byte("alpha-one"))
	secondPayload := gzipPayload(t, []byte("alpha-two"))
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		call := calls.Add(1)
		payload := firstPayload
		assetID := int64(301)
		published := time.Unix(200, 0).UTC()
		if call > 1 {
			payload = secondPayload
			assetID = 302
			published = time.Unix(300, 0).UTC()
		}
		writeRelease(t, response, releaseFixture{
			ID:          102,
			Tag:         AlphaTag,
			Prerelease:  true,
			PublishedAt: published,
			Assets: []assetFixture{{
				ID:      assetID,
				Name:    "mihomo-linux-arm64-alpha.gz",
				Content: payload,
				Digest:  sha256Hex(payload),
				BaseURL: serverURL(request),
			}},
		})
	}))
	defer server.Close()
	client := newTestGitHubClient(t, server.URL, "m-ui-test", "")
	first, err := client.Resolve(context.Background(), ChannelAlpha, "arm64")
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.Resolve(context.Background(), ChannelAlpha, "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if first.TagName != second.TagName || first.SameRelease(second) {
		t.Fatalf("rolling alpha change was not detected: %#v %#v", first, second)
	}
}

func TestGitHubClientRejectsUnsafeReleaseMetadata(t *testing.T) {
	t.Parallel()
	payload := gzipPayload(t, []byte("synthetic"))
	baseAsset := assetFixture{
		ID:      1,
		Name:    "mihomo-linux-amd64-compatible-v1.0.0.gz",
		Content: payload,
		Digest:  sha256Hex(payload),
	}
	tests := []struct {
		name    string
		release releaseFixture
	}{
		{
			name: "stable prerelease",
			release: releaseFixture{
				ID:         1,
				Tag:        "v1.0.0-rc.1",
				Prerelease: true,
				Assets:     []assetFixture{baseAsset},
			},
		},
		{
			name: "missing digest",
			release: releaseFixture{
				ID:     1,
				Tag:    "v1.0.0",
				Assets: []assetFixture{{ID: 1, Name: baseAsset.Name, Content: payload}},
			},
		},
		{
			name: "zero matches",
			release: releaseFixture{
				ID:  1,
				Tag: "v1.0.0",
				Assets: []assetFixture{{
					ID:      1,
					Name:    "mihomo-windows-amd64-v1.0.0.zip",
					Content: payload,
					Digest:  sha256Hex(payload),
				}},
			},
		},
		{
			name: "multiple matches",
			release: releaseFixture{
				ID:  1,
				Tag: "v1.0.0",
				Assets: []assetFixture{
					baseAsset,
					{
						ID:      2,
						Name:    "mihomo-linux-amd64-compatible-copy.gz",
						Content: payload,
						Digest:  sha256Hex(payload),
					},
				},
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(
				response http.ResponseWriter,
				request *http.Request,
			) {
				for index := range test.release.Assets {
					test.release.Assets[index].BaseURL = server.URL
				}
				writeRelease(t, response, test.release)
			}))
			defer server.Close()
			client := newTestGitHubClient(t, server.URL, "m-ui-test", "")
			if _, err := client.Resolve(
				context.Background(),
				ChannelRelease,
				"amd64",
			); err == nil {
				t.Fatal("Resolve() error = nil")
			}
		})
	}
}

func TestGitHubClientHandlesRateLimitCancellationAndResponseLimit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		body   []byte
	}{
		{name: "rate limit", status: http.StatusTooManyRequests},
		{
			name:   "oversized response",
			status: http.StatusOK,
			body:   bytes.Repeat([]byte("x"), maxAPIResponse+1),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(
				response http.ResponseWriter,
				_ *http.Request,
			) {
				response.WriteHeader(test.status)
				_, _ = response.Write(test.body)
			}))
			defer server.Close()
			client := newTestGitHubClient(t, server.URL, "m-ui-test", "")
			if _, err := client.Resolve(
				context.Background(),
				ChannelRelease,
				"amd64",
			); err == nil {
				t.Fatal("Resolve() error = nil")
			}
		})
	}

	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		<-request.Context().Done()
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client := newTestGitHubClient(t, server.URL, "m-ui-test", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Resolve(ctx, ChannelRelease, "amd64"); err == nil {
		t.Fatal("Resolve(cancelled) error = nil")
	}
}

func TestGitHubClientDownloadVerifiesDigestAndRedactsCredentials(t *testing.T) {
	t.Parallel()
	payload := gzipPayload(t, []byte("verified core"))
	token := "github-token-must-not-leak"
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/repos/MetaCubeX/mihomo/releases/latest":
			if request.Header.Get("Authorization") != "Bearer "+token {
				t.Fatal("GitHub token was not sent in the API Authorization header")
			}
			writeRelease(t, response, releaseFixture{
				ID:  1,
				Tag: "v1.0.0",
				Assets: []assetFixture{{
					ID:      2,
					Name:    "mihomo-linux-amd64-compatible-v1.0.0.gz",
					Content: payload,
					Digest:  sha256Hex(payload),
					BaseURL: server.URL,
				}},
			})
		case "/asset":
			_, _ = response.Write(payload)
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := newTestGitHubClient(t, server.URL, "m-ui-test", token)
	identity, err := client.Resolve(context.Background(), ChannelRelease, "amd64")
	if err != nil {
		t.Fatal(err)
	}
	var destination bytes.Buffer
	digest, size, err := client.Download(
		context.Background(),
		identity,
		&destination,
	)
	if err != nil {
		t.Fatal(err)
	}
	if digest != sha256Hex(payload) || size != int64(len(payload)) {
		t.Fatalf("Download() = %s/%d", digest, size)
	}
	identity.AssetDigestSHA256 = strings.Repeat("0", 64)
	if _, _, err := client.Download(
		context.Background(),
		identity,
		&bytes.Buffer{},
	); err == nil || strings.Contains(err.Error(), token) ||
		strings.Contains(err.Error(), server.URL) {
		t.Fatalf("unsafe Download() error = %v", err)
	}
}

type releaseFixture struct {
	ID          int64
	Tag         string
	Prerelease  bool
	Draft       bool
	PublishedAt time.Time
	Assets      []assetFixture
}

type assetFixture struct {
	ID      int64
	Name    string
	Content []byte
	Digest  string
	BaseURL string
}

func writeRelease(
	t *testing.T,
	response http.ResponseWriter,
	fixture releaseFixture,
) {
	t.Helper()
	if fixture.PublishedAt.IsZero() {
		fixture.PublishedAt = time.Unix(100, 0).UTC()
	}
	type asset struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"`
		URL    string `json:"browser_download_url"`
	}
	assets := make([]asset, 0, len(fixture.Assets))
	for _, item := range fixture.Assets {
		assets = append(assets, asset{
			ID:   item.ID,
			Name: item.Name,
			Size: int64(len(item.Content)),
			Digest: func() string {
				if item.Digest == "" {
					return ""
				}
				return "sha256:" + item.Digest
			}(),
			URL: strings.TrimRight(item.BaseURL, "/") + "/asset",
		})
	}
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(struct {
		ID         int64     `json:"id"`
		Tag        string    `json:"tag_name"`
		Target     string    `json:"target_commitish"`
		Draft      bool      `json:"draft"`
		Prerelease bool      `json:"prerelease"`
		Published  time.Time `json:"published_at"`
		Assets     []asset   `json:"assets"`
	}{
		ID:         fixture.ID,
		Tag:        fixture.Tag,
		Target:     "Meta",
		Draft:      fixture.Draft,
		Prerelease: fixture.Prerelease,
		Published:  fixture.PublishedAt,
		Assets:     assets,
	}); err != nil {
		t.Fatal(err)
	}
}

func newTestGitHubClient(
	t *testing.T,
	apiBase, userAgent, token string,
) *GitHubClient {
	t.Helper()
	client, err := NewGitHubClient(GitHubClientOptions{
		APIBase:       apiBase,
		Token:         token,
		UserAgent:     userAgent,
		AllowTestHTTP: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func gzipPayload(t *testing.T, content []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func sha256Hex(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func serverURL(request *http.Request) string {
	return "http://" + request.Host
}
