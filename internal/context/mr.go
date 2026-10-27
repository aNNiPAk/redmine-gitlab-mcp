package projectcontext

import (
	"context"
	"fmt"
	"strings"

	"redmine-gitlab-mcp/internal/api"
	"redmine-gitlab-mcp/internal/refs"
)

func (s *Service) MergeRequest(ctx context.Context, reference, mode string, discussionPage, commitsPage int, refresh bool) (any, error) {
	iid, project, base, e := s.mrLocation(ctx, reference)
	if e != nil {
		return nil, e
	}
	mr, e := doc(ctx, s.API.Gitlab, base, refresh)
	if e != nil {
		return nil, e
	}
	discussions, de := safeSection("discussions", func() (api.Page, error) { return s.API.GLPage(ctx, base+"/discussions", discussionPage, 20, refresh) })
	changes, ce := safeSection("changes", func() (map[string]any, error) { return doc(ctx, s.API.Gitlab, base+"/changes", refresh) })
	commits, ke := safeSection("commits", func() (api.Page, error) { return s.API.GLPage(ctx, base+"/commits", commitsPage, 30, refresh) })
	pipeline, pe := safeSection("pipeline", func() (api.Page, error) { return s.API.GLPage(ctx, base+"/pipelines", 1, 1, refresh) })
	if refresh {
		s.Notes.ClearMR(project, iid)
	}
	if de == nil {
		s.Notes.Remember(project, iid, discussions.Items)
	}
	items := discussions.Items
	if de != nil {
		items = nil
	}
	selected := []map[string]any{}
	if mode == "summary" {
		for _, d := range items {
			if active(d) {
				selected = append(selected, d)
			}
		}
		other := []map[string]any{}
		for _, d := range items {
			if !active(d) {
				other = append(other, d)
			}
		}
		if len(other) > 3 {
			other = other[len(other)-3:]
		}
		selected = append(selected, other...)
	} else {
		selected = items
	}
	viewed := []any{}
	unresolved := 0
	for _, d := range selected {
		viewed = append(viewed, s.ViewDiscussion(d))
	}
	for _, d := range items {
		if active(d) {
			unresolved++
		}
	}
	files := fileChanges(api.Objects(changes["changes"]))
	cs := []any{}
	for i, c := range commits.Items {
		if mode == "summary" && i >= 5 {
			break
		}
		v := commitSummary(c)
		if mode == "full" {
			v["message"] = c["message"]
		}
		cs = append(cs, v)
	}
	var latest any
	if len(pipeline.Items) > 0 {
		latest = pipeline.Items[0]
	}
	var pipelineErr any
	if pe != nil {
		pipelineErr = pe.Message
	}
	errors := failures(de, ce, ke, pe)
	res := api.Pick(mr, "id", "title", "description", "web_url", "state", "source_branch", "target_branch", "created_at", "updated_at", "sha", "merge_status", "labels", "changes_count")
	res["author"] = api.Map(mr["author"])["name"]
	res["reference"] = reference
	res["project"] = project
	res["iid"] = iid
	res["mode"] = mode
	res["partial"] = len(errors) > 0
	res["errors"] = errors
	res["available"] = map[string]bool{"discussions": de == nil, "changes": ce == nil, "commits": ke == nil, "pipeline": pe == nil}
	res["overflow"] = changes["overflow"]
	res["latest_pipeline"] = latest
	res["pipeline_error"] = pipelineErr
	res["changed_files"] = files
	res["discussions"] = viewed
	res["discussionPage"] = discussionPage
	res["nextDiscussionPage"] = nullableInt(discussions.Next)
	res["discussionPreviewOnly"] = mode == "summary"
	if de == nil {
		res["unresolvedOnPage"] = unresolved
	} else {
		res["unresolvedOnPage"] = nil
	}
	res["commitsPage"] = commitsPage
	res["nextCommitsPage"] = nullableInt(commits.Next)
	res["commits"] = cs
	if mode == "summary" {
		res["note"] = "Для всех обсуждений используйте mode=full; для полного ревью — get_mr_review."
	}
	return res, nil
}
func (s *Service) allDiscussions(ctx context.Context, base, project string, iid int, refresh bool) ([]map[string]any, bool, []api.Failure) {
	if refresh {
		s.Notes.ClearMR(project, iid)
	}
	all := []map[string]any{}
	errors := []api.Failure{}
	for page, i := 1, 0; i < 20; i++ {
		result, e := s.API.GLPage(ctx, base+"/discussions", page, 100, refresh)
		if e != nil {
			errors = append(errors, errMap("discussions", e))
			break
		}
		all = append(all, result.Items...)
		s.Notes.Remember(project, iid, result.Items)
		if result.Next == 0 {
			return all, true, errors
		}
		page = result.Next
	}
	return all, false, errors
}
func (s *Service) Review(ctx context.Context, reference string, offset, limit int, includeResolved, refresh bool) (any, error) {
	iid, project, base, e := s.mrLocation(ctx, reference)
	if e != nil {
		return nil, e
	}
	mr, e := doc(ctx, s.API.Gitlab, base, refresh)
	if e != nil {
		return nil, e
	}
	all, complete, errors := s.allDiscussions(ctx, base, project, iid, refresh)
	human := []map[string]any{}
	unresolved := []map[string]any{}
	resolved := []map[string]any{}
	for _, d := range all {
		if !humanDiscussion(d) {
			continue
		}
		human = append(human, d)
		if active(d) {
			unresolved = append(unresolved, d)
		} else {
			for _, n := range api.Objects(d["notes"]) {
				if api.Bool(n["resolvable"]) {
					resolved = append(resolved, d)
					break
				}
			}
		}
	}
	source := unresolved
	if includeResolved {
		source = human
	}
	if offset > len(source) {
		return nil, fmt.Errorf("offset больше числа замечаний")
	}
	var next any
	end := min(len(source), offset+limit)
	if end < len(source) {
		next = end
	}
	head := api.Map(mr["diff_refs"])["head_sha"]
	if head == nil {
		head = mr["sha"]
	}
	resultItems := []any{}
	for _, d := range source[offset:end] {
		item := s.ViewDiscussion(d)
		item["unresolved"] = active(d)
		var version any
		for _, n := range api.Objects(d["notes"]) {
			if v := api.Map(n["position"])["head_sha"]; v != nil {
				version = v
				break
			}
		}
		item["diff_version"] = version
		item["older_diff_version"] = version != nil && head != nil && head != version
		resultItems = append(resultItems, item)
	}
	commitReferences := []any{}
	linked := []any{}
	totalCommits, totalLinks := 0, 0
	seen := map[string]bool{}
	for _, d := range human {
		for _, n := range api.Objects(d["notes"]) {
			if api.Bool(n["system"]) {
				continue
			}
			body := api.Str(n["body"])
			pos := api.Map(n["position"])
			path := pos["new_path"]
			if path == nil {
				path = pos["old_path"]
			}
			for _, sha := range refs.ExtractSHAs(body, 20) {
				totalCommits++
				if len(commitReferences) < MaxCommitRefs {
					commitReferences = append(commitReferences, map[string]any{"sha": sha, "discussionId": d["id"], "noteId": n["id"], "path": path})
				}
			}
			for _, l := range refs.ExtractLinks(body, s.API.Cfg.GitlabURL, 30) {
				key := fmt.Sprintf("%v:%v:%s", d["id"], n["id"], strings.ToLower(l.Reference))
				if seen[key] {
					continue
				}
				seen[key] = true
				totalLinks++
				if len(linked) < MaxDiscussionLinks {
					linked = append(linked, map[string]any{"reference": l.Reference, "discussionId": d["id"], "noteId": n["id"]})
				}
			}
		}
	}
	res := map[string]any{"reference": reference, "project": project, "iid": iid, "title": mr["title"], "web_url": mr["web_url"], "head_sha": head,
		"scannedDiscussions": len(all), "linkedDiscussions": linked, "totalLinkedDiscussions": totalLinks, "linkedDiscussionsTruncated": totalLinks > len(linked),
		"commitReferences": commitReferences, "totalCommitReferences": totalCommits, "commitReferencesTruncated": totalCommits > len(commitReferences),
		"scanComplete": complete, "partial": !complete || len(errors) > 0, "errors": errors, "unresolvedCount": nil, "resolvedCount": nil,
		"offset": offset, "nextOffset": next, "includeResolved": includeResolved, "items": resultItems}
	if complete {
		res["unresolvedCount"] = len(unresolved)
		res["resolvedCount"] = len(resolved)
		res["note"] = "older_diff_version не означает исправленное замечание"
	} else {
		res["note"] = "Обход обсуждений неполон; счётчики по всем обсуждениям неизвестны"
	}
	return res, nil
}
func humanDiscussion(d map[string]any) bool { return human(d) }
func (s *Service) Discussion(ctx context.Context, reference string, refresh bool) (any, error) {
	parsed, e := refs.ParseDiscussion(reference, s.API.Cfg.GitlabURL)
	if e != nil {
		return nil, e
	}
	short := strings.Split(parsed.Reference, "#")[0]
	iid, project, base, e := s.mrLocation(ctx, short)
	if e != nil {
		return nil, e
	}
	if refresh {
		s.Notes.ClearMR(project, iid)
	}
	if cached := s.Notes.Get(project, iid, parsed.NoteID); cached != "" && !refresh {
		d, e := doc(ctx, s.API.Gitlab, base+"/discussions/"+cached, false)
		if e == nil && refs.NoteIn(d, parsed.NoteID) {
			s.Notes.Remember(project, iid, []map[string]any{d})
			return map[string]any{"reference": parsed.Reference, "project": project, "iid": iid, "noteId": parsed.NoteID, "discussionId": cached, "found": true, "partial": false, "errors": []api.Failure{}, "lookup": "cache", "discussion": s.ViewDiscussion(d)}, nil
		}
		if e != nil {
			if httpErr, ok := e.(*api.HTTPError); !ok || httpErr.Status != 404 {
				return nil, e
			}
		}
		s.Notes.Delete(project, iid, parsed.NoteID)
	}
	scanned := 0
	errors := []api.Failure{}
	for page, i := 1, 0; i < 20; i++ {
		p, e := s.API.GLPage(ctx, base+"/discussions", page, 100, refresh)
		if e != nil {
			errors = append(errors, errMap("discussions", e))
			break
		}
		scanned += len(p.Items)
		s.Notes.Remember(project, iid, p.Items)
		for _, d := range p.Items {
			if refs.NoteIn(d, parsed.NoteID) {
				return map[string]any{"reference": parsed.Reference, "project": project, "iid": iid, "noteId": parsed.NoteID, "discussionId": d["id"], "found": true, "partial": false, "errors": []api.Failure{}, "lookup": "scan", "scannedDiscussions": scanned, "discussion": s.ViewDiscussion(d)}, nil
			}
		}
		if p.Next == 0 {
			return nil, fmt.Errorf("комментарий note_%d не найден в MR %s (просмотрено %d обсуждений)", parsed.NoteID, short, scanned)
		}
		page = p.Next
	}
	return map[string]any{"reference": parsed.Reference, "project": project, "iid": iid, "noteId": parsed.NoteID, "discussionId": nil, "found": false, "partial": true, "errors": errors, "scannedDiscussions": scanned, "scanComplete": false, "discussion": nil, "note": "Поиск комментария неполон: не считать его отсутствующим"}, nil
}
