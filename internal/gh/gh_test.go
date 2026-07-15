package gh

import "testing"

// parsePRs decodes the exact JSON shape `gh search prs --json …` emits, mapping
// the nested author/repository objects onto the flat PullRequest.
func TestParsePRs(t *testing.T) {
	data := []byte(`[
	  {"number":123,"title":"Add AI studio tab","url":"https://github.com/blend-ed/frontend-app-authoring/pull/123",
	   "isDraft":false,"author":{"login":"rabeeh-ta"},"repository":{"nameWithOwner":"blend-ed/frontend-app-authoring"}},
	  {"number":118,"title":"Fix avatar upload","url":"https://github.com/blend-ed/frontend-app-account/pull/118",
	   "isDraft":true,"author":{"login":"someone"},"repository":{"nameWithOwner":"blend-ed/frontend-app-account"}}
	]`)
	prs, err := parsePRs(data)
	if err != nil {
		t.Fatalf("parsePRs: %v", err)
	}
	if len(prs) != 2 {
		t.Fatalf("got %d PRs, want 2", len(prs))
	}
	p := prs[0]
	if p.Number != 123 || p.Title != "Add AI studio tab" || p.Author != "rabeeh-ta" ||
		p.RepoSlug != "blend-ed/frontend-app-authoring" || p.IsDraft {
		t.Errorf("PR[0] = %+v", p)
	}
	if !prs[1].IsDraft || prs[1].Author != "someone" {
		t.Errorf("PR[1] = %+v", prs[1])
	}
}

// An empty gh result ("[]" or "") is not an error — it means nothing pending.
func TestParsePRs_Empty(t *testing.T) {
	for _, in := range []string{"[]", "", "   \n"} {
		prs, err := parsePRs([]byte(in))
		if err != nil {
			t.Errorf("parsePRs(%q): unexpected error %v", in, err)
		}
		if len(prs) != 0 {
			t.Errorf("parsePRs(%q) = %d PRs, want 0", in, len(prs))
		}
	}
}

func TestParsePRs_Malformed(t *testing.T) {
	if _, err := parsePRs([]byte(`{not json`)); err == nil {
		t.Error("malformed JSON should return an error")
	}
}
