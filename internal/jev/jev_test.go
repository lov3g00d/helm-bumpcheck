package jev

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const okBody = `{
  "model": "jev-1.13.0",
  "answers": {
    "is_urgent": {"type": "noul", "noul": 0.95},
    "department": {"type": "choice", "choice": "billing", "probabilities": {"billing": 0.88, "technical": 0.12}, "confidence": 0.81}
  },
  "usage": {"input_tokens": 296, "output_tokens": 20}
}`

func testClient(url string) *Client {
	return &Client{
		BaseURL:    url,
		APIKey:     "test-key",
		Model:      DefaultModel,
		HTTP:       &http.Client{Timeout: 5 * time.Second},
		MaxRetries: 3,
		Backoff:    time.Millisecond,
	}
}

func statusSequence(t *testing.T, statuses ...int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1)) - 1
		status := statuses[len(statuses)-1]
		if n < len(statuses) {
			status = statuses[n]
		}
		w.WriteHeader(status)
		if status == http.StatusOK {
			io.WriteString(w, okBody)
			return
		}
		io.WriteString(w, `{"detail":"nope"}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestEvaluateSendsRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" {
			t.Errorf("got %s %s, want POST /v1/systemone", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != DefaultModel || req.State != "payouts failing for 3 days" {
			t.Errorf("request = %+v", req)
		}
		if q := req.Questions["is_urgent"]; q.Type != "noul" {
			t.Errorf("question = %+v", q)
		}
		io.WriteString(w, okBody)
	}))
	defer srv.Close()

	resp, err := testClient(srv.URL).Evaluate(context.Background(), "payouts failing for 3 days", map[string]Question{
		"is_urgent": Noul("Does this convey urgency?"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Answers["is_urgent"].Noul; got != 0.95 {
		t.Errorf("noul = %v, want 0.95", got)
	}
	if got := resp.Answers["department"]; got.Choice != "billing" || got.Probabilities["technical"] != 0.12 {
		t.Errorf("choice answer = %+v", got)
	}
	if resp.Usage.InputTokens != 296 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestRetriesRateLimitAndOverload(t *testing.T) {
	srv, calls := statusSequence(t, http.StatusTooManyRequests, 529, http.StatusOK)
	resp, err := testClient(srv.URL).Evaluate(context.Background(), "x", map[string]Question{"q": Noul("q?")})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Answers["is_urgent"].Noul != 0.95 {
		t.Errorf("unexpected response %+v", resp)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
}

func TestGivesUpAfterMaxRetries(t *testing.T) {
	srv, calls := statusSequence(t, 529)
	_, err := testClient(srv.URL).Evaluate(context.Background(), "x", map[string]Question{"q": Noul("q?")})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 529 {
		t.Fatalf("err = %v, want APIError 529", err)
	}
	if got := calls.Load(); got != 4 {
		t.Errorf("calls = %d, want 4 (1 try + 3 retries)", got)
	}
}

func TestDoesNotRetryValidationError(t *testing.T) {
	srv, calls := statusSequence(t, http.StatusUnprocessableEntity)
	_, err := testClient(srv.URL).Evaluate(context.Background(), "x", map[string]Question{"q": Noul("q?")})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("err = %v, want APIError 422", err)
	}
	if apiErr.Body != `{"detail":"nope"}` {
		t.Errorf("body = %q", apiErr.Body)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1", got)
	}
}

func TestContextStopsBackoff(t *testing.T) {
	srv, _ := statusSequence(t, http.StatusTooManyRequests)
	c := testClient(srv.URL)
	c.Backoff = 10 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	start := time.Now()
	_, err := c.Evaluate(ctx, "x", map[string]Question{"q": Noul("q?")})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("took %s, backoff ignored the context", elapsed)
	}
}

func TestSkipsBackoffPastDeadline(t *testing.T) {
	srv, calls := statusSequence(t, http.StatusTooManyRequests)
	c := testClient(srv.URL)
	c.Backoff = 10 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	_, err := c.Evaluate(ctx, "x", map[string]Question{"q": Noul("q?")})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("err = %v, want the 429 rather than a deadline error", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond || calls.Load() != 1 {
		t.Errorf("took %s and %d calls, want one call and no wait", elapsed, calls.Load())
	}
}

func TestQuestionJSON(t *testing.T) {
	tests := []struct {
		name string
		q    Question
		want string
	}{
		{"noul", Noul("Is it urgent?"), `{"type":"noul","instructions":"Is it urgent?"}`},
		{"noul with criteria", NoulWithCriteria("Is it urgent?", "time-sensitive", "no urgency"),
			`{"type":"noul","instructions":"Is it urgent?","criteria":{"false":"no urgency","true":"time-sensitive"}}`},
		{"choice", Choice("Which team?", map[string]any{"billing": "payments", "sales": nil}),
			`{"type":"choice","instructions":"Which team?","criteria":{"billing":"payments","sales":null}}`},
		{"score", Score("How angry?", []string{"calm", "angry"}),
			`{"type":"score","instructions":"How angry?","criteria":["calm","angry"]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.q)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
		})
	}
}

func TestNewFromEnvRequiresKey(t *testing.T) {
	t.Setenv(APIKeyEnv, "")
	if _, err := NewFromEnv(); err == nil {
		t.Error("expected error without API key")
	}
}

