package projectcontext

import (
	"context"
	"fmt"
	"redmine-gitlab-mcp/internal/version"
)

type Check struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Skipped bool   `json:"skipped,omitempty"`
	Details string `json:"details,omitempty"`
}

func (s *Service) Doctor(ctx context.Context) (map[string]any, error) {
	checks := []Check{}
	add := func(name string, e error) {
		if e != nil {
			checks = append(checks, Check{Name: name, Details: e.Error()})
		} else {
			checks = append(checks, Check{Name: name, OK: true})
		}
	}
	issue, e := doc(ctx, s.API.Redmine, "issues.json?limit=1", true)
	if e == nil {
		_, ok := issue["issues"].([]any)
		if !ok {
			e = fmt.Errorf("Redmine не вернул issues[]")
		}
	}
	add("Redmine issues", e)
	project, e := s.API.Project(ctx, "")
	add("GitLab project resolution", e)
	if e == nil {
		info, pErr := doc(ctx, s.API.Gitlab, fmt.Sprintf("api/v4/projects/%s", project), true)
		if pErr == nil && info["id"] == nil {
			pErr = fmt.Errorf("проект не вернул id")
		}
		add("GitLab project", pErr)
		base := fmt.Sprintf("api/v4/projects/%s", project)
		p, pe := s.API.GLPage(ctx, base+"/merge_requests?scope=all", 1, 1, true)
		add("GitLab merge requests", pe)
		if pe == nil && len(p.Items) > 0 {
			iid := intFrom(p.Items[0]["iid"])
			mrBase := fmt.Sprintf("%s/merge_requests/%d", base, iid)
			_, e := s.API.GLPage(ctx, mrBase+"/discussions", 1, 1, true)
			add("GitLab discussions", e)
			_, e = doc(ctx, s.API.Gitlab, mrBase+"/changes", true)
			add("GitLab changes", e)
		} else {
			checks = append(checks, Check{Name: "GitLab discussions/changes", Skipped: true, Details: "Нет доступных MR или нельзя получить список"})
		}
		_, e = s.API.GLPage(ctx, base+"/search?scope=merge_requests&search=doctor_probe", 1, 1, true)
		add("GitLab MR search", e)
	}
	success := true
	for _, c := range checks {
		if !c.OK && !c.Skipped {
			success = false
		}
	}
	return map[string]any{"version": version.Version, "ok": success, "defaultProject": s.API.Cfg.GitlabProject, "resolvedProjectId": project, "checks": checks}, nil
}
