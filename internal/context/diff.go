package projectcontext

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"redmine-gitlab-mcp/internal/api"
	"redmine-gitlab-mcp/internal/refs"
)

func selectedDiff(files any, path string) ([]map[string]any, error) {
	a, ok := files.([]any)
	if !ok {
		return nil, fmt.Errorf("GitLab вернул некорректный diff")
	}
	selected := []map[string]any{}
	for _, item := range a {
		f, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if path == "" || api.Str(f["old_path"]) == path || api.Str(f["new_path"]) == path {
			selected = append(selected, f)
		}
	}
	if path != "" && len(selected) == 0 {
		return nil, fmt.Errorf("файл не найден в diff: %s", path)
	}
	return selected, nil
}
func diffSummary(files []map[string]any) []any {
	list := []any{}
	for _, f := range files {
		list = append(list, map[string]any{"old_path": f["old_path"], "new_path": f["new_path"], "new_file": api.Bool(f["new_file"]), "deleted_file": api.Bool(f["deleted_file"]), "renamed_file": api.Bool(f["renamed_file"]), "diff_empty": api.Str(f["diff"]) == ""})
	}
	return list
}
func renderDiff(files []map[string]any) string {
	parts := []string{}
	for _, f := range files {
		diff := api.Str(f["diff"])
		if diff == "" {
			diff = "(diff отсутствует: бинарный файл либо ограничение GitLab)"
		}
		parts = append(parts, fmt.Sprintf("=== %s -> %s ===\n%s", api.Str(f["old_path"]), api.Str(f["new_path"]), diff))
	}
	return strings.Join(parts, "\n\n")
}
func (s *Service) MRDiff(ctx context.Context, reference, path string, offset int) (any, error) {
	iid, project, base, e := s.mrLocation(ctx, reference)
	if e != nil {
		return nil, e
	}
	data, e := doc(ctx, s.API.Gitlab, base+"/changes", false)
	if e != nil {
		return nil, e
	}
	rawFiles := data["changes"]
	if rawFiles == nil {
		rawFiles = []any{}
	}
	files, e := selectedDiff(rawFiles, path)
	if e != nil {
		return nil, e
	}
	part, e := Slice(renderDiff(files), offset, DiffChunk)
	if e != nil {
		return nil, e
	}
	return map[string]any{"reference": reference, "project": project, "iid": iid, "path": nilIfEmpty(path), "offset": part["offset"], "totalLength": part["totalLength"], "nextOffset": part["nextOffset"], "diff": part["text"], "overflow": api.Bool(data["overflow"]), "note": "При серверном усечении diff используйте локальный Git и SHA коммитов"}, nil
}
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func (s *Service) CommitDiff(ctx context.Context, sha, project, reference, path, mode string, offset int, refresh bool) (any, error) {
	sha, e := refs.ValidSHA(sha)
	if e != nil {
		return nil, e
	}
	id, e := s.mrProject(ctx, project, reference)
	if e != nil {
		return nil, e
	}
	prefix := fmt.Sprintf("api/v4/projects/%s/repository/commits/%s", id, sha)
	obj, _, e := s.API.Gitlab.JSON(ctx, prefix+"/diff", refresh)
	if e != nil {
		return nil, e
	}
	files, e := selectedDiff(obj, path)
	if e != nil {
		return nil, e
	}
	meta, me := safeSection("commit_details", func() (map[string]any, error) { return doc(ctx, s.API.Gitlab, prefix, refresh) })
	summary := diffSummary(files)
	var content map[string]any
	if mode == "full" {
		content, e = Slice(renderDiff(files), offset, DiffChunk)
		if e != nil {
			return nil, e
		}
	}
	errors := failures(me)
	res := map[string]any{"sha": sha, "project": id, "reference": nilIfEmpty(reference), "mode": mode, "path": nilIfEmpty(path), "title": meta["title"], "message": meta["message"], "committed_date": meta["committed_date"], "partial": len(errors) > 0, "errors": errors, "changed_files_count": len(summary), "changed_files": summary[:min(len(summary), 200)], "filesPreviewTruncated": len(summary) > 200, "note": "GitLab 11.11.3 может ограничивать diff на сервере; отсутствие файла не доказывает, что его не меняли"}
	if content != nil {
		res["offset"] = content["offset"]
		res["totalLength"] = content["totalLength"]
		res["nextOffset"] = content["nextOffset"]
		res["diff"] = content["text"]
	}
	return res, nil
}
func (s *Service) Compare(ctx context.Context, from, to, project, reference, path string, straight bool, mode string, offset int, refresh bool) (any, error) {
	from, e := refs.ValidSHA(from)
	if e != nil {
		return nil, e
	}
	to, e = refs.ValidSHA(to)
	if e != nil {
		return nil, e
	}
	id, e := s.mrProject(ctx, project, reference)
	if e != nil {
		return nil, e
	}
	query := url.Values{"from": {from}, "to": {to}, "straight": {fmt.Sprintf("%t", straight)}}
	data, e := doc(ctx, s.API.Gitlab, fmt.Sprintf("api/v4/projects/%s/repository/compare?%s", id, query.Encode()), refresh)
	if e != nil {
		return nil, e
	}
	files, e := selectedDiff(data["diffs"], path)
	if e != nil {
		return nil, e
	}
	summary := diffSummary(files)
	commits := api.Objects(data["commits"])
	preview := []any{}
	for i, c := range commits {
		if i >= 50 {
			break
		}
		preview = append(preview, commitSummary(c))
	}
	var latest any
	if c := api.Map(data["commit"]); len(c) > 0 {
		latest = commitSummary(c)
	}
	res := map[string]any{"from": from, "to": to, "project": id, "reference": nilIfEmpty(reference), "straight": straight, "mode": mode, "path": nilIfEmpty(path), "compare_timeout": api.Bool(data["compare_timeout"]), "compare_same_ref": api.Bool(data["compare_same_ref"]), "latest_commit": latest, "commits_count": len(commits), "commits": preview, "commitsPreviewTruncated": len(commits) > 50, "changed_files_count": len(summary), "changed_files": summary[:min(len(summary), 200)], "filesPreviewTruncated": len(summary) > 200, "note": "compare_timeout и ограничения сервера GitLab могут делать diff неполным"}
	if mode == "full" {
		part, e := Slice(renderDiff(files), offset, DiffChunk)
		if e != nil {
			return nil, e
		}
		res["offset"] = part["offset"]
		res["totalLength"] = part["totalLength"]
		res["nextOffset"] = part["nextOffset"]
		res["diff"] = part["text"]
	}
	return res, nil
}
func (s *Service) PipelineJobs(ctx context.Context, reference string, pipelineID, page int) (any, error) {
	_, project, _, e := s.mrLocation(ctx, reference)
	if e != nil {
		return nil, e
	}
	p, e := s.API.GLPage(ctx, fmt.Sprintf("api/v4/projects/%s/pipelines/%d/jobs", project, pipelineID), page, 50, false)
	if e != nil {
		return nil, e
	}
	jobs := []any{}
	for _, j := range p.Items {
		jobs = append(jobs, api.Pick(j, "id", "name", "stage", "status", "created_at", "finished_at", "web_url"))
	}
	return map[string]any{"reference": reference, "project": project, "pipelineId": pipelineID, "page": page, "nextPage": nullableInt(p.Next), "jobs": jobs}, nil
}
func (s *Service) JobTrace(ctx context.Context, reference string, jobID, offset int) (any, error) {
	_, project, _, e := s.mrLocation(ctx, reference)
	if e != nil {
		return nil, e
	}
	data, e := s.API.Gitlab.Bytes(ctx, fmt.Sprintf("api/v4/projects/%s/jobs/%d/trace", project, jobID), 16*1024*1024)
	if e != nil {
		return nil, e
	}
	part, e := Slice(string(data), offset, LogChunk)
	if e != nil {
		return nil, e
	}
	return map[string]any{"reference": reference, "jobId": jobID, "offset": part["offset"], "totalLength": part["totalLength"], "nextOffset": part["nextOffset"], "trace": part["text"]}, nil
}
