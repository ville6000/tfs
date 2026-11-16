package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// approvalCall is one approval the fake server was asked to approve or reject.
type approvalCall struct {
	ID      int
	Status  string
	Comment string
}

// releaseServer serves the releases of two pipelines, web-cd and api-cd, with stages Test and
// Prod, and records the approvals it is asked to set. fail answers an approval's PATCH with an
// HTTP status instead.
//
//	web-10  Prod waits (110)       api-11  Prod waits (111)      web-12  Test waits (212)
//	web-13  Prod waits (113)       web-14  Prod waits (114)      web-15  Prod waits (115)
func releaseServer(t *testing.T, fail map[int]int) (*httptest.Server, func() []approvalCall) {
	t.Helper()
	stage := func(id, pipelineStage int, name string, rank, approval int) map[string]any {
		s := map[string]any{"id": id, "definitionEnvironmentId": pipelineStage, "name": name, "rank": rank, "status": "notStarted"}
		if approval != 0 {
			s["preDeployApprovals"] = []any{map[string]any{"id": approval, "status": "pending", "approvalType": "preDeploy",
				"approver": map[string]string{"displayName": "Release Managers"}}}
		}
		return s
	}
	// Both pipelines' stages have the same ids, so only the pipeline tells their releases apart.
	release := func(id int, pipeline string, test, prod int) map[string]any {
		pid := map[string]int{"web-cd": 1, "api-cd": 2}[pipeline]
		return map[string]any{
			"id": id, "name": strings.TrimSuffix(pipeline, "-cd") + "-" + strconv.Itoa(id), "status": "active",
			"releaseDefinition": map[string]any{"id": pid, "name": pipeline},
			"environments":      []any{stage(id*10+1, 101, "Test", 1, test), stage(id*10+2, 102, "Prod", 2, prod)},
		}
	}
	releases := []map[string]any{
		release(10, "web-cd", 0, 110), release(11, "api-cd", 0, 111), release(12, "web-cd", 212, 0),
		release(13, "web-cd", 0, 113), release(14, "web-cd", 0, 114), release(15, "web-cd", 0, 115),
	}

	var mu sync.Mutex
	var calls []approvalCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/_apis/connectionData"):
			reply(map[string]any{"authenticatedUser": map[string]string{"id": "me", "providerDisplayName": "Me"}})
		case r.Method == http.MethodGet && strings.HasSuffix(p, "/P/_apis/release/approvals"):
			reply(map[string]any{"value": []any{}}) // none of the approvals is assigned to the user by name
		case r.Method == http.MethodPatch && strings.Contains(p, "/P/_apis/release/approvals/"):
			id, _ := strconv.Atoi(path.Base(p))
			var body struct{ Status, Comments string }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			mu.Lock()
			calls = append(calls, approvalCall{id, body.Status, body.Comments})
			mu.Unlock()
			if code := fail[id]; code != 0 {
				w.WriteHeader(code)
				reply(map[string]string{"message": "approval " + strconv.Itoa(id) + " refused"})
				return
			}
			reply(map[string]any{"id": id, "status": body.Status})
		case r.Method == http.MethodGet && strings.HasSuffix(p, "/P/_apis/release/releases"):
			// Every release, as if the server ignored the pipeline filter: tfs must still only
			// touch the release's own pipeline.
			reply(map[string]any{"value": releases})
		case r.Method == http.MethodGet && strings.Contains(p, "/P/_apis/release/releases/"):
			id, _ := strconv.Atoi(path.Base(p))
			for _, rel := range releases {
				if rel["id"] == id {
					reply(rel)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
			reply(map[string]string{"message": "no release " + path.Base(p)})
		default:
			w.WriteHeader(http.StatusNotFound)
			reply(map[string]string{"message": "unexpected " + r.Method + " " + p})
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []approvalCall {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}
}

// `release approve` and `release reject` change only the approvals they should: never without
// --yes when not in a terminal, approving before rejecting the older releases waiting for the
// same stage, and only those.
func TestReleaseApproveAndReject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("TFS_PAT", "x")
	t.Setenv("TFS_ALLOW_INSECURE", "1")
	t.Chdir(t.TempDir())

	approved := func(id int, comment string) approvalCall { return approvalCall{id, "approved", comment} }
	rejected := func(id int, comment string) approvalCall { return approvalCall{id, "rejected", comment} }
	for _, c := range []struct {
		name string
		args string
		fail map[int]int
		want []approvalCall
		err  string // in the error; empty when the command succeeds
		out  string // in the output
	}{
		{name: "approve asks first", args: "approve 14 --stage Prod",
			err: "refusing to change a release without confirmation; pass --yes"},
		{name: "reject asks first", args: "reject 14 --stage Prod",
			err: "refusing to change a release without confirmation; pass --yes"},
		{name: "approve and reject older asks first", args: "approve 14 --stage Prod --reject-older",
			err: "refusing to change a release without confirmation; pass --yes"},

		{name: "approve", args: "approve 14 --stage Prod --yes -m ship",
			want: []approvalCall{approved(114, "ship")}, out: "Approved web-14 for Prod (web-cd)"},
		{name: "reject", args: "reject 14 --stage Prod --yes",
			want: []approvalCall{rejected(114, "")}, out: "Rejected web-14 for Prod (web-cd)"},
		// Not web-12 (waiting for Test), api-11 (another pipeline) or web-15 (newer).
		{name: "approve and reject older", args: "approve 14 --stage Prod --reject-older --yes -m hotfix",
			want: []approvalCall{approved(114, "hotfix"), rejected(110, "Superseded by web-14: hotfix"), rejected(113, "Superseded by web-14: hotfix")},
			out:  "Approved web-14 for Prod (web-cd)\nRejected web-10, web-13 as superseded"},
		{name: "nothing older to reject", args: "approve 10 --stage Prod --reject-older --yes",
			want: []approvalCall{approved(110, "")}, out: "Approved web-10 for Prod (web-cd)"},

		{name: "a failed rejection keeps the approval and tries the rest", args: "approve 14 --stage Prod --reject-older --yes",
			fail: map[int]int{110: http.StatusInternalServerError},
			want: []approvalCall{approved(114, ""), rejected(110, "Superseded by web-14"), rejected(113, "Superseded by web-14")},
			err:  "could not reject web-10", out: "Approved web-14 for Prod (web-cd)"},
		{name: "a failed approval rejects nothing", args: "approve 14 --stage Prod --reject-older --yes",
			fail: map[int]int{114: http.StatusForbidden},
			want: []approvalCall{approved(114, "")}, err: "not allowed to approve this stage"},

		{name: "unknown stage", args: "approve 14 --stage Staging --yes",
			err: `web-14 has no stage "Staging" (stages: Test, Prod)`},
		{name: "stage not waiting", args: "approve 12 --stage Prod --yes",
			err: "web-12 has no pending approval on Prod"},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv, calls := releaseServer(t, c.fail)
			for _, kv := range [][]string{{"collectionUrl", srv.URL + "/Col"}, {"defaultProject", "P"}} {
				if _, err := runCLI(t, "config", "set", kv[0], kv[1]); err != nil {
					t.Fatal(err)
				}
			}
			out, err := runCLI(t, append([]string{"release"}, strings.Fields(c.args)...)...)
			switch {
			case c.err == "" && err != nil:
				t.Errorf("error: %v", err)
			case c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)):
				t.Errorf("error %v, want %q", err, c.err)
			}
			if !strings.Contains(out, c.out) {
				t.Errorf("output %q, want %q", out, c.out)
			}
			if got := calls(); !reflect.DeepEqual(got, c.want) {
				t.Errorf("approvals set:\n got %v\nwant %v", got, c.want)
			}
		})
	}
}
