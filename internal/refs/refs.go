package refs

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var mrPattern = regexp.MustCompile(`^([a-zA-Z0-9][a-zA-Z0-9_./-]*)?!(\d+)$`)
var notePattern = regexp.MustCompile(`^(.*!\d+)#note_(\d+)$`)
var fullMRPath = regexp.MustCompile(`^(.+?)/(?:-/)?merge_requests/(\d+)(?:/diffs)?/?$`)
var fullAnyMRPath = regexp.MustCompile(`^(.+?)/(?:-/)?merge_requests/(\d+)(?:/.*)?$`)
var anchorPattern = regexp.MustCompile(`^#note_(\d+)$`)
var shaPattern = regexp.MustCompile(`(?i)(^|[^a-z0-9])([a-f0-9]{7,40})(?:\b)`)
var bareMR = regexp.MustCompile(`(^|[^a-zA-Z0-9_./!-])((?:[a-zA-Z0-9][a-zA-Z0-9_./-]*)?![1-9]\d*#note_[1-9]\d*)(?:\b)`)
var urlPattern = regexp.MustCompile(`https?://[^\s<>"'()\[\]]+`)
var validSha = regexp.MustCompile(`^[a-f0-9]{7,40}$`)

type MR struct {
	IID   int
	Alias string
}
type Link struct {
	Reference string `json:"reference"`
	NoteID    int    `json:"noteId"`
}

