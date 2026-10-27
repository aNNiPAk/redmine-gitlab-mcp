package projectcontext

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"redmine-gitlab-mcp/internal/api"
	"redmine-gitlab-mcp/internal/refs"
)

const DiffChunk = 16000
const LogChunk = 16000
const MaxFileBytes = 512 * 1024
const MaxCommitRefs = 100
const MaxDiscussionLinks = 100

type Service struct {
	API   *api.Service
	Notes *refs.NoteIndex
}

func New(c api.Config) *Service { return &Service{API: api.NewService(c), Notes: refs.NewNoteIndex()} }
func (s *Service) mrLocation(ctx context.Context, reference string) (int, string, string, error) {
	m, e := refs.ParseMR(reference)
	if e != nil {
		return 0, "", "", e
	}
	project, e := s.API.Project(ctx, m.Alias)
	if e != nil {
		return 0, "", "", e
	}
	return m.IID, project, fmt.Sprintf("api/v4/projects/%s/merge_requests/%d", project, m.IID), nil
}
func (s *Service) mrProject(ctx context.Context, project, reference string) (string, error) {
	if project != "" && reference != "" {
		return "", fmt.Errorf("укажите project ИЛИ reference, но не оба")
	}
	alias := project
	if reference != "" {
		m, e := refs.ParseMR(reference)
		if e != nil {
			return "", e
		}
		alias = m.Alias
	}
	return s.API.Project(ctx, alias)
}
func Slice(text string, offset, limit int) (map[string]any, error) {
	r := []rune(text)
	if offset < 0 || offset > len(r) {
		return nil, fmt.Errorf("неверное смещение %d (длина %d)", offset, len(r))
	}
	end := min(len(r), offset+limit)
	var next any
	if end < len(r) {
		next = end
	}
	return map[string]any{"offset": offset, "totalLength": len(r), "nextOffset": next, "text": string(r[offset:end])}, nil
}
func nullableInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}
func errMap(section string, e error) api.Failure { return api.Failed(section, e) }
func doc(ctx context.Context, client *api.Client, rel string, fresh bool) (map[string]any, error) {
	obj, _, e := client.JSON(ctx, rel, fresh)
	if e != nil {
		return nil, e
	}
	m, ok := obj.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("API вернул не объект: %s", rel)
	}
	return m, nil
}
func safeSection[T any](section string, f func() (T, error)) (T, *api.Failure) {
	v, e := f()
	if e != nil {
		er := api.Failed(section, e)
		return v, &er
	}
	return v, nil
}
func failures(list ...*api.Failure) []api.Failure {
	out := []api.Failure{}
	for _, e := range list {
		if e != nil {
			out = append(out, *e)
		}
	}
	return out
}
func readableJournal(j map[string]any) map[string]any {
	out := api.Pick(j, "id", "user", "created_on", "notes", "details")
	if out["details"] == nil {
		out["details"] = []any{}
	}
	return out
}
func commitSummary(c map[string]any) map[string]any {
	return api.Pick(c, "id", "short_id", "title", "created_at")
}
func (s *Service) ViewDiscussion(d map[string]any) map[string]any {
	notes := []any{}
	for _, n := range api.Objects(d["notes"]) {
		position := api.Map(n["position"])
		var pos any
		if n["position"] != nil {
			pos = api.Pick(position, "old_path", "new_path", "old_line", "new_line", "head_sha")
		}
		note := api.Pick(n, "id", "created_at", "updated_at", "body", "system", "resolvable", "resolved")
		author := api.Map(n["author"])
		note["author"] = author["name"]
		if note["author"] == nil {
			note["author"] = author["username"]
		}
		note["resolved_by"] = api.Map(n["resolved_by"])["name"]
		note["commitRefs"] = refs.ExtractSHAs(api.Str(n["body"]), 20)
		note["linkedDiscussions"] = refs.ExtractLinks(api.Str(n["body"]), s.API.Cfg.GitlabURL, 30)
		if pos != nil {
			note["position"] = pos
		}
		notes = append(notes, note)
	}
	return map[string]any{"id": d["id"], "individual_note": d["individual_note"], "notes": notes}
}
func active(d map[string]any) bool {
	for _, n := range api.Objects(d["notes"]) {
		if api.Bool(n["resolvable"]) && !api.Bool(n["resolved"]) {
			return true
		}
	}
	return false
}
func human(d map[string]any) bool {
	for _, n := range api.Objects(d["notes"]) {
		if !api.Bool(n["system"]) {
			return true
		}
	}
	return false
}
func fileChanges(changes []map[string]any) []any {
	out := []any{}
	for _, f := range changes {
		out = append(out, api.Pick(f, "old_path", "new_path", "new_file", "deleted_file", "renamed_file"))
	}
	return out
}
func issueStrength(text string, id int) string {
	// Go's regexp has no lookahead: use explicit left/right boundaries.
	needle := strconv.Itoa(id)
	l := strings.ToLower(text)
	for start := 0; start < len(l); {
		i := strings.Index(l[start:], needle)
		if i < 0 {
			break
		}
		i += start
		j := i + len(needle)
		left := i == 0 || !isDigit(l[i-1])
		right := j == len(l) || !isDigit(l[j])
		if left && right {
			pre := l[max(0, i-90):i]
			if strings.HasSuffix(pre, "#") || strings.HasSuffix(pre, "/") && strings.Contains(pre, "redmine") || strings.Contains(pre, "redmine") && strings.Contains(pre, "#") {
				return "explicit"
			}
		}
		start = j
	}
	if regexp.MustCompile(`(^|\D)` + regexp.QuoteMeta(needle) + `($|\D)`).MatchString(text) {
		return "possible"
	}
	return ""
}
func isDigit(b byte) bool             { return b >= '0' && b <= '9' }
func EncodeQuery(v url.Values) string { return v.Encode() }
