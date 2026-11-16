package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// reviewServer serves pull request !5 (active, into main) and !6 (completed), lets main be
// merged as allowSquash and allowRebase say, and records the votes and pull request changes it
// is asked for. autoComplete starts !5 with auto-complete on; the fail fields answer a vote, a
// change or the policy lookup with an HTTP status.
type reviewServer struct {
	autoComplete             bool
	completeWorkItems        bool // !5's own completion options
	allowSquash, allowRebase bool
	failVote, failPatch      int
	failPolicy               int

	srv   *httptest.Server
	mu    *sync.Mutex
	calls []string
}

func (s *reviewServer) start(t *testing.T) {
	s.mu = &sync.Mutex{}
	pr := func(id int, status string) map[string]any {
		p := map[string]any{"pullRequestId": id, "status": status, "title": "Fix login",
			"sourceRefName": "refs/heads/feature/5-fix", "targetRefName": "refs/heads/main",
			"createdBy":         map[string]string{"displayName": "Bob"},
			"repository":        map[string]any{"id": "repo-1", "name": "web", "project": map[string]string{"name": "P"}},
			"completionOptions": map[string]any{"transitionWorkItems": s.completeWorkItems}}
		if s.autoComplete {
			p["autoCompleteSetBy"] = map[string]string{"id": "bob-id", "displayName": "Bob"}
			p["completionOptions"] = map[string]any{"mergeStrategy": "squash", "deleteSourceBranch": true}
		}
		return p
	}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		body, _ := io.ReadAll(r.Body)
		record := func(call string, status int) bool {
			s.mu.Lock()
			s.calls = append(s.calls, call)
			s.mu.Unlock()
			if status != 0 {
				http.Error(w, `{"message":"no"}`, status)
				return false
			}
			return true
		}
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/_apis/connectionData"):
			reply(map[string]any{"authenticatedUser": map[string]string{"id": "me-id", "providerDisplayName": "Me"}})
		case r.Method == http.MethodGet && strings.HasSuffix(p, "/_apis/git/pullrequests/5"):
			reply(pr(5, "active"))
		case r.Method == http.MethodGet && strings.HasSuffix(p, "/_apis/git/pullrequests/6"):
			reply(pr(6, "completed"))
		case strings.HasSuffix(p, "/P/_apis/git/policy/configurations"):
			if s.failPolicy != 0 {
				http.Error(w, `{"message":"no"}`, s.failPolicy)
				return
			}
			reply(map[string]any{"value": []any{map[string]any{"isEnabled": true, "type": map[string]string{"id": "fa4e907d-c16b-4a4c-9dfa-4916e5d171ab"},
				"settings": map[string]any{"allowSquash": s.allowSquash, "allowRebase": s.allowRebase, "allowNoFastForward": false}}}})
		case r.Method == http.MethodPut && strings.HasSuffix(p, "/P/_apis/git/repositories/repo-1/pullRequests/5/reviewers/me-id"):
			if record("vote "+canonicalJSON(body), s.failVote) {
				reply(map[string]any{"id": "me-id", "vote": 10})
			}
		case r.Method == http.MethodPatch && strings.HasSuffix(p, "/P/_apis/git/repositories/repo-1/pullRequests/5"):
			if record("patch "+canonicalJSON(body), s.failPatch) {
				reply(pr(5, "active"))
			}
		default:
			http.Error(w, "unexpected "+r.Method+" "+p, http.StatusNotFound)
		}
	}))
	t.Cleanup(s.srv.Close)
}

func canonicalJSON(b []byte) string {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return string(b)
	}
	out, _ := json.Marshal(v)
	return string(out)
}

