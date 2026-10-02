package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/lov3g00d/helm-bumpcheck/internal/httpretry"
)

const (
	DefaultBaseURL = "https://api.typesafe.ai"
	DefaultModel   = "jev-1.13.0"
	APIKeyEnv      = "TYPESAFE_API_KEY"
	BaseURLEnv     = "TYPESAFE_BASE_URL"
	ModelEnv       = "TYPESAFE_MODEL"

	maxResponseSize = 10 << 20
	maxRetryAfter   = 30 * time.Second
	maxErrorBody    = 1 << 10
)

type Question struct {
	Type         string `json:"type"`
	Instructions any    `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

func Noul(instructions any) Question {
	return Question{Type: "noul", Instructions: instructions}
}

func NoulWithCriteria(instructions any, yes, no string) Question {
	return Question{Type: "noul", Instructions: instructions, Criteria: map[string]string{"true": yes, "false": no}}
}

func Choice(instructions any, options map[string]any) Question {
	return Question{Type: "choice", Instructions: instructions, Criteria: options}
}

func Score(instructions any, levels []string) Question {
	return Question{Type: "score", Instructions: instructions, Criteria: levels}
}

type Request struct {
	Model     string              `json:"model"`
	State     any                 `json:"state"`
	Questions map[string]Question `json:"questions"`
}

type Answer struct {
	Type          string             `json:"type"`
	Noul          float64            `json:"noul"`
	Choice        string             `json:"choice"`
	Score         float64            `json:"score"`
	Probabilities map[string]float64 `json:"probabilities"`
	Legend        map[string]string  `json:"legend"`
	Confidence    float64            `json:"confidence"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type Response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   Usage             `json:"usage"`
}

type APIError struct {
	StatusCode int
	Body       string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("typesafe api: %d %s: %s", e.StatusCode, http.StatusText(e.StatusCode), e.Body)
}

func (e *APIError) Retryable() bool {
	return e.StatusCode == http.StatusRequestTimeout || e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

type Client struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTP       *http.Client
	MaxRetries int
	Backoff    time.Duration
	MaxBackoff time.Duration
	Timeout    time.Duration
	UserAgent  string
}

func NewFromEnv() (*Client, error) {
	key := os.Getenv(APIKeyEnv)
	if key == "" {
		return nil, fmt.Errorf("%s is not set", APIKeyEnv)
	}
	baseURL := os.Getenv(BaseURLEnv)
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if u, err := url.Parse(baseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("%s must be an http or https URL, got %q", BaseURLEnv, baseURL)
	}
	model := os.Getenv(ModelEnv)
	if model == "" {
		model = DefaultModel
	}
	return &Client{
		BaseURL:    baseURL,
		APIKey:     key,
		Model:      model,
		HTTP:       &http.Client{},
		MaxRetries: 2,
		Backoff:    500 * time.Millisecond,
		MaxBackoff: 5 * time.Second,
		Timeout:    30 * time.Second,
		UserAgent:  "helm-bumpcheck",
	}, nil
}

func (c *Client) Evaluate(ctx context.Context, state any, questions map[string]Question) (*Response, error) {
	body, err := json.Marshal(Request{Model: c.Model, State: state, Questions: questions})
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}

	for attempt := 0; ; attempt++ {
		resp, err := c.post(ctx, body)
		if err == nil || ctx.Err() != nil || attempt >= c.MaxRetries || !retryable(err) {
			return resp, err
		}
		wait := httpretry.Backoff(c.Backoff, c.MaxBackoff, attempt)
		if apiErr, ok := errors.AsType[*APIError](err); ok && apiErr.RetryAfter > 0 {
			wait = min(apiErr.RetryAfter, maxRetryAfter)
		}
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < wait {
			return nil, err
		}
		if err := httpretry.Sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
}

func retryable(err error) bool {
	if apiErr, ok := errors.AsType[*APIError](err); ok {
		return apiErr.Retryable()
	}
	_, ok := errors.AsType[*url.Error](err)
	return ok
}

func (c *Client) post(ctx context.Context, body []byte) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseSize))
		resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		apiErr := &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(msg))}
		apiErr.RetryAfter, _ = httpretry.After(resp.Header, time.Now())
		return nil, apiErr
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var out Response
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &out, nil
}
