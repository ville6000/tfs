package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ville6000/tfs/internal/ado"
	"github.com/ville6000/tfs/internal/domain"
)

// workItemServer serves the work item actions' requests in project P and records each write:
//
//	update #8 System.State=Resolved
//	comment #7 Looks good
//	create User Story: Write tests | parent #2 | assigned DOMAIN\me
//
// It refuses updates of the ids in fail.
func workItemServer(t *testing.T, fail ...int) (*Actions, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	record := func(s string) {
		mu.Lock()
		calls = append(calls, s)
		mu.Unlock()
	}
	type patchOp struct {
		Op, Path string
		Value    any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		item := func(id int, fields map[string]any) map[string]any {
			f := map[string]any{"System.Title": "Item", "System.WorkItemType": "Bug", "System.State": "Active", "System.TeamProject": "P"}
			for k, v := range fields {
				f[k] = v
			}
			return map[string]any{"id": id, "fields": f}
		}
		p := strings.ToLower(r.URL.Path)
		id, _ := strconv.Atoi(path.Base(p))
		switch {
		case strings.HasSuffix(p, "/_apis/connectiondata"):
			reply(map[string]any{"authenticatedUser": map[string]string{"uniqueName": `DOMAIN\me`}})
		case strings.HasSuffix(p, "/states"):
			reply(map[string]any{"value": []any{map[string]string{"name": "New"}, map[string]string{"name": "Active"},
				map[string]string{"name": "Resolved"}, map[string]string{"name": "Closed"}}})
		case strings.HasSuffix(p, "/p/_apis/wit/workitemtypecategories"):
			reply(map[string]any{"value": []any{
				map[string]any{"referenceName": domain.CategoryFeature, "defaultWorkItemType": map[string]string{"name": "Feature"},
					"workItemTypes": []any{map[string]string{"name": "Feature"}}},
				map[string]any{"referenceName": domain.CategoryRequirement, "defaultWorkItemType": map[string]string{"name": "User Story"},
					"workItemTypes": []any{map[string]string{"name": "User Story"}}},
				map[string]any{"referenceName": domain.CategoryTask, "defaultWorkItemType": map[string]string{"name": "Task"},
					"workItemTypes": []any{map[string]string{"name": "Task"}}},
			}})
		case r.Method == http.MethodPatch && strings.Contains(p, "/_apis/wit/workitems/"):
			var ops []patchOp
			_ = json.NewDecoder(r.Body).Decode(&ops)
			fields := map[string]any{}
			var set []string
			for _, op := range ops {
				f := strings.TrimPrefix(op.Path, "/fields/")
				fields[f] = op.Value
				set = append(set, f+"="+op.Value.(string))
			}
			record("update #" + strconv.Itoa(id) + " " + strings.Join(set, " "))
			if slices.Contains(fail, id) {
				w.WriteHeader(http.StatusBadRequest)
				reply(map[string]string{"message": "the state Resolved is not allowed"})
				return
			}
			reply(item(id, fields))
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/comments"):
			var body struct{ Text string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			id, _ := strconv.Atoi(path.Base(path.Dir(p)))
			record("comment #" + strconv.Itoa(id) + " " + body.Text)
			reply(map[string]any{"id": 1, "text": body.Text})
		case r.Method == http.MethodPost && strings.Contains(p, "/p/_apis/wit/workitems/$"):
			typ := strings.TrimPrefix(path.Base(r.URL.Path), "$")
			var ops []patchOp
			_ = json.NewDecoder(r.Body).Decode(&ops)
			parts := []string{"create " + typ + ":"}
			for _, op := range ops {
				switch op.Path {
				case "/fields/System.Title":
					parts[0] += " " + op.Value.(string)
				case "/fields/System.AssignedTo":
					parts = append(parts, "assigned "+op.Value.(string))
				case "/relations/-":
					rel := op.Value.(map[string]any)
					parts = append(parts, "parent #"+path.Base(rel["url"].(string)))
				}
			}
			record(strings.Join(parts, " | "))
			reply(item(99, map[string]any{"System.WorkItemType": typ, "System.Title": "Write tests"}))
		case r.Method == http.MethodGet && strings.Contains(p, "/_apis/wit/workitems/"):
			reply(item(id, nil))
		default:
			w.WriteHeader(http.StatusNotFound)
			reply(map[string]string{"message": "unexpected " + r.Method + " " + r.URL.Path})
		}
	}))
	t.Cleanup(srv.Close)
	return &Actions{Client: ado.New(srv.URL+"/Col", "7.0", "x")}, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(calls)
	}
}

