package notes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
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
	maxErrorBody     = 1 << 10

	unauthenticatedResults = 1000
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
	UserAgent  string
	HTTP       *http.Client
	PerPage    int
	MaxPages   int
	MaxRetries int
	MaxWait    time.Duration
	Backoff    time.Duration
	MaxBackoff time.Duration
}

type Truncation struct {
	Pages     int
	Oldest    string
	ResultCap bool
}

var errResultCap = errors.New("github returns only the first 1000 results")

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
		UserAgent:  "helm-bumpcheck",
		HTTP:       &http.Client{Timeout: 30 * time.Second},
		PerPage:    100,
		MaxPages:   10,
		MaxRetries: 2,
		MaxWait:    time.Minute,
		Backoff:    time.Second,
		MaxBackoff: 10 * time.Second,
	}
}

func Canonical(version string) string {
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	return semver.Canonical(version)
}

func (g *GitHub) Releases(ctx context.Context, repo, tagPrefix, from, to string) ([]Release, *Truncation, error) {
	if strings.Count(repo, "/") != 1 {
		return nil, nil, fmt.Errorf("repo must be owner/name, got %q", repo)
	}
	lo, hi := Canonical(from), Canonical(to)
	if lo == "" || hi == "" {
		return nil, nil, fmt.Errorf("versions must be semver, got %q and %q", from, to)
	}

	maxPages, clamped := g.MaxPages, false
	if limit := unauthenticatedResults / max(g.PerPage, 1); g.Token == "" && limit < maxPages {
		maxPages, clamped = limit, true
	}

	var (
		out      []Release
		trunc    *Truncation
		seenFrom bool
		oldest   string
	)
	for page := 1; ; page++ {
		if page > maxPages {
			trunc = &Truncation{Pages: maxPages, Oldest: oldest, ResultCap: clamped}
			break
		}
		batch, err := g.page(ctx, repo, page)
		if errors.Is(err, errResultCap) {
			trunc = &Truncation{Pages: page - 1, Oldest: oldest, ResultCap: true}
			break
		}
		if err != nil {
			if page > 1 {
				err = fmt.Errorf("%w (after page %d%s)", err, page-1, atTag(oldest))
			}
			return nil, nil, err
		}
		matching, older := 0, 0
		for _, r := range batch {
			if r.Draft || r.Prerelease || !strings.HasPrefix(r.TagName, tagPrefix) {
				continue
			}
			v := Canonical(strings.TrimPrefix(r.TagName, tagPrefix))
			if v == "" {
				continue
			}
			matching++
			oldest = r.TagName
			if c := semver.Compare(v, lo); c <= 0 {
				older++
				seenFrom = seenFrom || c == 0
				continue
			}
			if semver.Compare(v, hi) > 0 {
				continue
			}
			out = append(out, Release{Tag: r.TagName, Version: v, Body: r.Body, URL: r.HTMLURL})
		}
		if len(batch) < g.PerPage || (seenFrom && matching > 0 && older == matching) {
			break
		}
	}
	if seenFrom {
		trunc = nil
	}

	slices.SortFunc(out, func(a, b Release) int { return semver.Compare(a.Version, b.Version) })
	return out, trunc, nil
}

func atTag(tag string) string {
	if tag == "" {
		return ""
	}
	return ", at " + tag
}

func (g *GitHub) page(ctx context.Context, repo string, page int) ([]ghRelease, error) {
	for attempt := 0; ; attempt++ {
		out, wait, err := g.fetch(ctx, repo, page, attempt)
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

func (g *GitHub) fetch(ctx context.Context, repo string, page, attempt int) ([]ghRelease, time.Duration, error) {
	url := fmt.Sprintf("%s/repos/%s/releases?per_page=%d&page=%d", g.BaseURL, repo, g.PerPage, page)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, -1, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	if g.UserAgent != "" {
		req.Header.Set("User-Agent", g.UserAgent)
	}
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}

	resp, err := g.HTTP.Do(req)
	if err != nil {
		return nil, g.backoff(attempt), err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseSize))
		resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		if resp.StatusCode == http.StatusUnprocessableEntity && strings.Contains(string(msg), "first 1000 results") {
			return nil, -1, errResultCap
		}
		err := fmt.Errorf("github releases %s page %d: %s: %s", repo, page, resp.Status, strings.TrimSpace(string(msg)))
		if g.Token == "" && (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) {
			err = fmt.Errorf("%w (unauthenticated: set GITHUB_TOKEN, for example GITHUB_TOKEN=$(gh auth token))", err)
		}
		return nil, g.retryWait(resp, attempt), err
	}

	var out []ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseSize)).Decode(&out); err != nil {
		return nil, -1, fmt.Errorf("decode github releases %s page %d: %w", repo, page, err)
	}
	return out, -1, nil
}

func (g *GitHub) backoff(attempt int) time.Duration {
	return httpretry.Backoff(g.Backoff, g.MaxBackoff, attempt)
}

func (g *GitHub) retryWait(resp *http.Response, attempt int) time.Duration {
	switch {
	case resp.StatusCode >= 500:
		return g.backoff(attempt)
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
