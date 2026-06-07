package functionskv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const VersionHeader = "X-KV-Version"

var ErrNotFound = errors.New("functions-kv value not found")

type Versioned[T any] struct {
	Value   T
	Version string
}

type Client[T any] struct {
	baseURL string
	cookie  string
	key     string
	version string
}

func New[T any](baseURL, auth, key string) *Client[T] {
	return &Client[T]{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		cookie:  NormalizeAuthCookie(auth),
		key:     strings.Trim(strings.TrimSpace(key), "/"),
	}
}

func NormalizeAuthCookie(auth string) string {
	auth = strings.TrimSpace(auth)
	if auth == "" || strings.Contains(auth, "=") {
		return auth
	}
	return "__Host-Auth=" + auth
}

func (c *Client[T]) Init(ctx context.Context, local T) (T, error) {
	value, err := c.Get(ctx)
	if err == nil {
		c.version = value.Version
		return value.Value, nil
	}
	if !errors.Is(err, ErrNotFound) {
		var zero T
		return zero, err
	}
	if err := c.Save(ctx, local); err != nil {
		var zero T
		return zero, err
	}
	return local, nil
}

func (c *Client[T]) Get(ctx context.Context) (*Versioned[T], error) {
	res, err := c.request(ctx, http.MethodGet, "", nil)
	if err != nil {
		return nil, err
	}
	if res.statusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if res.statusCode >= 400 {
		return nil, fmt.Errorf("functions-kv GET failed: %s", res.bodyString())
	}
	return c.parse(res.body, res.headers.Get(VersionHeader))
}

func (c *Client[T]) Save(ctx context.Context, value T) error {
	saved, err := c.post(ctx, http.MethodPost, value)
	if err != nil {
		return err
	}
	c.version = saved.Version
	return nil
}

func (c *Client[T]) Delete(ctx context.Context) error {
	res, err := c.request(ctx, http.MethodDelete, "", nil)
	if err != nil {
		return err
	}
	if res.statusCode >= 400 {
		return fmt.Errorf("functions-kv DELETE failed: %s", res.bodyString())
	}
	c.version = ""
	return nil
}

func (c *Client[T]) BeforeRefresh(ctx context.Context) (*T, error) {
	for {
		value, locked, err := c.lock(ctx)
		if err != nil {
			return nil, err
		}
		if value != nil {
			c.version = value.Version
			current := value.Value
			return &current, nil
		}
		if locked {
			return nil, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (c *Client[T]) AfterRefresh(ctx context.Context, value T) error {
	saved, err := c.post(ctx, "UNLOCK", value)
	if err != nil {
		return err
	}
	c.version = saved.Version
	return nil
}

func (c *Client[T]) lock(ctx context.Context) (*Versioned[T], bool, error) {
	if c.version == "" {
		value, err := c.Get(ctx)
		if err != nil {
			return nil, false, err
		}
		c.version = value.Version
	}
	res, err := c.request(ctx, "LOCK", "?t="+url.QueryEscape(c.version), nil)
	if err != nil {
		return nil, false, err
	}
	switch res.statusCode {
	case http.StatusOK:
		value, err := c.parse(res.body, res.headers.Get(VersionHeader))
		return value, false, err
	case http.StatusCreated:
		return nil, true, nil
	case http.StatusLocked:
		return nil, false, nil
	case http.StatusNotFound:
		return nil, false, ErrNotFound
	default:
		return nil, false, fmt.Errorf("functions-kv LOCK failed: %s", res.bodyString())
	}
}

func (c *Client[T]) post(ctx context.Context, method string, value T) (*Versioned[T], error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	res, err := c.request(ctx, method, "", body)
	if err != nil {
		return nil, err
	}
	if res.statusCode >= 400 {
		return nil, fmt.Errorf("functions-kv %s failed: %s", method, res.bodyString())
	}
	version := res.headers.Get(VersionHeader)
	if version == "" {
		return nil, fmt.Errorf("functions-kv %s missing %s", method, VersionHeader)
	}
	return &Versioned[T]{Value: value, Version: version}, nil
}

func (c *Client[T]) request(ctx context.Context, method, suffix string, body []byte) (*response, error) {
	endpoint := c.baseURL + "/" + url.PathEscape(c.key) + suffix
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &response{statusCode: resp.StatusCode, headers: resp.Header, body: data}, nil
}

func (c *Client[T]) parse(body []byte, version string) (*Versioned[T], error) {
	if version == "" {
		return nil, fmt.Errorf("functions-kv response missing %s", VersionHeader)
	}
	var value T
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, err
	}
	return &Versioned[T]{Value: value, Version: version}, nil
}

type response struct {
	statusCode int
	headers    http.Header
	body       []byte
}

func (r *response) bodyString() string {
	return strings.TrimSpace(string(r.body))
}
