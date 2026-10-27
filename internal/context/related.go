package projectcontext

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"redmine-gitlab-mcp/internal/api"
	"redmine-gitlab-mcp/internal/refs"
)

func (s *Service) FindRelated(ctx context.Context, issueID int, project string, refresh bool) (any, error) {
	issue, ie := safeSection("redmine_issue", func() (map[string]any, error) {
		return doc(ctx, s.API.Redmine, fmt.Sprintf("issues/%d.json?include=journals,relations", issueID), refresh)
	})
	i := api.Map(issue["issue"])
	text := api.Str(i["description"])
	for _, j := range api.Objects(i["journals"]) {
		text += "\n" + api.Str(j["notes"])
	}
	direct := apiDirect(s.API.Cfg.GitlabURL, text)
	names := []string{}
	if project != "" {
		names = append(names, project)
	} else {
		names = append(names, s.API.Cfg.GitlabProject)
		keys := []string{}
		for k := range s.API.Cfg.GitlabProjects {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			names = append(names, s.API.Cfg.GitlabProjects[k])
		}
	}
	projects := []string{}
	seen := map[string]bool{}
	for _, name := range names {
		k := strings.ToLower(name)
		if !seen[k] && len(projects) < 12 {
			projects = append(projects, k)
			seen[k] = true
		}
	}
	errors := failures(ie)
	complete := len(names) <= 12
	found := []any{}
	seenMR := map[string]bool{}
	for _, name := range projects {
		id, e := s.API.Project(ctx, name)
		if e != nil {
			errors = append(errors, errMap("project_resolution", e))
			complete = false
			continue
		}
		page, ended := 1, false
		for n := 0; n < 3; n++ {
			p, e := s.API.GLPage(ctx, fmt.Sprintf("api/v4/projects/%s/search?scope=merge_requests&search=%s", id, url.QueryEscape(fmt.Sprintf("%d", issueID))), page, 100, refresh)
			if e != nil {
				errors = append(errors, errMap("mr_search", e))
				break
			}
			for _, mr := range p.Items {
				evidence := issueStrength(api.Str(mr["title"])+"\n"+api.Str(mr["description"]), issueID)
				iid := intFrom(mr["iid"])
				if evidence == "" || iid == 0 {
					continue
				}
				key := fmt.Sprintf("%s:%d", id, iid)
				if seenMR[key] {
					continue
				}
				seenMR[key] = true
				found = append(found, map[string]any{"reference": fmt.Sprintf("%s!%d", name, iid), "project": id, "iid": iid, "title": mr["title"], "web_url": mr["web_url"], "state": mr["state"], "updated_at": mr["updated_at"], "evidence": evidence})
			}
			if p.Next == 0 {
				ended = true
				break
			}
			page = p.Next
		}
		if !ended {
			complete = false
		}
	}
	return map[string]any{"issueId": issueID, "searchedProjects": projects, "directReferences": direct, "matches": found, "partial": !complete || len(errors) > 0, "searchComplete": complete && len(errors) == 0, "errors": errors, "note": "Совпадения поиска не доказывают связь. Список может быть неполным из-за индексации GitLab и ограничения поиска."}, nil
}
func apiDirect(base, text string) []string { return refs.DirectMRURLs(text, base) }
func intFrom(v any) int                    { return refs.Int(v) }
