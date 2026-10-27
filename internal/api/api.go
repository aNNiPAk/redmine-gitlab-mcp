package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"redmine-gitlab-mcp/internal/refs"
)

type Config struct {
	RedmineURL     string            `json:"redmineUrl"`
	RedmineAPIKey  string            `json:"redmineApiKey"`
	GitlabURL      string            `json:"gitlabUrl"`
	GitlabToken    string            `json:"gitlabToken"`
	GitlabProject  string            `json:"gitlabProject"`
	GitlabProjects map[string]string `json:"gitlabProjects,omitempty"`
}

func LoadConfig(filename string) (Config, error) {
	b, e := os.ReadFile(filename)
	if e != nil {
		return Config{}, e
	}
	b = bytes.TrimPrefix(b, []byte{0xef, 0xbb, 0xbf})
	var c Config
	if e = json.Unmarshal(b, &c); e != nil {
		return c, e
	}
	if strings.TrimSpace(c.RedmineURL) == "" || c.RedmineAPIKey == "" || strings.TrimSpace(c.GitlabURL) == "" || c.GitlabToken == "" || strings.TrimSpace(c.GitlabProject) == "" {
		return c, fmt.Errorf("заполните redmineUrl, redmineApiKey, gitlabUrl, gitlabToken и gitlabProject")
	}
	for _, raw := range []string{c.RedmineURL, c.GitlabURL} {
		u, e := url.Parse(raw)
		if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return c, fmt.Errorf("адрес API должен быть http(s) URL без логина и параметров")
		}
	}
	seenAliases := map[string]bool{}
	for k, v := range c.GitlabProjects {
		if k == "" || v == "" || strings.ContainsAny(k, "!/\\ ") {
			return c, fmt.Errorf("некорректный псевдоним проекта: %s", k)
		}
		if seenAliases[strings.ToLower(k)] {
			return c, fmt.Errorf("повторяющийся псевдоним проекта: %s", k)
		}
		seenAliases[strings.ToLower(k)] = true
	}
	return c, nil
}
func DefaultConfigPath() string {
	exe, e := os.Executable()
	if e != nil {
		return "config.local.json"
	}
	return filepath.Join(filepath.Dir(exe), "config.local.json")
}

type Failure struct {
	Section   string `json:"section"`
	Message   string `json:"message"`
	Status    *int   `json:"status"`
	Retryable bool   `json:"retryable"`
}
type HTTPError struct {
	Status    int
	URLPath   string
	Retryable bool
}

func (e *HTTPError) Error() string {
	if e.Status == 0 {
		return "Сетевая ошибка GET " + e.URLPath
	}
	return fmt.Sprintf("HTTP %d при GET %s", e.Status, e.URLPath)
}
func Failed(section string, e error) Failure {
	f := Failure{Section: section, Message: e.Error()}
	var h *HTTPError
	if errors.As(e, &h) {
		if h.Status > 0 {
			s := h.Status
			f.Status = &s
		}
		f.Retryable = h.Retryable
	}
	return f
}

type cacheItem struct {
	Data    []byte
	Header  http.Header
	Expires time.Time
	Size    int
	Tick    uint64
}
type Cache struct {
	mu      sync.Mutex
	entries map[string]cacheItem
	size    int
	tick    uint64
}

func NewCache() *Cache { return &Cache{entries: map[string]cacheItem{}} }
func (c *Cache) Get(key string) ([]byte, http.Header, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	it, ok := c.entries[key]
	if !ok {
		return nil, nil, false
	}
	if time.Now().After(it.Expires) {
		delete(c.entries, key)
		c.size -= it.Size
		return nil, nil, false
	}
	c.tick++
	it.Tick = c.tick
	c.entries[key] = it
	return append([]byte(nil), it.Data...), it.Header.Clone(), true
}
func (c *Cache) Put(key string, data []byte, h http.Header) {
	if len(data) > 12*1024*1024 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.entries[key]; ok {
		c.size -= old.Size
	}
	c.tick++
	c.entries[key] = cacheItem{Data: append([]byte(nil), data...), Header: h.Clone(), Size: len(data), Expires: time.Now().Add(2 * time.Minute), Tick: c.tick}
	c.size += len(data)
	for c.size > 24*1024*1024 || len(c.entries) > 120 {
		var oldest string
		var tick uint64 = ^uint64(0)
		for k, v := range c.entries {
			if v.Tick < tick {
				oldest = k
				tick = v.Tick
			}
		}
		c.size -= c.entries[oldest].Size
		delete(c.entries, oldest)
	}
}

