package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"redmine-gitlab-mcp/internal/api"
	projectcontext "redmine-gitlab-mcp/internal/context"
	"redmine-gitlab-mcp/internal/mcp"
	"redmine-gitlab-mcp/internal/version"
	"strings"
)

func main() {
	configPath := flag.String("config", api.DefaultConfigPath(), "Path to config.local.json")
	doctor := flag.Bool("doctor", false, "Check Redmine and GitLab API connectivity")
	ver := flag.Bool("version", false, "Print version")
	flag.Parse()
	if *ver {
		fmt.Println(version.Version)
		return
	}
	cfg, e := api.LoadConfig(*configPath)
	if e != nil {
		fmt.Fprintln(os.Stderr, "Configuration:", e)
		os.Exit(1)
	}
	for _, endpoint := range []struct{ name, value string }{{"redmineUrl", cfg.RedmineURL}, {"gitlabUrl", cfg.GitlabURL}} {
		u, _ := url.Parse(endpoint.value)
		if u.Scheme == "http" && !strings.EqualFold(u.Hostname(), "localhost") && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1" {
			fmt.Fprintln(os.Stderr, "WARNING:", endpoint.name, "uses HTTP without TLS; API tokens are transmitted in plaintext")
		}
	}
	service := projectcontext.New(cfg)
	if *doctor {
		result, e := service.Doctor(context.Background())
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		b, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(b))
		if result["ok"] != true {
			os.Exit(1)
		}
		return
	}
	server := &mcp.Server{Version: version.Version, Tools: tools(service)}
	fmt.Fprintln(os.Stderr, "Redmine GitLab MCP", version.Version, "ready (stdio, read-only)")
	if e = server.Serve(context.Background(), os.Stdin, os.Stdout); e != nil {
		fmt.Fprintln(os.Stderr, "MCP:", e)
		os.Exit(1)
	}
}
func tool(name, desc string, required []string, props map[string]any, handler func(context.Context, map[string]any) (any, error)) mcp.Tool {
	return mcp.Tool{Name: name, Description: desc, Schema: mcp.Schema(required, props), Handler: handler}
}
func sField(desc string) any { return mcp.Field("string", desc) }
func bField(desc string) any { return mcp.Field("boolean", desc) }
func nField(desc string) any {
	return map[string]any{"type": "integer", "minimum": 0, "description": desc}
}
func positive(name string, a map[string]any, def int, required bool) (int, error) {
	return mcp.Positive(name, a, def, required)
}
func nonNegative(name string, a map[string]any, def int) (int, error) {
	return mcp.NonNegative(name, a, def)
}
func stringField(name string, a map[string]any, required bool) (string, error) {
	return mcp.String(name, a, required)
}
func boolField(name string, a map[string]any, def bool) (bool, error) { return mcp.Bool(name, a, def) }
func modeField(a map[string]any) (string, error)                      { return mcp.Mode(a) }
func checkLimit(n, maximum int) error {
	if n > maximum {
		return fmt.Errorf("limit не должен превышать %d", maximum)
	}
	return nil
}
func tools(s *projectcontext.Service) []mcp.Tool {
	t := []mcp.Tool{}
	t = append(t, tool("get_issue", "Read Redmine #ID, latest journal entries in summary; full mode pages all entries", []string{"id"}, map[string]any{"id": nField("Redmine issue ID"), "mode": sField("summary or full"), "journalOffset": nField("Full journal offset, default 0"), "journalLimit": nField("Full journal page size, default 30, max 100"), "refresh": bField("Bypass cache")}, func(ctx context.Context, a map[string]any) (any, error) {
		id, e := positive("id", a, 0, true)
		if e != nil {
			return nil, e
		}
		mode, e := modeField(a)
		if e != nil {
			return nil, e
		}
		offset, e := nonNegative("journalOffset", a, 0)
		if e != nil {
			return nil, e
		}
		limit, e := positive("journalLimit", a, 30, false)
		if e != nil {
			return nil, e
		}
		if e = checkLimit(limit, 100); e != nil {
			return nil, e
		}
		refresh, e := boolField("refresh", a, false)
		if e != nil {
			return nil, e
		}
		return s.Issue(ctx, id, mode, offset, limit, refresh)
	}))
	t = append(t, tool("get_issue_attachment", "Read text attachment from Redmine up to 512 KiB", []string{"id"}, map[string]any{"id": nField("Attachment numeric ID")}, func(ctx context.Context, a map[string]any) (any, error) {
		id, e := positive("id", a, 0, true)
		if e != nil {
			return nil, e
		}
		return s.IssueAttachment(ctx, id)
	}))
	t = append(t, tool("get_merge_request", "Read MR !9999, theme!9999 or GitLab MR URL, summary/full; partial=true on noncritical API failure", []string{"reference"}, map[string]any{"reference": sField("!9999, theme!9999 or GitLab MR URL"), "mode": sField("summary or full"), "discussionPage": nField("Discussion page default 1"), "commitsPage": nField("Commits page default 1"), "refresh": bField("Bypass cache")}, func(ctx context.Context, a map[string]any) (any, error) {
		ref, e := stringField("reference", a, true)
		if e != nil {
			return nil, e
		}
		mode, e := modeField(a)
		if e != nil {
			return nil, e
		}
		dp, e := positive("discussionPage", a, 1, false)
		if e != nil {
			return nil, e
		}
		cp, e := positive("commitsPage", a, 1, false)
		if e != nil {
			return nil, e
		}
		r, e := boolField("refresh", a, false)
		if e != nil {
			return nil, e
		}
		return s.MergeRequest(ctx, ref, mode, dp, cp, r)
	}))
	t = append(t, tool("get_mr_review", "Read MR review, unresolved discussions, linkedDiscussions, commitReferences; paginate with nextOffset", []string{"reference"}, map[string]any{"reference": sField("!9999, theme!9999 or GitLab MR URL"), "offset": nField("Review offset"), "limit": nField("Page size default 20, max 50"), "includeResolved": bField("Include resolved discussions"), "refresh": bField("Bypass cache")}, func(ctx context.Context, a map[string]any) (any, error) {
		ref, e := stringField("reference", a, true)
		if e != nil {
			return nil, e
		}
		off, e := nonNegative("offset", a, 0)
		if e != nil {
			return nil, e
		}
		limit, e := positive("limit", a, 20, false)
		if e != nil {
			return nil, e
		}
		if e = checkLimit(limit, 50); e != nil {
			return nil, e
		}
		include, e := boolField("includeResolved", a, false)
		if e != nil {
			return nil, e
		}
		r, e := boolField("refresh", a, false)
		if e != nil {
			return nil, e
		}
		return s.Review(ctx, ref, off, limit, include, r)
	}))
	t = append(t, tool("get_mr_discussion", "Return entire thread by !9999#note_99999, theme!9999#note_99999 or full GitLab URL; nonrecursive", []string{"reference"}, map[string]any{"reference": sField("MR comment link"), "refresh": bField("Bypass cache")}, func(ctx context.Context, a map[string]any) (any, error) {
		ref, e := stringField("reference", a, true)
		if e != nil {
			return nil, e
		}
		r, e := boolField("refresh", a, false)
		if e != nil {
			return nil, e
		}
		return s.Discussion(ctx, ref, r)
	}))
	t = append(t, tool("find_related_merge_requests", "Bounded search GitLab MR titles/descriptions referencing Redmine issue", []string{"issueId"}, map[string]any{"issueId": nField("Redmine issue ID"), "project": sField("Optional GitLab project alias"), "refresh": bField("Bypass cache")}, func(ctx context.Context, a map[string]any) (any, error) {
		id, e := positive("issueId", a, 0, true)
		if e != nil {
			return nil, e
		}
		project, e := stringField("project", a, false)
		if e != nil {
			return nil, e
		}
		r, e := boolField("refresh", a, false)
		if e != nil {
			return nil, e
		}
		return s.FindRelated(ctx, id, project, r)
	}))
	t = append(t, tool("get_merge_request_diff", "Read MR diff with optional path filter and 16000-character chunks", []string{"reference"}, map[string]any{"reference": sField("!9999, theme!9999 or GitLab MR URL"), "path": sField("Optional file path"), "offset": nField("Diff text offset")}, func(ctx context.Context, a map[string]any) (any, error) {
		ref, e := stringField("reference", a, true)
		if e != nil {
			return nil, e
		}
		path, e := stringField("path", a, false)
		if e != nil {
			return nil, e
		}
		offset, e := nonNegative("offset", a, 0)
		if e != nil {
			return nil, e
		}
		return s.MRDiff(ctx, ref, path, offset)
	}))
	t = append(t, tool("get_commit_diff", "Get a commit diff by SHA, optionally project or reference, path, summary/full, chunk offset", []string{"sha"}, map[string]any{"sha": sField("7-40 hex SHA"), "project": sField("Project alias or ID"), "reference": sField("MR reference instead of project"), "path": sField("Optional file path"), "mode": sField("summary or full"), "offset": nField("Chunk offset"), "refresh": bField("Bypass cache")}, func(ctx context.Context, a map[string]any) (any, error) {
		sha, e := stringField("sha", a, true)
		if e != nil {
			return nil, e
		}
		project, e := stringField("project", a, false)
		if e != nil {
			return nil, e
		}
		ref, e := stringField("reference", a, false)
		if e != nil {
			return nil, e
		}
		path, e := stringField("path", a, false)
		if e != nil {
			return nil, e
		}
		mode, e := modeField(a)
		if e != nil {
			return nil, e
		}
		offset, e := nonNegative("offset", a, 0)
		if e != nil {
			return nil, e
		}
		r, e := boolField("refresh", a, false)
		if e != nil {
			return nil, e
		}
		return s.CommitDiff(ctx, sha, project, ref, path, mode, offset, r)
	}))
	t = append(t, tool("compare_commits", "Compare two commit SHAs; straight=true means from..to", []string{"from", "to"}, map[string]any{"from": sField("7-40 hex SHA"), "to": sField("7-40 hex SHA"), "project": sField("Project alias or ID"), "reference": sField("MR reference instead of project"), "path": sField("Optional file path"), "straight": bField("Default true; false uses merge-base"), "mode": sField("summary or full"), "offset": nField("Chunk offset"), "refresh": bField("Bypass cache")}, func(ctx context.Context, a map[string]any) (any, error) {
		from, e := stringField("from", a, true)
		if e != nil {
			return nil, e
		}
		to, e := stringField("to", a, true)
		if e != nil {
			return nil, e
		}
		project, e := stringField("project", a, false)
		if e != nil {
			return nil, e
		}
		ref, e := stringField("reference", a, false)
		if e != nil {
			return nil, e
		}
		path, e := stringField("path", a, false)
		if e != nil {
			return nil, e
		}
		straight, e := boolField("straight", a, true)
		if e != nil {
			return nil, e
		}
		mode, e := modeField(a)
		if e != nil {
			return nil, e
		}
		offset, e := nonNegative("offset", a, 0)
		if e != nil {
			return nil, e
		}
		r, e := boolField("refresh", a, false)
		if e != nil {
			return nil, e
		}
		return s.Compare(ctx, from, to, project, ref, path, straight, mode, offset, r)
	}))
	t = append(t, tool("get_pipeline_jobs", "List jobs for a GitLab pipeline ID", []string{"reference", "pipelineId"}, map[string]any{"reference": sField("!9999, theme!9999 or GitLab MR URL"), "pipelineId": nField("Pipeline numeric ID"), "page": nField("Page default 1")}, func(ctx context.Context, a map[string]any) (any, error) {
		ref, e := stringField("reference", a, true)
		if e != nil {
			return nil, e
		}
		id, e := positive("pipelineId", a, 0, true)
		if e != nil {
			return nil, e
		}
		p, e := positive("page", a, 1, false)
		if e != nil {
			return nil, e
		}
		return s.PipelineJobs(ctx, ref, id, p)
	}))
	t = append(t, tool("get_job_trace", "Read GitLab CI job log 16000 characters per call (max 16 MiB)", []string{"reference", "jobId"}, map[string]any{"reference": sField("!9999, theme!9999 or GitLab MR URL"), "jobId": nField("CI job numeric ID"), "offset": nField("Trace chunk offset")}, func(ctx context.Context, a map[string]any) (any, error) {
		ref, e := stringField("reference", a, true)
		if e != nil {
			return nil, e
		}
		id, e := positive("jobId", a, 0, true)
		if e != nil {
			return nil, e
		}
		offset, e := nonNegative("offset", a, 0)
		if e != nil {
			return nil, e
		}
		return s.JobTrace(ctx, ref, id, offset)
	}))
	return t
}
