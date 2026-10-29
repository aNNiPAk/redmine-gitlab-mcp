package projectcontext

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"redmine-gitlab-mcp/internal/api"
	"strings"
	"testing"
)

func TestEndToEndTools(t *testing.T) {
	var server *httptest.Server
	handler := func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		send := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		if r.Header.Get("PRIVATE-TOKEN") != "secret-git" && strings.HasPrefix(path, "/git/") {
			t.Errorf("GitLab token missing")
		}
		switch {
		case path == "/rm/issues/123.json":
			send(map[string]any{"issue": map[string]any{"id": 123, "subject": "Implement feature", "description": "Relates to https://" + strings.TrimPrefix(server.URL, "http://") + "/git/dev/server/-/merge_requests/26", "journals": []any{map[string]any{"id": 1, "notes": "fix 123"}, map[string]any{"id": 2, "notes": "last note"}}}})
		case path == "/rm/issues.json":
			send(map[string]any{"issues": []any{}})
		case path == "/rm/attachments/5.json":
			send(map[string]any{"attachment": map[string]any{"id": 5, "filename": "help.txt", "filesize": 11, "content_type": "text/plain", "content_url": server.URL + "/rm/attachments/download/5/help.txt"}})
		case path == "/rm/attachments/download/5/help.txt":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "hello world")
		case path == "/git/api/v4/projects":
			send([]any{map[string]any{"id": 42, "path": "server", "path_with_namespace": "dev/server"}, map[string]any{"id": 55, "path": "server", "path_with_namespace": "ops/server"}, map[string]any{"id": 47, "path": "theme", "path_with_namespace": "dev/theme"}})
		case path == "/git/api/v4/projects/42":
			send(map[string]any{"id": 42, "path_with_namespace": "dev/server"})
		case path == "/git/api/v4/projects/47":
			send(map[string]any{"id": 47, "path_with_namespace": "dev/theme"})
		case path == "/git/api/v4/projects/42/merge_requests":
			send([]any{map[string]any{"iid": 26}})
		case strings.HasSuffix(path, "/search"):
			send([]any{map[string]any{"iid": 26, "title": "Fix Redmine #123", "description": "fix 123", "web_url": "url"}})
		case strings.HasSuffix(path, "/merge_requests/26"):
			send(map[string]any{"id": 900, "iid": 26, "title": "Fix issue", "description": "Detailed MR", "state": "opened", "sha": "abcdef1234567", "diff_refs": map[string]any{"head_sha": "abcdef1234567"}})
		case strings.HasSuffix(path, "/merge_requests/26/discussions"):
			send([]any{map[string]any{"id": "discussion1", "notes": []any{map[string]any{"id": 201, "body": "see !7#note_999 and commit a738f717", "resolvable": true, "resolved": false, "position": map[string]any{"head_sha": "aaaaaaa", "new_path": "test.go"}}, map[string]any{"id": 202, "body": "follow-up"}}}})
		case strings.HasSuffix(path, "/merge_requests/26/discussions/discussion1"):
			send(map[string]any{"id": "discussion1", "notes": []any{map[string]any{"id": 201, "body": "see !7#note_999", "resolvable": true, "resolved": false}}})
		case strings.HasSuffix(path, "/merge_requests/26/changes"):
			send(map[string]any{"changes": []any{map[string]any{"old_path": "test.go", "new_path": "test.go", "diff": "@@ -1 +1 @@\n-old\n+new"}}})
		case strings.HasSuffix(path, "/merge_requests/26/commits"):
			send([]any{map[string]any{"id": "a738f717", "title": "fix it"}})
		case strings.HasSuffix(path, "/merge_requests/26/pipelines"):
			send([]any{map[string]any{"id": 77, "status": "failed"}})
		case strings.HasSuffix(path, "/pipelines/77/jobs"):
			send([]any{map[string]any{"id": 88, "name": "test", "status": "failed"}})
		case strings.HasSuffix(path, "/jobs/88/trace"):
			fmt.Fprint(w, "ERROR: tests failed")
		case strings.HasSuffix(path, "/repository/commits/a738f717/diff"):
			send([]any{map[string]any{"old_path": "test.go", "new_path": "test.go", "diff": "@@ single commit"}})
		case strings.HasSuffix(path, "/repository/commits/a738f717"):
			send(map[string]any{"title": "fix it", "committed_date": "2026-10-26"})
		case strings.HasSuffix(path, "/repository/compare"):
			send(map[string]any{"diffs": []any{map[string]any{"old_path": "test.go", "new_path": "test.go", "diff": "@@ compare"}}, "commits": []any{}})
		default:
			t.Logf("unhandled %s %s", path, r.URL.RawQuery)
			w.WriteHeader(404)
		}
	}
	server = httptest.NewServer(http.HandlerFunc(handler))
	defer server.Close()
	cfg := api.Config{RedmineURL: server.URL + "/rm", RedmineAPIKey: "secret-red", GitlabURL: server.URL + "/git", GitlabToken: "secret-git", GitlabProject: "dev/server", GitlabProjects: map[string]string{"theme": "dev/theme"}}
	s := New(cfg)
	ctx := context.Background()
	check := func(name string, v any, e error) {
		t.Helper()
		if e != nil {
			t.Fatalf("%s failed: %v", name, e)
		}
		if v == nil {
			t.Fatalf("%s returned nil", name)
		}
	}
	issue, e := s.Issue(ctx, 123, "full", 0, 1, false)
	check("issue", issue, e)
	if api.Map(issue)["nextJournalOffset"] != 1 {
		t.Fatalf("issue pagination %#v", issue)
	}
	attachment, e := s.IssueAttachment(ctx, 5)
	check("attachment", attachment, e)
	if api.Map(attachment)["text"] != "hello world" {
		t.Fatalf("attachment wrong %#v", attachment)
	}
	mr, e := s.MergeRequest(ctx, "!26", "summary", 1, 1, false)
	check("MR", mr, e)
	urlMR, e := s.MergeRequest(ctx, server.URL+"/git/dev/server/merge_requests/26", "summary", 1, 1, false)
	check("MR URL", urlMR, e)
	urlDiff, e := s.MRDiff(ctx, server.URL+"/git/dev/server/-/merge_requests/26", "test.go", 0)
	check("MR diff URL", urlDiff, e)
	_, e = s.mrProject(ctx, "", server.URL+"/git/dev/server/merge_requests/26")
	if e != nil {
		t.Fatalf("MR project URL: %v", e)
	}
	if api.Map(mr)["partial"] != false {
		t.Fatalf("partial on happy path %#v", mr)
	}
	theme, e := s.API.Project(ctx, "theme")
	if e != nil || theme != "47" {
		t.Fatalf("alias failed %s %v", theme, e)
	}
	rev, e := s.Review(ctx, "!26", 0, 20, false, false)
	check("review", rev, e)
	if api.Map(rev)["unresolvedCount"] != 1 || len(api.Array(api.Map(rev)["linkedDiscussions"])) != 1 {
		t.Fatalf("review wrong %#v", rev)
	}
	d, e := s.Discussion(ctx, "!26#note_201", false)
	check("discussion", d, e)
	if api.Map(d)["lookup"] != "cache" {
		t.Fatalf("note index failed %#v", d)
	}
	diff, e := s.MRDiff(ctx, "theme!26", "test.go", 0)
	check("diff", diff, e)
	commit, e := s.CommitDiff(ctx, "a738f717", "", "theme!26", "test.go", "full", 0, false)
	check("commit diff", commit, e)
	comp, e := s.Compare(ctx, "a738f717", "b829f812", "theme", "", "test.go", true, "summary", 0, false)
	check("compare", comp, e)
	jobs, e := s.PipelineJobs(ctx, "!26", 77, 1)
	check("jobs", jobs, e)
	log, e := s.JobTrace(ctx, "!26", 88, 0)
	check("log", log, e)
	related, e := s.FindRelated(ctx, 123, "", false)
	check("related", related, e)
	doctor, e := s.Doctor(ctx)
	check("doctor", doctor, e)
	if api.Map(doctor)["ok"] != true {
		t.Fatalf("doctor did not pass %#v", doctor)
	}
}
func TestPartialOn403(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/merge_requests/9"):
			fmt.Fprint(w, `{"id":9,"title":"a"}`)
		case strings.HasSuffix(r.URL.Path, "/changes"):
			w.WriteHeader(403)
		default:
			fmt.Fprint(w, `[]`)
		}
	}))
	defer server.Close()
	c := api.Config{GitlabURL: server.URL, RedmineURL: server.URL, GitlabProject: "42", GitlabToken: "s", RedmineAPIKey: "s"}
	s := New(c)
	res, e := s.MergeRequest(context.Background(), "!9", "summary", 1, 1, false)
	if e != nil || api.Map(res)["partial"] != true {
		t.Fatalf("expected partial %+v %v", res, e)
	}
}