type Client struct {
	Base   string
	Token  string
	Header string
	HTTP   *http.Client
	Cache  *Cache
}

func NewClient(base, token, header string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), Token: token, Header: header, Cache: NewCache(), HTTP: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) URL(rel string) *url.URL {
	base, _ := url.Parse(c.Base + "/")
	u, _ := url.Parse(strings.TrimLeft(rel, "/"))
	return base.ResolveReference(u)
}
func retryable(status int) bool {
	switch status {
	case 408, 429, 500, 502, 503, 504:
		return true
	}
	return false
}
func retryWait(h string, attempt int) time.Duration {
	if h != "" {
		if seconds, e := strconv.ParseFloat(h, 64); e == nil && seconds >= 0 {
			return time.Duration(seconds * float64(time.Second))
		}
		if d, e := http.ParseTime(h); e == nil {
			w := time.Until(d)
			if w < 0 {
				return 0
			}
			return w
		}
	}
	return time.Duration(250*(1<<attempt)+rand.Intn(101)) * time.Millisecond
}
func (c *Client) Get(ctx context.Context, u *url.URL, limit int) ([]byte, http.Header, error) {
	safe := u.Scheme + "://" + u.Host + u.EscapedPath()
	for attempt := 0; attempt < 3; attempt++ {
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if e != nil {
			return nil, nil, e
		}
		req.Header.Set(c.Header, c.Token)
		req.Header.Set("Accept", "*/*")
		resp, e := c.HTTP.Do(req)
		if e != nil {
			var netErr net.Error
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			transient := errors.As(e, &netErr) || errors.Is(e, io.EOF) || strings.Contains(strings.ToLower(e.Error()), "connection reset")
			if transient && attempt < 2 {
				select {
				case <-ctx.Done():
					return nil, nil, ctx.Err()
				case <-time.After(retryWait("", attempt)):
				}
				continue
			}
			return nil, nil, &HTTPError{URLPath: safe, Retryable: transient}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
			resp.Body.Close()
			r := retryable(resp.StatusCode)
			w := retryWait(resp.Header.Get("Retry-After"), attempt)
			if r && attempt < 2 && w <= 15*time.Second {
				select {
				case <-ctx.Done():
					return nil, nil, ctx.Err()
				case <-time.After(w):
				}
				continue
			}
			return nil, nil, &HTTPError{Status: resp.StatusCode, URLPath: safe, Retryable: r}
		}
		if resp.ContentLength > int64(limit) {
			resp.Body.Close()
			return nil, nil, fmt.Errorf("ответ превышает лимит %d байт (%s)", limit, safe)
		}
		body, e := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
		resp.Body.Close()
		if e != nil {
			return nil, nil, e
		}
		if len(body) > limit {
			return nil, nil, fmt.Errorf("ответ превышает лимит %d байт (%s)", limit, safe)
		}
		return body, resp.Header, nil
	}
	return nil, nil, fmt.Errorf("превышено число попыток HTTP")
}
func (c *Client) JSON(ctx context.Context, rel string, fresh bool) (any, http.Header, error) {
	u := c.URL(rel)
	key := u.String()
	if !fresh {
		if data, h, ok := c.Cache.Get(key); ok {
			var obj any
			if json.Unmarshal(data, &obj) == nil {
				return obj, h, nil
			}
		}
	}
	data, h, e := c.Get(ctx, u, 16*1024*1024)
	if e != nil {
		return nil, nil, e
	}
	var obj any
	if e := json.Unmarshal(data, &obj); e != nil {
		return nil, nil, fmt.Errorf("неверный JSON (%s)", u.EscapedPath())
	}
	c.Cache.Put(key, data, h)
	return obj, h, nil
}
func (c *Client) Bytes(ctx context.Context, rel string, limit int) ([]byte, error) {
	b, _, e := c.Get(ctx, c.URL(rel), limit)
	return b, e
}
func (c *Client) ExternalBytes(ctx context.Context, u *url.URL, limit int) ([]byte, error) {
	b, _, e := c.Get(ctx, u, limit)
	return b, e
}
func CheckRedmineAttachment(base, download string) (*url.URL, error) {
	root, e := url.Parse(strings.TrimRight(base, "/") + "/")
	if e != nil {
		return nil, e
	}
	u, e := url.Parse(download)
	if e != nil {
		return nil, e
	}
	u = root.ResolveReference(u)
	if u.Scheme != root.Scheme || u.Host != root.Host || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.EscapedPath(), root.EscapedPath()+"attachments/download/") {
		return nil, fmt.Errorf("недопустимый URL вложения Redmine")
	}
	return u, nil
}

