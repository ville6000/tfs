package domain

import "testing"

func TestVisibleThreads(t *testing.T) {
	text := Comment{ID: 1, Text: "looks good", Type: "text"}
	code := Comment{ID: 2, Text: "suggestion", Type: "codeChange"}
	system := Comment{ID: 3, Text: "voted", Type: "system"}
	deleted := Comment{ID: 4, Text: "gone", Type: "text", IsDeleted: true}
	threads := []CommentThread{
		{ID: 1, Comments: []Comment{text, system, deleted}},
		{ID: 2, Comments: []Comment{system}},
		{ID: 3, Comments: []Comment{deleted}},
		{ID: 4, IsDeleted: true, Comments: []Comment{text}},
		{ID: 5},
		{ID: 6, Comments: []Comment{code}},
	}
	got := VisibleThreads(threads)
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 6 {
		t.Fatalf("threads: %+v", got)
	}
	if len(got[0].Comments) != 1 || got[0].Comments[0].ID != 1 {
		t.Errorf("thread 1 comments: %+v", got[0].Comments)
	}
	if len(threads[0].Comments) != 3 {
		t.Errorf("input mutated: %+v", threads[0].Comments)
	}
}