// screenOf runs cmd and returns the screen it pushes, of type S.
func screenOf[S Screen](t *testing.T, cmd tea.Cmd) S {
	t.Helper()
	for _, msg := range collect(cmd) {
		if p, ok := msg.(PushMsg); ok {
			if s, ok := p.Screen.(S); ok {
				p.Screen.SetSize(100, 20)
				_ = p.Screen.Init() // focuses inputs; the cursor blink it starts is not needed
				return s
			}
		}
	}
	var zero S
	t.Fatalf("no %T pushed", zero)
	return zero
}

// outcome runs cmd and returns its status and the ids of the work items it announced as changed.
func outcome(cmd tea.Cmd) (StatusMsg, []int) {
	var status StatusMsg
	var changed []int
	for _, msg := range collect(cmd) {
		switch m := msg.(type) {
		case StatusMsg:
			status = m
		case workItemChangedMsg:
			changed = append(changed, m.item.ID)
		}
	}
	return status, changed
}

// setState answers the state choice cmd opens by moving down steps and selecting.
func setState(t *testing.T, cmd tea.Cmd, steps int) (StatusMsg, []int) {
	t.Helper()
	c := screenOf[*Choice](t, cmd)
	for range steps {
		c.Update(press("j"))
	}
	_, cmd = c.Update(press("enter"))
	return outcome(cmd)
}

// comment answers the comment input cmd opens with text.
func comment(t *testing.T, cmd tea.Cmd, text string) StatusMsg {
	t.Helper()
	in := screenOf[*Input](t, cmd)
	for _, r := range text {
		in.Update(press(string(r)))
	}
	_, cmd = in.Update(press("ctrl+s"))
	s, _ := outcome(cmd)
	return s
}

func checkCalls(t *testing.T, calls func() []string, want ...string) {
	t.Helper()
	if got := calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("writes:\n got %q\nwant %q", got, want)
	}
}

// The list's s, a and c change the selected work item, also after moving and filtering.
func TestWorkItemListActions(t *testing.T) {
	act, calls := workItemServer(t)
	l := NewWorkItemList("items", act, nil)
	l.SetSize(120, 10)
	l.Update(workItemsLoadedMsg{screen: l.id, items: []domain.WorkItem{
		{ID: 7, Type: "Bug", State: "Active", Title: "Fix login", Project: "P"},
		{ID: 8, Type: "Bug", State: "Active", Title: "Add reset", Project: "P"},
		{ID: 9, Type: "Bug", State: "Active", Title: "Remove old API", Project: "P"},
	}})

	l.Update(press("j"))
	_, cmd := l.Update(press("s"))
	s, changed := setState(t, cmd, 1) // from Active (current) to Resolved
	if s.Err || s.Text != `#8: Resolved · unassigned` || !reflect.DeepEqual(changed, []int{8}) {
		t.Errorf("state: %+v, changed %v", s, changed)
	}
	checkCalls(t, calls, "update #8 System.State=Resolved")

	l.Update(press("j"))
	_, cmd = l.Update(press("a"))
	if s, changed := outcome(cmd); s.Err || !reflect.DeepEqual(changed, []int{9}) {
		t.Errorf("assign: %+v, changed %v", s, changed)
	}
	checkCalls(t, calls, "update #8 System.State=Resolved", `update #9 System.AssignedTo=DOMAIN\me`)

	// Filtering leaves one match, last in the list, and selects it: the comment goes to it, not to
	// the first item.
	l.Update(press("g"))
	for _, k := range []string{"/", "o", "l", "d", "enter"} {
		l.Update(press(k))
	}
	_, cmd = l.Update(press("c"))
	if s := comment(t, cmd, "Looks good"); s.Err || s.Text != "comment added to #9" {
		t.Errorf("comment: %+v", s)
	}
	checkCalls(t, calls, "update #8 System.State=Resolved", `update #9 System.AssignedTo=DOMAIN\me`, "comment #9 Looks good")
}

// A refused update says why and changes nothing on screen.
func TestWorkItemUpdateRefused(t *testing.T) {
	act, calls := workItemServer(t, 8)
	s, changed := setState(t, act.changeState(domain.WorkItem{ID: 8, Type: "Bug", State: "Active", Project: "P"}), 1)
	if !s.Err || !strings.Contains(s.Text, "the state Resolved is not allowed") || len(changed) != 0 {
		t.Errorf("status %+v, changed %v", s, changed)
	}
	checkCalls(t, calls, "update #8 System.State=Resolved")
}

