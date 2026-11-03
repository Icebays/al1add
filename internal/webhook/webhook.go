// Package webhook sends notification requests with variable expansion.
package webhook

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/icebays/al1add/internal/vars"
)

// Config describes one webhook.
type Config struct {
	Enable          bool     `json:"Enable"`
	URL             string   `json:"URL"`
	Method          string   `json:"Method"`
	Headers         []string `json:"Headers"`
	Body            string   `json:"Body"`
	SuccessContains string   `json:"SuccessContains"`
	Timeout         int      `json:"Timeout"`
}

// Send performs the request. extra variables override global ones.
func (c Config) Send(ctx context.Context, extra map[string]string) (string, error) {
	if !c.Enable || c.URL == "" {
		return "", nil
	}
	return Do(ctx, c.Method, vars.Expand(c.URL, extra), c.Headers, vars.Expand(c.Body, extra), c.SuccessContains, c.Timeout, extra)
}

// Do executes an HTTP request and returns the body.
func Do(ctx context.Context, method, url string, headers []string, body, success string, timeout int, extra map[string]string) (string, error) {
	if method == "" {
		method = http.MethodGet
		if body != "" {
			method = http.MethodPost
		}
	}
	if timeout <= 0 {
		timeout = 20
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), url, rd)
	if err != nil {
		return "", err
	}
	for _, h := range headers {
		k, v, ok := strings.Cut(h, ":")
		if ok {
			req.Header.Set(strings.TrimSpace(k), strings.TrimSpace(vars.Expand(v, extra)))
		}
	}
	if body != "" && req.Header.Get("Content-Type") == "" {
		ct := "text/plain; charset=utf-8"
		if t := strings.TrimSpace(body); strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
			ct = "application/json"
		}
		req.Header.Set("Content-Type", ct)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	out := string(data)
	if success != "" {
		if !strings.Contains(out, success) {
			return out, fmt.Errorf("response does not contain %q", success)
		}
	} else if resp.StatusCode >= 400 {
		return out, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return out, nil
}
