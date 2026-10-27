package projectcontext

import (
	"context"
	"fmt"
	"redmine-gitlab-mcp/internal/api"
	"redmine-gitlab-mcp/internal/refs"
	"regexp"
	"strings"
)

func (s *Service) Issue(ctx context.Context, id int, mode string, journalOffset, journalLimit int, refresh bool) (any, error) {
	if id <= 0 || journalOffset < 0 || journalLimit < 1 || journalLimit > 100 {
		return nil, fmt.Errorf("неверные параметры get_issue")
	}
	root, e := doc(ctx, s.API.Redmine, fmt.Sprintf("issues/%d.json?include=journals,attachments,relations,children", id), refresh)
	if e != nil {
		return nil, e
	}
	issue, ok := root["issue"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Redmine не вернул issue")
	}
	journals := api.Objects(issue["journals"])
	if journalOffset > len(journals) {
		return nil, fmt.Errorf("journalOffset больше числа записей")
	}
	selected := []map[string]any{}
	if mode == "summary" {
		for _, j := range journals {
			if strings.TrimSpace(api.Str(j["notes"])) != "" || len(api.Array(j["details"])) > 0 {
				selected = append(selected, j)
			}
		}
		if len(selected) > 5 {
			selected = selected[len(selected)-5:]
		}
	} else {
		end := min(len(journals), journalOffset+journalLimit)
		selected = journals[journalOffset:end]
	}
	entries := []any{}
	for _, j := range selected {
		entries = append(entries, readableJournal(j))
	}
	attachments := []any{}
	for _, a := range api.Objects(issue["attachments"]) {
		attachments = append(attachments, api.Pick(a, "id", "filename", "filesize", "content_type", "description", "content_url"))
	}
	result := api.Pick(issue, "id", "subject", "description", "project", "tracker", "status", "priority", "author", "assigned_to", "created_on", "updated_on", "custom_fields", "parent")
	result["url"] = strings.TrimRight(s.API.Cfg.RedmineURL, "/") + fmt.Sprintf("/issues/%d", id)
	result["children"] = api.Array(issue["children"])
	result["relations"] = api.Array(issue["relations"])
	result["attachments"] = attachments
	result["mode"] = mode
	result["journalCount"] = len(journals)
	result["journals"] = entries
	if mode == "summary" {
		result["journalOffset"] = nil
		result["nextJournalOffset"] = nil
		result["journalPreviewOnly"] = len(selected) < len(journals)
		result["note"] = "Показаны последние 5 содержательных записей. Для всей истории используйте mode=full и journalOffset."
	} else {
		result["journalOffset"] = journalOffset
		result["nextJournalOffset"] = nullableInt(0)
		next := journalOffset + journalLimit
		if next < len(journals) {
			result["nextJournalOffset"] = next
		} else {
			result["nextJournalOffset"] = nil
		}
		result["journalPreviewOnly"] = false
	}
	return result, nil
}

var attachmentExt = regexp.MustCompile(`(?i)\.(txt|md|json|xml|csv|tsv|yml|yaml|js|ts|pas|pp|sql|diff|patch|log|ini|cfg)$`)

func (s *Service) IssueAttachment(ctx context.Context, id int) (any, error) {
	if id <= 0 {
		return nil, fmt.Errorf("неверный attachment ID")
	}
	root, e := doc(ctx, s.API.Redmine, fmt.Sprintf("attachments/%d.json", id), false)
	if e != nil {
		return nil, e
	}
	a := api.Map(root["attachment"])
	if api.Str(a["content_url"]) == "" {
		return nil, fmt.Errorf("Redmine не вернул адрес вложения")
	}
	result := api.Pick(a, "id", "filename", "filesize", "content_type", "description")
	mime := strings.Split(strings.ToLower(api.Str(a["content_type"])), ";")[0]
	name := api.Str(a["filename"])
	isText := strings.HasPrefix(mime, "text/") || mime == "application/json" || mime == "application/xml" || mime == "application/javascript" || mime == "application/x-yaml" || mime == "application/yaml" || attachmentExt.MatchString(name)
	if !isText {
		result["readable"] = false
		result["note"] = "Бинарное вложение: извлечение текста не поддерживается."
		return result, nil
	}
	if refs.Int(a["filesize"]) > MaxFileBytes {
		result["readable"] = false
		result["note"] = "Файл превышает 512 КиБ."
		return result, nil
	}
	link, e := api.CheckRedmineAttachment(s.API.Cfg.RedmineURL, api.Str(a["content_url"]))
	if e != nil {
		return nil, e
	}
	data, e := s.API.Redmine.ExternalBytes(ctx, link, MaxFileBytes)
	if e != nil {
		return nil, e
	}
	part, e := Slice(string(data), 0, 64000)
	if e != nil {
		return nil, e
	}
	result["readable"] = true
	result["text"] = part["text"]
	result["truncated"] = part["nextOffset"] != nil
	return result, nil
}