func positive(text string) (int, error) {
	n, e := strconv.Atoi(text)
	if e != nil || n <= 0 {
		return 0, fmt.Errorf("некорректный положительный ID")
	}
	return n, nil
}
func ParseMR(reference string) (MR, error) {
	m := mrPattern.FindStringSubmatch(strings.TrimSpace(reference))
	if m == nil {
		return MR{}, fmt.Errorf("неверная ссылка на MR: используйте !9999 или theme!9999")
	}
	if strings.Contains(m[1], "/") {
		for _, p := range strings.Split(m[1], "/") {
			if p == "" || p == "." || p == ".." {
				return MR{}, fmt.Errorf("некорректный путь проекта")
			}
		}
	}
	id, e := positive(m[2])
	if e != nil {
		return MR{}, e
	}
	return MR{IID: id, Alias: m[1]}, nil
}
func baseURL(raw string) (*url.URL, error) {
	u, e := url.Parse(strings.TrimRight(raw, "/") + "/")
	if e != nil || u == nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("некорректный URL GitLab")
	}
	return u, nil
}
func ParseDiscussion(raw, gitlabURL string) (Link, error) {
	raw = strings.TrimSpace(raw)
	mrRef, note := "", ""
	if strings.HasPrefix(strings.ToLower(raw), "https://") || strings.HasPrefix(strings.ToLower(raw), "http://") {
		base, e := baseURL(gitlabURL)
		if e != nil {
			return Link{}, e
		}
		u, e := url.Parse(raw)
		if e != nil || u.User != nil || u.Host != base.Host || u.Scheme != base.Scheme || !strings.HasPrefix(u.EscapedPath(), base.EscapedPath()) {
			return Link{}, fmt.Errorf("ссылка должна принадлежать настроенному GitLab")
		}
		path, err := url.PathUnescape(strings.TrimPrefix(u.EscapedPath(), base.EscapedPath()))
		if err != nil {
			return Link{}, err
		}
		m := fullMRPath.FindStringSubmatch(path)
		a := anchorPattern.FindStringSubmatch("#" + u.Fragment)
		if m == nil || a == nil {
			return Link{}, fmt.Errorf("ожидается URL MR с #note_ID")
		}
		mrRef = m[1] + "!" + m[2]
		note = a[1]
	} else {
		m := notePattern.FindStringSubmatch(raw)
		if m == nil {
			return Link{}, fmt.Errorf("ожидается !9999#note_99999, theme!9999#note_99999 или GitLab URL")
		}
		mrRef = m[1]
		note = m[2]
	}
	mr, e := ParseMR(mrRef)
	if e != nil {
		return Link{}, e
	}
	id, e := positive(note)
	if e != nil {
		return Link{}, e
	}
	return Link{Reference: fmt.Sprintf("%s!%d#note_%d", mr.Alias, mr.IID, id), NoteID: id}, nil
}
func ExtractLinks(body, base string, limit int) []Link {
	if limit <= 0 {
		return nil
	}
	seen := map[string]bool{}
	out := []Link{}
	add := func(candidate string) {
		l, e := ParseDiscussion(strings.TrimRight(candidate, ".,;!?"), base)
		if e != nil {
			return
		}
		key := strings.ToLower(l.Reference)
		if !seen[key] && len(out) < limit {
			seen[key] = true
			out = append(out, l)
		}
	}
	for _, u := range urlPattern.FindAllString(body, -1) {
		add(u)
	}
	for _, m := range bareMR.FindAllStringSubmatch(body, -1) {
		add(m[2])
	}
	return out
}
func ExtractSHAs(body string, limit int) []string {
	out := []string{}
	seen := map[string]bool{}
	if limit <= 0 {
		return out
	}
	matches := shaPattern.FindAllStringSubmatchIndex(body, -1)
	for _, m := range matches {
		if len(m) < 6 {
			continue
		}
		start, end := m[4], m[5]
		sha := strings.ToLower(body[start:end])
		if !validSha.MatchString(sha) {
			continue
		}
		if _, e := strconv.ParseUint(sha, 10, 64); e == nil && strings.Trim(sha, "0123456789") == "" {
			before := strings.ToLower(body[max(0, start-30):start])
			if !regexp.MustCompile(`(?:commit|коммит|sha|revision|ревизи[яи])\s*[:#=]?\s*$`).MatchString(before) {
				continue
			}
		}
		if !seen[sha] {
			seen[sha] = true
			out = append(out, sha)
		}
		if len(out) == limit {
			break
		}
	}
	return out
}
func ValidSHA(input string) (string, error) {
	sha := strings.ToLower(strings.TrimSpace(input))
	if !validSha.MatchString(sha) {
		return "", fmt.Errorf("ожидался SHA коммита (7–40 hex символов)")
	}
	return sha, nil
}
func DirectMRURLs(body, gitlabURL string) []string {
	base, e := baseURL(gitlabURL)
	if e != nil {
		return nil
	}
	seen := map[string]bool{}
	out := []string{}
	for _, candidate := range urlPattern.FindAllString(body, -1) {
		u, e := url.Parse(strings.TrimRight(candidate, ".,;!?"))
		if e != nil || u.User != nil || u.Host != base.Host || u.Scheme != base.Scheme || !strings.HasPrefix(u.EscapedPath(), base.EscapedPath()) {
			continue
		}
		path, e := url.PathUnescape(strings.TrimPrefix(u.EscapedPath(), base.EscapedPath()))
		if e != nil {
			continue
		}
		m := fullAnyMRPath.FindStringSubmatch(path)
		if m == nil {
			continue
		}
		v := m[1] + "!" + m[2]
		if _, e := ParseMR(v); e == nil && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// NoteIndex is bounded and scoped by project, MR and note ID.
type NoteIndex struct {
	mu      sync.Mutex
	entries map[string]indexEntry
	ttl     time.Duration
	max     int
}
type indexEntry struct {
	id      string
	expires time.Time
}

func NewNoteIndex() *NoteIndex {
	return &NoteIndex{entries: map[string]indexEntry{}, ttl: 2 * time.Minute, max: 5000}
}
func noteKey(project string, iid, noteID int) string {
	return fmt.Sprintf("%s:%d:%d", project, iid, noteID)
}
func (n *NoteIndex) Get(project string, iid, noteID int) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	k := noteKey(project, iid, noteID)
	e, ok := n.entries[k]
	if !ok || time.Now().After(e.expires) {
		delete(n.entries, k)
		return ""
	}
	return e.id
}
func (n *NoteIndex) Delete(project string, iid, noteID int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.entries, noteKey(project, iid, noteID))
}
func (n *NoteIndex) ClearMR(project string, iid int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	prefix := fmt.Sprintf("%s:%d:", project, iid)
	for k := range n.entries {
		if strings.HasPrefix(k, prefix) {
			delete(n.entries, k)
		}
	}
}
func (n *NoteIndex) Remember(project string, iid int, discussions []map[string]any) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, d := range discussions {
		id, ok := d["id"].(string)
		if !ok || len(id) > 128 || id == "" {
			continue
		}
		notes, _ := d["notes"].([]any)
		for _, item := range notes {
			note, _ := item.(map[string]any)
			noteID := Int(note["id"])
			if noteID > 0 {
				n.entries[noteKey(project, iid, noteID)] = indexEntry{id: id, expires: time.Now().Add(n.ttl)}
			}
		}
	}
	for len(n.entries) > n.max {
		for k := range n.entries {
			delete(n.entries, k)
			break
		}
	}
}
func Int(v any) int {
	switch x := v.(type) {
	case float64:
		if x > 0 && x <= float64(int(^uint(0)>>1)) && float64(int(x)) == x {
			return int(x)
		}
	case int:
		return x
	case int64:
		return int(x)
	case interface{ String() string }:
		n, _ := strconv.Atoi(x.String())
		return n
	}
	return 0
}
func NoteIn(d map[string]any, noteID int) bool {
	notes, _ := d["notes"].([]any)
	for _, it := range notes {
		m, _ := it.(map[string]any)
		if Int(m["id"]) == noteID {
			return true
		}
	}
	return false
}