type Page struct {
	Items []map[string]any
	Next  int
}
type Service struct {
	Cfg             Config
	Redmine, Gitlab *Client
	mu              sync.Mutex
	projectIDs      map[string]string
}

func NewService(c Config) *Service {
	return &Service{Cfg: c, Redmine: NewClient(c.RedmineURL, c.RedmineAPIKey, "X-Redmine-API-Key"), Gitlab: NewClient(c.GitlabURL, c.GitlabToken, "PRIVATE-TOKEN"), projectIDs: map[string]string{}}
}
func Map(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}
func Array(v any) []any { a, _ := v.([]any); return a }
func Objects(v any) []map[string]any {
	a := Array(v)
	out := make([]map[string]any, 0, len(a))
	for _, it := range a {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}
func Str(v any) string { s, _ := v.(string); return s }
func Bool(v any) bool  { b, _ := v.(bool); return b }
func Field(m map[string]any, k string) any {
	if m == nil {
		return nil
	}
	return m[k]
}
func Pick(m map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		out[k] = m[k]
	}
	return out
}
func (s *Service) GLPage(ctx context.Context, rel string, page, perPage int, fresh bool) (Page, error) {
	sep := "?"
	if strings.Contains(rel, "?") {
		sep = "&"
	}
	v, h, e := s.Gitlab.JSON(ctx, fmt.Sprintf("%s%spage=%d&per_page=%d", rel, sep, page, perPage), fresh)
	if e != nil {
		return Page{}, e
	}
	a, ok := v.([]any)
	if !ok {
		return Page{}, fmt.Errorf("GitLab вернул не массив")
	}
	raw := h.Get("X-Next-Page")
	next := 0
	if raw != "" {
		next, e = strconv.Atoi(raw)
		if e != nil || next <= page {
			return Page{}, fmt.Errorf("неверный X-Next-Page")
		}
	} else if _, exists := h[http.CanonicalHeaderKey("X-Next-Page")]; !exists && len(a) == perPage {
		next = page + 1
	}
	return Page{Items: Objects(a), Next: next}, nil
}
func (s *Service) Project(ctx context.Context, alias string) (string, error) {
	configured := s.Cfg.GitlabProject
	if alias != "" {
		configured = alias
		keys := make([]string, 0, len(s.Cfg.GitlabProjects))
		for k := range s.Cfg.GitlabProjects {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if strings.EqualFold(k, alias) {
				configured = s.Cfg.GitlabProjects[k]
				break
			}
		}
	}
	configured = strings.TrimSpace(configured)
	if id, e := strconv.Atoi(configured); e == nil && id > 0 {
		return configured, nil
	}
	key := strings.ToLower(configured)
	s.mu.Lock()
	cached := s.projectIDs[key]
	s.mu.Unlock()
	if cached != "" {
		return cached, nil
	}
	target := configured
	full := strings.Contains(configured, "/")
	if full {
		target = path.Base(configured)
	}
	unique := map[int]string{}
	page := 1
	ended := false
	for i := 0; i < 50; i++ {
		p, e := s.GLPage(ctx, "api/v4/projects?search="+url.QueryEscape(target), page, 100, false)
		if e != nil {
			return "", e
		}
		for _, m := range p.Items {
			id := refs.Int(m["id"])
			name := Str(m["path_with_namespace"])
			if id < 1 || name == "" {
				continue
			}
			if full && strings.EqualFold(name, configured) || !full && strings.EqualFold(Str(m["path"]), configured) {
				unique[id] = name
			}
		}
		if p.Next == 0 {
			ended = true
			break
		}
		page = p.Next
	}
	if !ended {
		return "", fmt.Errorf("проект %s: более 50 страниц поиска; укажите числовой ID", configured)
	}
	if len(unique) == 0 {
		return "", fmt.Errorf("проект %s не найден или недоступен токену", configured)
	}
	if len(unique) > 1 {
		names := []string{}
		for _, n := range unique {
			names = append(names, n)
		}
		sort.Strings(names)
		return "", fmt.Errorf("неоднозначный проект %s: %s", configured, strings.Join(names, ", "))
	}
	var id int
	for i := range unique {
		id = i
	}
	value := strconv.Itoa(id)
	s.mu.Lock()
	s.projectIDs[key] = value
	s.mu.Unlock()
	return value, nil
}
