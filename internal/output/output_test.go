package output

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ville6000/tfs/internal/domain"
)

func TestWorkItemsTablePlain(t *testing.T) {
	var buf bytes.Buffer
	WorkItems(&buf, []domain.WorkItem{{ID: 1, Type: "Bug", State: "New", Title: "A very long title indeed"}, {ID: 1234, Type: "User Story", State: "Active", AssignedTo: "Al", Title: "B"}}, 50)
	got := buf.String()
	want := "ID    TYPE        STATE   ASSIGNED  TITLE\n" +
		"1     Bug         New     —         A very long t…\n" +
		"1234  User Story  Active  Al        B\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTreePlain(t *testing.T) {
	tree := domain.BuildForest([]domain.WorkItem{
		{ID: 1, Type: "Epic", State: "New", Title: "E"},
		{ID: 2, Type: "Feature", State: "New", Title: "F", ParentID: 1},
		{ID: 3, Type: "Task", State: "New", Title: "T", ParentID: 2},
		{ID: 4, Type: "Task", State: "New", Title: "T2", ParentID: 1},
		{ID: 5, Type: "Bug", State: "New", Title: "B"},
	}, domain.NewFilter(nil, nil, nil, nil))
	var buf bytes.Buffer
	Tree(&buf, tree)
	want := strings.Join([]string{
		"#1 Epic [New] E",
		"├── #2 Feature [New] F",
		"│   └── #3 Task [New] T",
		"└── #4 Task [New] T2",
		"No parent",
		"└── #5 Bug [New] B",
		"",
	}, "\n")
	if buf.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestTreeAssignee(t *testing.T) {
	tree := domain.BuildForest([]domain.WorkItem{
		{ID: 1, Type: "Epic", State: "New", Title: "E", AssignedTo: "Alice Smith"},
		{ID: 2, Type: "Task", State: "New", Title: "T", ParentID: 1},
	}, domain.NewFilter(nil, nil, nil, nil))
	var buf bytes.Buffer
	Tree(&buf, tree)
	want := "#1 Epic [New] E · Alice Smith\n└── #2 Task [New] T\n"
	if buf.String() != want {
		t.Errorf("got:\n%q\nwant:\n%q", buf.String(), want)
	}
}
