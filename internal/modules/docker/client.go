package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// client talks to the Docker Engine API over a unix socket or TCP host.
type client struct {
	http *http.Client
	host string // for URL scheme
}

func newClient(host string) *client {
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	tr := &http.Transport{}
	if strings.HasPrefix(host, "unix://") {
		sock := strings.TrimPrefix(host, "unix://")
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", sock)
		}
	} else {
		h := strings.TrimPrefix(strings.TrimPrefix(host, "tcp://"), "http://")
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", h)
		}
	}
	return &client{http: &http.Client{Transport: tr, Timeout: 30 * time.Second}, host: host}
}

func (c *client) do(ctx context.Context, method, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("docker daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 400 {
		var e struct{ Message string }
		_ = json.Unmarshal(data, &e)
		if e.Message != "" {
			return fmt.Errorf("docker: %s", e.Message)
		}
		return fmt.Errorf("docker: HTTP %d", resp.StatusCode)
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

type container struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Created int64             `json:"Created"`
	Ports   []portMap         `json:"Ports"`
	Labels  map[string]string `json:"Labels"`
}

type portMap struct {
	IP          string `json:"IP"`
	PrivatePort int    `json:"PrivatePort"`
	PublicPort  int    `json:"PublicPort"`
	Type        string `json:"Type"`
}

type image struct {
	ID       string   `json:"Id"`
	RepoTags []string `json:"RepoTags"`
	Size     int64    `json:"Size"`
	Created  int64    `json:"Created"`
}

func (c *client) ping(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/_ping", nil)
}

func (c *client) containers(ctx context.Context) ([]container, error) {
	var cs []container
	err := c.do(ctx, http.MethodGet, "/containers/json?all=1", &cs)
	return cs, err
}

func (c *client) images(ctx context.Context) ([]image, error) {
	var is []image
	err := c.do(ctx, http.MethodGet, "/images/json", &is)
	return is, err
}

func (c *client) containerAction(ctx context.Context, id, action string) error {
	id = url.PathEscape(id)
	switch action {
	case "start", "stop", "restart":
		return c.do(ctx, http.MethodPost, "/containers/"+id+"/"+action, nil)
	case "remove":
		return c.do(ctx, http.MethodDelete, "/containers/"+id+"?force=1", nil)
	}
	return fmt.Errorf("unknown container action %q", action)
}

func (c *client) logs(ctx context.Context, id string, tail int) (string, error) {
	id = url.PathEscape(id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("http://docker/containers/%s/logs?stdout=1&stderr=1&tail=%d", id, tail), nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return demux(data), nil
}

// demux strips Docker's 8-byte stream multiplexing headers when present.
func demux(data []byte) string {
	var b strings.Builder
	i := 0
	for i+8 <= len(data) {
		if data[i] > 2 { // not a stream header; treat rest as raw
			b.Write(data[i:])
			break
		}
		n := int(data[i+4])<<24 | int(data[i+5])<<16 | int(data[i+6])<<8 | int(data[i+7])
		i += 8
		if n < 0 || i+n > len(data) {
			b.Write(data[i:])
			break
		}
		b.Write(data[i : i+n])
		i += n
	}
	if b.Len() == 0 {
		return string(data)
	}
	return b.String()
}
