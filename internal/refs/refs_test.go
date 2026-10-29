package refs

import (
	"strings"
	"testing"
)

func TestParseMRReferenceURL(t *testing.T) {
	base := "https://git.example.com/gitlab"
	for _, input := range []string{
		"https://git.example.com/gitlab/dev/server/merge_requests/9999",
		"https://git.example.com/gitlab/dev/server/-/merge_requests/9999/",
		"https://git.example.com/gitlab/dev/server/-/merge_requests/9999/diffs?view=parallel#note_1",
	} {
		got, err := ParseMRReference(input, base)
		if err != nil || got != (MR{IID: 9999, Alias: "dev/server"}) {
			t.Errorf("%s: got %#v, %v", input, got, err)
		}
	}
	for _, input := range []string{
		"https://evil.example.com/gitlab/dev/server/merge_requests/9999",
		"http://git.example.com/gitlab/dev/server/merge_requests/9999",
		"https://user@git.example.com/gitlab/dev/server/merge_requests/9999",
		"https://git.example.com/gitlab-other/dev/server/merge_requests/9999",
		"https://git.example.com/gitlab/dev/../server/merge_requests/9999",
		"https://git.example.com/gitlab/dev/server/issues/9999",
	} {
		if _, err := ParseMRReference(input, base); err == nil {
			t.Errorf("accepted invalid URL %s", input)
		}
	}
}

func TestParseMR(t *testing.T) {
	for in, want := range map[string]MR{"!26": {26, ""}, "theme!26": {26, "theme"}, "dev/server!5642": {5642, "dev/server"}} {
		got, e := ParseMR(in)
		if e != nil || got != want {
			t.Fatalf("%s: %#v %v", in, got, e)
		}
	}
	for _, bad := range []string{"!0", "theme!NaN", "../a!26", "foo//bar!26"} {
		if _, e := ParseMR(bad); e == nil {
			t.Fatalf("expected invalid: %s", bad)
		}
	}
}
func TestDiscussionAndExtraction(t *testing.T) {
	base := "https://git.internal/gitlab"
	for _, in := range []string{"theme!5642#note_633455", "https://git.internal/gitlab/dev/theme/-/merge_requests/5642#note_633455", "https://git.internal/gitlab/dev/theme/merge_requests/5642#note_633455"} {
		l, e := ParseDiscussion(in, base)
		if e != nil || l.NoteID != 633455 {
			t.Fatalf("%s => %+v %v", in, l, e)
		}
	}
	for _, in := range []string{"https://evil.com/dev/theme/-/merge_requests/5642#note_633455", "https://git.internal/elsewhere/dev/theme/-/merge_requests/5642#note_633455"} {
		if _, e := ParseDiscussion(in, base); e == nil {
			t.Fatal("accepted foreign URL:", in)
		}
	}
	body := "Discussed in !5642#note_633455 and theme!9#note_7 (also https://git.internal/gitlab/dev/theme/-/merge_requests/2#note_300)"
	list := ExtractLinks(body, base, 30)
	if len(list) != 3 {
		t.Fatalf("expected 3 links; got %+v", list)
	}
	for _, l := range list {
		if strings.Contains(l.Reference, "http") {
			t.Fatalf("expected normalized link: %+v", l)
		}
	}
}
func TestIndexIsolation(t *testing.T) {
	n := NewNoteIndex()
	n.Remember("42", 26, []map[string]any{{"id": "discuss1", "notes": []any{map[string]any{"id": float64(633455)}}}})
	if n.Get("42", 26, 633455) != "discuss1" {
		t.Fatal("missing mapping")
	}
	if n.Get("43", 26, 633455) != "" || n.Get("42", 27, 633455) != "" {
		t.Fatal("index collision")
	}
	n.ClearMR("42", 26)
	if n.Get("42", 26, 633455) != "" {
		t.Fatal("clear failed")
	}
}
func TestSHAExtraction(t *testing.T) {
	v := ExtractSHAs("see commit a738f717 and issue 1524312; position deadbeef", 20)
	if len(v) != 2 || v[0] != "a738f717" {
		t.Fatalf("bad SHA: %v", v)
	}
	if _, e := ValidSHA("/etc/passwd"); e == nil {
		t.Fatal("invalid sha")
	}
}
