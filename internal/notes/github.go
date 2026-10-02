package notes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/lov3g00d/helm-bumpcheck/internal/httpretry"
)

const (
	DefaultGitHubURL = "https://api.github.com"
	apiVersion       = "2022-11-28"
	maxResponseSize  = 20 << 20
)

type Release struct {
	Tag     string
	Version string
	Body    string
	URL     string
}

type ghRelease struct {
	TagName    string `json:"tag_name"`
	Body       string `json:"body"`
	HTMLURL    string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

type GitHub struct {
	BaseURL    string
	Token      string
	HTTP       *http.Client
	PerPage    int
	MaxPages   int
	MaxRetries int
	MaxWait    time.Duration
}

func NewGitHubFromEnv() *GitHub {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	baseURL := os.Getenv("GITHUB_API_URL")
	if baseURL == "" {
		baseURL = DefaultGitHubURL
	}
	return &GitHub{
		BaseURL:    baseURL,
		Token:      token,
		HTTP:       &http.Client{Timeout: 30 * time.Second},
		PerPage:    100,
		MaxPages:   10,
		MaxRetries: 2,
		MaxWait:    time.Minute,
	}
}

func Canonical(version string) string {
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	return semver.Canonical(version)
}

func (g *GitHub) Releases(ctx context.Context, repo, tagPrefix, from, to string) ([]Release, error) {
	if strings.Count(repo, "/") != 1 {
		return nil, fmt.Errorf("repo must be owner/name, got %q", repo)
	}
	lo, hi := Canonical(from), Canonical(to)
	if lo == "" || hi == "" {
		return nil, fmt.Errorf("versions must be semver, got %q and %q", from, to)
	}

	var out []Release
	for page := 1; page <= g.MaxPages; page++ {
		batch, err := g.page(ctx, repo, page)
		if err != nil {
			return nil, err
		}
		for _, r := range batch {
			if r.Draft || r.Prerelease || !strings.HasPrefix(r.TagName, tagPrefix) {
				continue
			}
			v := Canonical(strings.TrimPrefix(r.TagName, tagPrefix))
			if v == "" || semver.Compare(v, lo) <= 0 || semver.Compare(v, hi) > 0 {
				continue
			}
			out = append(out, Release{Tag: r.TagName, Version: v, Body: r.Body, URL: r.HTMLURL})
		}
		if len(batch) < g.PerPage {
			break
		}
	}

	sort.Slice(out, func(i, j int) bool { return semver.Compare(out[i].Version, out[j].Version) < 0 })
	return out, nil
}

func (g *GitHub) page(ctx context.Context, repo string, page int) ([]ghRelease, error) {
	for attempt := 0; ; attempt++ {
		out, wait, err := g.fetch(ctx, repo, page)
		if err == nil || wait < 0 || attempt >= g.MaxRetries || ctx.Err() != nil {
			return out, err
		}
		if wait > g.MaxWait {
			return nil, fmt.Errorf("%w (rate limited for %s, more than the %s this tool waits)", err, wait.Round(time.Second), g.MaxWait)
		}
		if err := httpretry.Sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
}

func (g *GitHub) fetch(ctx context.Context, repo string, page int) ([]ghRelease, time.Duration, error) {
	url := fmt.Sprintf("%s/repos/%s/releases?per_page=%d&page=%d", g.BaseURL, repo, g.PerPage, page)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, -1, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}

	resp, err := g.HTTP.Do(req)
	if err != nil {
		return nil, httpretry.Backoff(time.Second, 10*time.Second, 0), err
	}
	defer resp.Body.Close()

	body := io.LimitReader(resp.Body, maxResponseSize)
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(body, 1<<10))
		err := fmt.Errorf("github releases %s page %d: %s: %s", repo, page, resp.Status, strings.TrimSpace(string(msg)))
		if g.Token == "" && (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusUnprocessableEntity) {
			err = fmt.Errorf("%w (unauthenticated: set GITHUB_TOKEN, for example GITHUB_TOKEN=$(gh auth token))", err)
		}
		return nil, retryWait(resp), err
	}

	var out []ghRelease
	if err := json.NewDecoder(body).Decode(&out); err != nil {
		return nil, -1, fmt.Errorf("decode github releases %s page %d: %w", repo, page, err)
	}
	return out, -1, nil
}

func retryWait(resp *http.Response) time.Duration {
	switch {
	case resp.StatusCode >= 500:
		return httpretry.Backoff(time.Second, 10*time.Second, 0)
	case resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests:
		return -1
	}
	if d, ok := httpretry.After(resp.Header, time.Now()); ok {
		return d
	}
	if resp.Header.Get("X-Ratelimit-Remaining") == "0" {
		if reset, err := strconv.ParseInt(resp.Header.Get("X-Ratelimit-Reset"), 10, 64); err == nil {
			return max(time.Until(time.Unix(reset, 0)), 0)
		}
	}
	if resp.StatusCode == http.StatusTooManyRequests || strings.Contains(strings.ToLower(resp.Header.Get("X-Ratelimit-Resource")), "secondary") {
		return time.Minute
	}
	return -1
}