// The tree's i, s and a act on the selected item; a new child goes under it.
func TestTreeActions(t *testing.T) {
	act, calls := workItemServer(t)
	tv := NewTreeView("t", act, nil)
	tv.SetSize(100, 20)
	tree := domain.BuildForest([]domain.WorkItem{
		{ID: 1, Type: "Epic", Title: "E", Project: "P"},
		{ID: 2, Type: "Feature", Title: "F", ParentID: 1, Project: "P", State: "Active"},
		{ID: 3, Type: "Task", Title: "T", ParentID: 2, Project: "P"},
	}, domain.NewFilter(nil, nil, nil, nil))
	tv.Update(treeLoadedMsg{screen: tv.id, tree: tree})
	tv.Update(press("j")) // the Feature

	_, cmd := tv.Update(press("i"))
	form := screenOf[*CreateChild](t, cmd)
	if form.parent.ID != 2 || form.typ != "User Story" {
		t.Errorf("new child of #%d, type %q", form.parent.ID, form.typ)
	}
	_, cmd = tv.Update(press("s"))
	setState(t, cmd, 1)
	_, cmd = tv.Update(press("a"))
	outcome(cmd)
	checkCalls(t, calls, "update #2 System.State=Resolved", `update #2 System.AssignedTo=DOMAIN\me`)
}

// The new child form's action creates the item under its parent, assigned when asked.
func TestCreateChild(t *testing.T) {
	act, calls := workItemServer(t)
	parent := domain.WorkItem{ID: 2, Type: "Feature", Project: "P"}
	var created []int
	var status StatusMsg
	for _, msg := range collect(act.createChild(parent, "User Story", "Write tests", true)) {
		switch m := msg.(type) {
		case workItemCreatedMsg:
			if m.parentID != 2 || m.item.ParentID != 2 || m.item.Project != "P" {
				t.Errorf("created %+v under %d", m.item, m.parentID)
			}
			created = append(created, m.item.ID)
		case StatusMsg:
			status = m
		}
	}
	if status.Err || status.Text != "created User Story #99 under #2" || !reflect.DeepEqual(created, []int{99}) {
		t.Errorf("status %+v, created %v", status, created)
	}
	outcome(act.createChild(parent, "Task", "Write tests", false))
	checkCalls(t, calls, `create User Story: Write tests | assigned DOMAIN\me | parent #2`, "create Task: Write tests | parent #2")
}

// The detail screen's keys act on the item it shows.
func TestWorkItemDetailActions(t *testing.T) {
	act, calls := workItemServer(t)
	d := NewWorkItemDetail(act, 8)
	d.SetSize(100, 20)
	d.Update(detailLoadedMsg{screen: d.sid, detail: domain.WorkItemDetail{
		WorkItem: domain.WorkItem{ID: 8, Type: "Feature", State: "Active", Title: "Item", Project: "P"}}})

	_, cmd := d.Update(press("s"))
	setState(t, cmd, 1)
	_, cmd = d.Update(press("a"))
	outcome(cmd)
	_, cmd = d.Update(press("c"))
	comment(t, cmd, "ok")
	_, cmd = d.Update(press("i"))
	if form := screenOf[*CreateChild](t, cmd); form.parent.ID != 8 {
		t.Errorf("new child of #%d", form.parent.ID)
	}
	checkCalls(t, calls, "update #8 System.State=Resolved", `update #8 System.AssignedTo=DOMAIN\me`, "comment #8 ok")
}

// The home screen's work item rows act on the selected item.
func TestHomeWorkItemActions(t *testing.T) {
	act, calls := workItemServer(t)
	h := NewHome("P", act, &Builds{}, &Releases{}, HomeSources{})
	h.SetSize(120, 40)
	h.feed(homeLoadedMsg{source: sourceWorkItems, items: []domain.WorkItem{
		{ID: 7, Type: "User Story", State: "Active", Title: "A", Project: "P"},
		{ID: 8, Type: "User Story", State: "Active", Title: "B", Project: "P"},
	}})
	h.Update(press("j"))

	_, cmd := h.Update(press("s"))
	setState(t, cmd, 1)
	_, cmd = h.Update(press("c"))
	comment(t, cmd, "ok")
	checkCalls(t, calls, "update #8 System.State=Resolved", "comment #8 ok")
}
