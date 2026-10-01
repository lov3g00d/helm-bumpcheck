package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	DefaultBaseURL = "https://api.typesafe.ai"
	DefaultModel   = "jev-latest"
	APIKeyEnv      = "TYPESAFE_API_KEY"
	BaseURLEnv     = "TYPESAFE_BASE_URL"

	statusOverloaded = 529
	maxResponseSize  = 10 << 20
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
}

func (e *APIError) Error() string {
	return fmt.Sprintf("typesafe api: %d %s: %s", e.StatusCode, http.StatusText(e.StatusCode), e.Body)
}

func (e *APIError) Retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode == statusOverloaded
}

type Client struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTP       *http.Client
	MaxRetries int
	Backoff    time.Duration
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
	return &Client{
		BaseURL:    baseURL,
		APIKey:     key,
		Model:      DefaultModel,
		HTTP:       &http.Client{Timeout: 30 * time.Second},
		MaxRetries: 4,
		Backoff:    500 * time.Millisecond,
	}, nil
}

func (c *Client) Evaluate(ctx context.Context, state any, questions map[string]Question) (*Response, error) {
	body, err := json.Marshal(Request{Model: c.Model, State: state, Questions: questions})
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	delay := c.Backoff
	for attempt := 0; ; attempt++ {
		resp, err := c.post(ctx, body)
		var apiErr *APIError
		if err == nil || !errors.As(err, &apiErr) || !apiErr.Retryable() || attempt >= c.MaxRetries {
			return resp, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		delay *= 2
	}
}

func (c *Client) post(ctx context.Context, body []byte) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}

	var out Response
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &out, nil
}