func TestPullRequestApproveAndAutoComplete(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("TFS_PAT", "x")
	t.Setenv("TFS_ALLOW_INSECURE", "1")
	t.Chdir(t.TempDir())

	vote := `vote {"vote":10}`
	set := func(options string) string {
		return `patch {"autoCompleteSetBy":{"id":"me-id"},"completionOptions":{` + options + `}}`
	}
	off := `patch {"autoCompleteSetBy":{"id":"00000000-0000-0000-0000-000000000000"}}`
	for _, c := range []struct {
		name   string
		args   string
		server reviewServer
		want   []string
		err    string // in the error; empty when the command succeeds
		out    string // in the output
		errOut string // in stderr
	}{
		{name: "approve asks first", args: "approve 5",
			err: "refusing to change a pull request without confirmation; pass --yes"},
		{name: "autocomplete asks first", args: "autocomplete 5",
			err: "refusing to change a pull request without confirmation; pass --yes"},
		{name: "approve", args: "approve 5 --yes",
			want: []string{vote}, out: "Approved !5: Fix login"},
		{name: "approve and set auto-complete", args: "approve 5 --yes --auto-complete --delete-source-branch",
			server: reviewServer{allowSquash: true},
			want:   []string{vote, set(`"deleteSourceBranch":true,"mergeStrategy":"squash","squashMerge":true,"transitionWorkItems":false`)},
			out:    "Approved !5: Fix login\nSet to auto-complete: squash · delete source branch"},
		{name: "merge options need --auto-complete", args: "approve 5 --yes --merge-strategy squash",
			err: "--merge-strategy requires --auto-complete"},
		{name: "a failed auto-complete keeps the approval", args: "approve 5 --yes --auto-complete",
			server: reviewServer{allowSquash: true, failPatch: http.StatusForbidden},
			want:   []string{vote, set(`"deleteSourceBranch":false,"mergeStrategy":"squash","squashMerge":true,"transitionWorkItems":false`)},
			err:    `approved !5, but could not set auto-complete: not allowed to set auto-complete of !5 (does the token have the Code "Read & write" scope?)`,
			out:    "Approved !5: Fix login"},
		{name: "a failed vote", args: "approve 5 --yes --auto-complete",
			server: reviewServer{allowSquash: true, failVote: http.StatusForbidden},
			want:   []string{vote}, err: "not allowed to vote on !5"},

		{name: "autocomplete keeps the pull request's options", args: "autocomplete 5 --yes",
			server: reviewServer{allowSquash: true, allowRebase: true, completeWorkItems: true},
			want:   []string{set(`"deleteSourceBranch":false,"mergeStrategy":"squash","squashMerge":true,"transitionWorkItems":true`)},
			out:    "Set !5 to auto-complete: squash · complete work items"},
		{name: "autocomplete with options", args: "autocomplete 5 --yes --merge-strategy rebase --complete-work-items=false",
			server: reviewServer{allowSquash: true, allowRebase: true, completeWorkItems: true},
			want:   []string{set(`"deleteSourceBranch":false,"mergeStrategy":"rebase","transitionWorkItems":false`)},
			out:    "Set !5 to auto-complete: rebase"},
		{name: "a merge type the policies do not allow", args: "autocomplete 5 --yes --merge-strategy rebase",
			server: reviewServer{allowSquash: true},
			err:    "the policies of main do not allow rebase (allowed: squash)"},
		{name: "an unknown merge type", args: "autocomplete 5 --yes --merge-strategy ff",
			err: `--merge-strategy: unknown merge strategy "ff"`},
		{name: "policies that cannot be read are not checked", args: "autocomplete 5 --yes --merge-strategy rebase",
			server: reviewServer{failPolicy: http.StatusForbidden},
			want:   []string{set(`"deleteSourceBranch":false,"mergeStrategy":"rebase","transitionWorkItems":false`)},
			out:    "Set !5 to auto-complete: rebase", errOut: "warning: could not read the merge types main allows"},

		{name: "turn off", args: "autocomplete 5 --off --yes", server: reviewServer{autoComplete: true},
			want: []string{off}, out: "Turned off auto-complete of !5: Fix login"},
		{name: "turn off when off", args: "autocomplete 5 --off --yes",
			out: "Auto-complete of !5 is already off"},
		{name: "options with --off", args: "autocomplete 5 --off --yes --delete-source-branch",
			err: "--delete-source-branch cannot be used with --off"},

		{name: "a completed pull request", args: "approve 6 --yes", err: "pull request !6 is completed"},
		{name: "a completed pull request's auto-complete", args: "autocomplete 6 --yes", err: "pull request !6 is completed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := &c.server
			s.start(t)
			for _, kv := range [][]string{{"collectionUrl", s.srv.URL + "/Col"}, {"defaultProject", "P"}} {
				if _, err := runCLI(t, "config", "set", kv[0], kv[1]); err != nil {
					t.Fatal(err)
				}
			}
			out, errOut, err := runCLIWith(t, nil, append([]string{"pr"}, strings.Fields(c.args)...)...)
			switch {
			case c.err == "" && err != nil:
				t.Errorf("error: %v", err)
			case c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)):
				t.Errorf("error %v, want %q", err, c.err)
			}
			if !strings.Contains(out, c.out) {
				t.Errorf("output %q, want %q", out, c.out)
			}
			if !strings.Contains(errOut, c.errOut) {
				t.Errorf("stderr %q, want %q", errOut, c.errOut)
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if !reflect.DeepEqual(s.calls, c.want) {
				t.Errorf("calls:\n got %q\nwant %q", s.calls, c.want)
			}
		})
	}
}

// --json prints the pull request as it is after the change.
func TestPullRequestApproveJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("TFS_PAT", "x")
	t.Setenv("TFS_ALLOW_INSECURE", "1")
	t.Chdir(t.TempDir())
	s := &reviewServer{autoComplete: true}
	s.start(t)
	for _, kv := range [][]string{{"collectionUrl", s.srv.URL + "/Col"}, {"defaultProject", "P"}} {
		if _, err := runCLI(t, "config", "set", kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	out, err := runCLI(t, "pr", "approve", "5", "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var pr map[string]any
	if err := json.Unmarshal([]byte(out), &pr); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if pr["id"] != float64(5) || pr["autoCompleteSetBy"] != "Bob" {
		t.Errorf("json: %s", out)
	}
	if opts, _ := pr["completionOptions"].(map[string]any); opts["mergeStrategy"] != "squash" || opts["deleteSourceBranch"] != true {
		t.Errorf("completion options: %s", out)
	}

	out, err = runCLI(t, "pr", "show", "5")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "auto-complete: squash · delete source branch  ·  set by Bob") {
		t.Errorf("show:\n%s", out)
	}
}
