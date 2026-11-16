package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ville6000/tfs/internal/ado"
	"github.com/ville6000/tfs/internal/domain"
)

// prServer serves pull request !5, into main where squash and rebase merges are allowed, and
// records the votes and changes it is asked for. autoComplete starts !5 with auto-complete on;
// failPolicy answers the policy lookup with an HTTP status.
type prServer struct {
	autoComplete bool
	deleteBranch bool // !5's own completion options
	failPolicy   int
	failPatch    int

	mu      sync.Mutex
	calls   []string
	vote    int
	options map[string]any // as last set
}

func (s *prServer) pr() domain.PullRequest {
	return domain.PullRequest{ID: 5, Title: "Fix login", Project: "P", Repository: "web", RepositoryID: "repo-1",
		Status: "active", SourceBranch: "feature/5-fix", TargetBranch: "main", CreatedBy: "Bob"}
}

func (s *prServer) start(t *testing.T) *Actions {
	t.Helper()
	if s.options == nil {
		s.options = map[string]any{"deleteSourceBranch": s.deleteBranch}
	}
	// pr is !5 as the server has it; the caller holds s.mu.
	pr := func() map[string]any {
		p := map[string]any{"pullRequestId": 5, "status": "active", "title": "Fix login",
			"sourceRefName": "refs/heads/feature/5-fix", "targetRefName": "refs/heads/main",
			"createdBy":         map[string]string{"displayName": "Bob"},
			"repository":        map[string]any{"id": "repo-1", "name": "web", "project": map[string]string{"name": "P"}},
			"reviewers":         []any{map[string]any{"id": "me-id", "displayName": "Me", "vote": s.vote}},
			"completionOptions": s.options}
		if s.autoComplete {
			p["autoCompleteSetBy"] = map[string]string{"id": "me-id", "displayName": "Me"}
		}
		return p
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		body, _ := io.ReadAll(r.Body)
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/_apis/connectionData"):
			reply(map[string]any{"authenticatedUser": map[string]string{"id": "me-id", "providerDisplayName": "Me"}})
		case r.Method == http.MethodGet && strings.HasSuffix(p, "/_apis/git/pullrequests/5"):
			s.mu.Lock()
			defer s.mu.Unlock()
			reply(pr())
		case strings.HasSuffix(p, "/P/_apis/git/policy/configurations"):
			if s.failPolicy != 0 {
				w.WriteHeader(s.failPolicy)
				reply(map[string]string{"message": "no"})
				return
			}
			reply(map[string]any{"value": []any{map[string]any{"isEnabled": true, "type": map[string]string{"id": "fa4e907d-c16b-4a4c-9dfa-4916e5d171ab"},
				"settings": map[string]any{"allowSquash": true, "allowRebase": true, "allowNoFastForward": false}}}})
		case r.Method == http.MethodPut && strings.HasSuffix(p, "/P/_apis/git/repositories/repo-1/pullRequests/5/reviewers/me-id"):
			s.record("vote " + canonicalBody(body))
			s.mu.Lock()
			s.vote = 10
			s.mu.Unlock()
			reply(map[string]any{"id": "me-id", "vote": 10})
		case r.Method == http.MethodPatch && strings.HasSuffix(p, "/P/_apis/git/repositories/repo-1/pullRequests/5"):
			s.record("patch " + canonicalBody(body))
			if s.failPatch != 0 {
				w.WriteHeader(s.failPatch)
				reply(map[string]string{"message": "refused"})
				return
			}
			var b struct {
				AutoCompleteSetBy struct{ ID string }
				CompletionOptions map[string]any
			}
			_ = json.Unmarshal(body, &b)
			s.mu.Lock()
			defer s.mu.Unlock()
			s.autoComplete = b.AutoCompleteSetBy.ID == "me-id"
			if b.CompletionOptions != nil {
				s.options = b.CompletionOptions
			}
			reply(pr())
		default:
			http.Error(w, "unexpected "+r.Method+" "+p, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return &Actions{Client: ado.New(srv.URL+"/Col", "7.0", "x")}
}

func (s *prServer) record(call string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call)
}

func (s *prServer) check(t *testing.T, want ...string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls)+len(want) > 0 && !reflect.DeepEqual(s.calls, want) {
		t.Errorf("calls:\n got %q\nwant %q", s.calls, want)
	}
}

func canonicalBody(b []byte) string {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return string(b)
	}
	out, _ := json.Marshal(v)
	return string(out)
}

