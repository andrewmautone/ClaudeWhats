package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/andrewmautone/claudewhats/internal/config"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func NewClient(port int) *Client {
	return &Client{BaseURL: fmt.Sprintf("http://127.0.0.1:%d", port), HTTP: &http.Client{Timeout: 60 * time.Second}}
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		var e struct{ Error string }
		json.Unmarshal(rb, &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(rb))
		}
		return fmt.Errorf("daemon %d: %s", resp.StatusCode, e.Error)
	}
	if out != nil {
		return json.Unmarshal(rb, out)
	}
	return nil
}

func (c *Client) Status(ctx context.Context) (StatusResponse, error) {
	var st StatusResponse
	err := c.do(ctx, "GET", "/status", nil, &st)
	return st, err
}

func (c *Client) Send(ctx context.Context, chatJID, text string) (string, error) {
	var out struct{ ID string }
	err := c.do(ctx, "POST", "/send", map[string]string{"chat": chatJID, "text": text}, &out)
	return out.ID, err
}

func (c *Client) Sync(ctx context.Context, chatJID string, count int) error {
	return c.do(ctx, "POST", "/sync", map[string]any{"chat": chatJID, "count": count}, nil)
}

func (c *Client) Shutdown(ctx context.Context) error {
	return c.do(ctx, "POST", "/shutdown", nil, nil)
}

var spawnFn = Spawn

// Spawn starts `claudewhats serve --background` detached from this process.
func Spawn(cfg *config.Config) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, "serve", "--background")
	cmd.Env = append(os.Environ(), "CLAUDEWHATS_HOME="+cfg.Home)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func logTail(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// EnsureRunning returns a client to a live daemon, spawning one when needed.
func EnsureRunning(ctx context.Context, cfg *config.Config, wait time.Duration) (*Client, error) {
	c := NewClient(cfg.Port)
	probe := func() bool {
		pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_, err := c.Status(pctx)
		return err == nil
	}
	if probe() {
		return c, nil
	}
	if err := spawnFn(cfg); err != nil {
		return nil, fmt.Errorf("não consegui iniciar o daemon: %w", err)
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if probe() {
			return c, nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	msg := "daemon não subiu a tempo"
	if tail := logTail(cfg.LogPath, 5); tail != "" {
		msg += "; fim do daemon.log:\n" + tail
	}
	return nil, errors.New(msg)
}

// Kick nudges the daemon to life without waiting; errors are ignored.
func Kick(cfg *config.Config) {
	c := NewClient(cfg.Port)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := c.Status(ctx); err == nil {
		return
	}
	_ = spawnFn(cfg)
}