func TestLiveEvaluate(t *testing.T) {
	if os.Getenv("LIVE") != "1" || os.Getenv("TYPESAFE_API_KEY") == "" {
		t.Skip("set LIVE=1 and TYPESAFE_API_KEY to call the real API")
	}
	c, err := NewFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Evaluate(context.Background(), "Removed metrics-generator localblocks processor and related local block storage plumbing", map[string]Question{
		"concerns_key": Noul("Does this release-note entry concern the config key `processor.local_blocks.flush_to_storage`?"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("model=%s noul=%.3f usage=%+v", resp.Model, resp.Answers["concerns_key"].Noul, resp.Usage)
}

func TestRetriesServerErrorsAndTimeouts(t *testing.T) {
	srv, calls := statusSequence(t, http.StatusBadGateway, http.StatusRequestTimeout, http.StatusOK)
	if _, err := testClient(srv.URL).Evaluate(context.Background(), "x", map[string]Question{"q": Noul("q?")}); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
}

func TestRetriesDroppedConnection(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		io.WriteString(w, okBody)
	}))
	defer srv.Close()

	if _, err := testClient(srv.URL).Evaluate(context.Background(), "x", map[string]Question{"q": Noul("q?")}); err != nil {
		t.Fatalf("err = %v, want success after a dropped connection", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("calls = %d, want 2", got)
	}
}

func TestHonoursRetryAfter(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After-Ms", "150")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		io.WriteString(w, okBody)
	}))
	defer srv.Close()

	start := time.Now()
	if _, err := testClient(srv.URL).Evaluate(context.Background(), "x", map[string]Question{"q": Noul("q?")}); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Errorf("retried after %s, want at least the 150ms Retry-After-Ms", elapsed)
	}
}

func TestNewFromEnvPinsModel(t *testing.T) {
	t.Setenv(APIKeyEnv, "k")
	t.Setenv(ModelEnv, "")
	c, err := NewFromEnv()
	if err != nil || c.Model != "jev-1.13.0" || c.MaxRetries != 2 {
		t.Errorf("client = %+v, err %v", c, err)
	}
	t.Setenv(ModelEnv, "jev-preview")
	if c, _ := NewFromEnv(); c.Model != "jev-preview" {
		t.Errorf("model override = %q", c.Model)
	}
}

func TestDoesNotRetryMalformedResponse(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, "{not json")
	}))
	defer srv.Close()

	_, err := testClient(srv.URL).Evaluate(context.Background(), "x", map[string]Question{"q": Noul("q?")})
	if err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Errorf("err = %v, want a decode error", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1", got)
	}
}

func TestCapsErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, strings.Repeat("x", 64<<10))
	}))
	defer srv.Close()

	_, err := testClient(srv.URL).Evaluate(context.Background(), "x", map[string]Question{"q": Noul("q?")})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || len(apiErr.Body) != maxErrorBody {
		t.Errorf("err = %T, body of %d bytes, want an APIError with %d", err, len(apiErr.Body), maxErrorBody)
	}
}

func TestTimeoutCoversAllAttempts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(40 * time.Millisecond)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := testClient(srv.URL)
	c.MaxRetries, c.Timeout = 50, 100*time.Millisecond
	start := time.Now()
	if _, err := c.Evaluate(context.Background(), "x", map[string]Question{"q": Noul("q?")}); err == nil {
		t.Fatal("expected an error")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("took %s, want the call to stop near its 100ms budget", elapsed)
	}
}

func TestSendsUserAgent(t *testing.T) {
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		io.WriteString(w, okBody)
	}))
	defer srv.Close()

	c := testClient(srv.URL)
	c.UserAgent = "helm-bumpcheck/1.2.3"
	if _, err := c.Evaluate(context.Background(), "x", map[string]Question{"q": Noul("q?")}); err != nil {
		t.Fatal(err)
	}
	if ua != "helm-bumpcheck/1.2.3" {
		t.Errorf("User-Agent = %q", ua)
	}
}

func TestReusesConnectionAfterError(t *testing.T) {
	var calls, conns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			io.WriteString(w, strings.Repeat("x", 64<<10))
			return
		}
		io.WriteString(w, okBody)
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()

	c := testClient(srv.URL)
	c.HTTP = srv.Client()
	if _, err := c.Evaluate(context.Background(), "x", map[string]Question{"q": Noul("q?")}); err != nil {
		t.Fatal(err)
	}
	if got := conns.Load(); got != 1 {
		t.Errorf("connections = %d, want 1", got)
	}
}

func TestNewFromEnvRejectsBadBaseURL(t *testing.T) {
	t.Setenv(APIKeyEnv, "k")
	for _, u := range []string{"ftp://api.typesafe.ai", "api.typesafe.ai", "https://"} {
		t.Setenv(BaseURLEnv, u)
		if _, err := NewFromEnv(); err == nil || !strings.Contains(err.Error(), BaseURLEnv) {
			t.Errorf("%s: err = %v", u, err)
		}
	}
	t.Setenv(BaseURLEnv, "http://localhost:8080")
	if _, err := NewFromEnv(); err != nil {
		t.Errorf("http://localhost:8080: %v", err)
	}
}