// prOutcome runs cmd and returns its status and the pull requests it announced as changed.
func prOutcome(cmd tea.Cmd) (StatusMsg, []domain.PullRequest) {
	var status StatusMsg
	var changed []domain.PullRequest
	for _, msg := range collect(cmd) {
		switch m := msg.(type) {
		case StatusMsg:
			status = m
		case pullRequestChangedMsg:
			changed = append(changed, m.pr)
		}
	}
	return status, changed
}

const approveVote = `vote {"vote":10}`

// a on a pull request asks, then approves it and announces it as read again.
func TestApprovePullRequestKey(t *testing.T) {
	s := &prServer{}
	act := s.start(t)
	d := NewPullRequestDetail(act, s.pr())
	d.SetSize(100, 30)
	_, cmd := d.Update(press("a"))
	q := screenOf[*Confirm](t, cmd)
	if v := ansi.Strip(q.View()); !strings.Contains(v, "Approve !5?") || strings.Contains(v, "Comment") {
		t.Errorf("question:\n%s", v)
	}
	_, cmd = q.Update(press("ctrl+s"))
	status, changed := prOutcome(cmd)
	if status.Text != "approved !5" || status.Err {
		t.Errorf("status %+v", status)
	}
	if len(changed) != 1 || changed[0].ID != 5 || changed[0].Repository != "web" {
		t.Errorf("changed %+v", changed)
	}
	s.check(t, approveVote)

	// esc approves nothing.
	s.calls = nil
	_, cmd = d.Update(press("a"))
	q = screenOf[*Confirm](t, cmd)
	_, cmd = q.Update(press("esc"))
	runAll(cmd)
	s.check(t)

	// A closed pull request is not offered.
	closed := s.pr()
	closed.Status = "abandoned"
	_, cmd = NewPullRequestDetail(act, closed).Update(press("a"))
	if st, ok := run(cmd).(StatusMsg); !ok || !st.Err || !strings.Contains(st.Text, "is abandoned") {
		t.Errorf("closed: %#v", run(cmd))
	}
}

// A opens the auto-complete form with the merge types main allows, starting from the pull
// request's options; submitting approves, then sets auto-complete.
func TestApproveAndAutoCompleteForm(t *testing.T) {
	s := &prServer{deleteBranch: true}
	act := s.start(t)
	l := NewPullRequestList("prs", act, nil)
	l.SetSize(120, 10)
	l.Update(pullRequestsLoadedMsg{screen: l.id, prs: []domain.PullRequest{s.pr()}})
	_, cmd := l.Update(press("A"))
	f := screenOf[*AutoCompleteForm](t, cmd)
	view := ansi.Strip(f.View())
	for _, want := range []string{"!5 Fix login", "web · feature/5-fix → main", "Squash commit", "Rebase and fast-forward",
		"Those the policies of main allow", "Delete feature/5-fix", "Approve !5 and set it to auto-complete?"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q in:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Merge (no fast-forward)") {
		t.Errorf("offers a merge type main does not allow:\n%s", view)
	}
	if f.strategy != domain.MergeSquash || !slices.Equal(f.after, []string{afterDeleteBranch}) {
		t.Errorf("starts with %q %v", f.strategy, f.after)
	}
	f.Update(press("j")) // rebase
	_, cmd = f.Update(press("ctrl+s"))
	status, changed := prOutcome(cmd)
	if status.Text != "approved !5 and set it to auto-complete: rebase · delete source branch" || status.Err {
		t.Errorf("status %+v", status)
	}
	s.check(t, approveVote,
		`patch {"autoCompleteSetBy":{"id":"me-id"},"completionOptions":{"deleteSourceBranch":true,"mergeStrategy":"rebase","transitionWorkItems":false}}`)

	// The list shows the approval; a detail of the pull request shows auto-complete.
	if len(changed) != 1 || !changed[0].HasAutoComplete() {
		t.Fatalf("changed %+v", changed)
	}
	if v := ansi.Strip(l.View()); strings.Contains(v, "approved") {
		t.Errorf("approved before the change is announced:\n%s", v)
	}
	l.Update(pullRequestChangedMsg{changed[0]})
	if v := ansi.Strip(l.View()); !strings.Contains(v, "approved") {
		t.Errorf("list:\n%s", v)
	}
	d := NewPullRequestDetail(act, s.pr())
	d.SetSize(100, 30)
	d.Update(pullRequestChangedMsg{changed[0]})
	if v := ansi.Strip(d.View()); !strings.Contains(v, "auto-complete: rebase · delete source branch  ·  set by Me") {
		t.Errorf("detail:\n%s", v)
	}
}

// m sets auto-complete without approving, and turns it off when it is on.
func TestAutoCompleteKey(t *testing.T) {
	s := &prServer{}
	act := s.start(t)
	d := NewPullRequestDetail(act, s.pr())
	d.SetSize(100, 30)

	_, cmd := d.Update(press("m"))
	f := screenOf[*AutoCompleteForm](t, cmd)
	if v := ansi.Strip(f.View()); !strings.Contains(v, "Set !5 to auto-complete?") {
		t.Errorf("form:\n%s", v)
	}
	_, cmd = f.Update(press("ctrl+s"))
	status, _ := prOutcome(cmd)
	if status.Text != "set !5 to auto-complete: squash" {
		t.Errorf("status %+v", status)
	}
	s.check(t, `patch {"autoCompleteSetBy":{"id":"me-id"},"completionOptions":{"deleteSourceBranch":false,"mergeStrategy":"squash","squashMerge":true,"transitionWorkItems":false}}`)

	// Now that it is on, m offers to turn it off.
	s.calls = nil
	_, cmd = d.Update(press("m"))
	q := screenOf[*Confirm](t, cmd)
	if v := ansi.Strip(q.View()); !strings.Contains(v, "Turn off auto-complete of !5?") || !strings.Contains(v, "auto-complete: squash · set by Me") {
		t.Errorf("question:\n%s", v)
	}
	_, cmd = q.Update(press("ctrl+s"))
	status, changed := prOutcome(cmd)
	if status.Text != "turned off auto-complete of !5" || len(changed) != 1 || changed[0].HasAutoComplete() {
		t.Errorf("status %+v, changed %+v", status, changed)
	}
	s.check(t, `patch {"autoCompleteSetBy":{"id":"00000000-0000-0000-0000-000000000000"}}`)
}

// Without the policies, the form offers every merge type and says why; a refused change says so
// and still announces the approval.
func TestAutoCompleteFormFallbacks(t *testing.T) {
	s := &prServer{failPolicy: http.StatusForbidden, failPatch: http.StatusForbidden}
	act := s.start(t)
	_, cmd := NewPullRequestDetail(act, s.pr()).Update(press("A"))
	f := screenOf[*AutoCompleteForm](t, cmd)
	view := ansi.Strip(f.View())
	if !strings.Contains(view, "Could not read the merge types main allows") || !strings.Contains(view, "Merge (no fast-forward)") {
		t.Errorf("form:\n%s", view)
	}
	if f.strategy != domain.MergeNoFastForward {
		t.Errorf("starts with %q", f.strategy)
	}
	_, cmd = f.Update(press("ctrl+s"))
	status, changed := prOutcome(cmd)
	if !status.Err || !strings.HasPrefix(status.Text, "approved !5, but could not set auto-complete: not allowed to set auto-complete of !5") {
		t.Errorf("status %+v", status)
	}
	if len(changed) != 1 {
		t.Errorf("the approval is announced: %+v", changed)
	}

	// esc changes nothing.
	s.calls = nil
	_, cmd = NewPullRequestDetail(act, s.pr()).Update(press("m"))
	f = screenOf[*AutoCompleteForm](t, cmd)
	_, cmd = f.Update(press("esc"))
	runAll(cmd)
	s.check(t)
}

// On the home screen a review request can be approved; the user's own pull request can be set to
// auto-complete but is not offered to approve. A change reloads the pull requests.
func TestHomePullRequestReviewKeys(t *testing.T) {
	s := &prServer{}
	act := s.start(t)
	h := NewHome("P", act, &Builds{}, &Releases{}, HomeSources{})
	h.SetSize(120, 40)
	review := s.pr()
	own := s.pr()
	own.ID = 6
	h.feed(homeLoadedMsg{source: sourcePullRequests, prs: domain.MyPullRequests{
		ToReview: []domain.ReviewRequest{{PullRequest: review}}, Created: []domain.PullRequest{own},
	}})
	if keys := keyNames(h); !strings.Contains(keys, "a A m") {
		t.Errorf("review request keys: %s", keys)
	}
	_, cmd := h.Update(press("a"))
	q := screenOf[*Confirm](t, cmd)
	_, cmd = q.Update(press("ctrl+s"))
	runAll(cmd)
	s.check(t, approveVote)

	h.Update(press("tab"))
	if keys := keyNames(h); strings.Contains(keys, "a A") || !strings.Contains(keys, "m") {
		t.Errorf("own pull request keys: %s", keys)
	}
	if _, cmd := h.Update(press("a")); cmd != nil {
		t.Error("a must not approve your own pull request from the home screen")
	}
	if _, cmd := h.Update(press("m")); cmd == nil {
		t.Error("m should set your own pull request to auto-complete")
	}
	if _, cmd := h.Update(pullRequestChangedMsg{review}); cmd == nil {
		t.Error("a changed pull request should reload the home screen's pull requests")
	}
}
