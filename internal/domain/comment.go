package domain

import "time"

// Comment is a single discussion entry on a work item or pull request. Only ID, Author, Created
// and Text are always set; the rest depend on what the source API reports.
type Comment struct {
	ID      int       `json:"id"`
	Author  string    `json:"author"`
	Created time.Time `json:"created"`
	Text    string    `json:"text"`
	// ParentID is the comment this one replies to (pull request threads); 0 for a top-level comment.
	ParentID int `json:"parentId,omitempty"`
	// Type is the pull request comment type: text, codeChange or system.
	Type           string    `json:"type,omitempty"`
	Updated        time.Time `json:"updated,omitzero"`
	ContentUpdated time.Time `json:"contentUpdated,omitzero"`
	ModifiedBy     string    `json:"modifiedBy,omitempty"`
	Version        int       `json:"version,omitempty"`
	IsDeleted      bool      `json:"isDeleted,omitempty"`
	LikedBy        []string  `json:"likedBy,omitempty"`
	URL            string    `json:"url,omitempty"`
}

// CommentThread is a pull request discussion thread, optionally anchored to a file line.
type CommentThread struct {
	ID        int       `json:"id"`
	Status    string    `json:"status,omitempty"`
	FilePath  string    `json:"filePath,omitempty"`
	Line      int       `json:"line,omitempty"`
	Published time.Time `json:"published,omitzero"`
	Updated   time.Time `json:"updated,omitzero"`
	IsDeleted bool      `json:"isDeleted,omitempty"`
	Comments  []Comment `json:"comments"`
}

// VisibleThreads returns the threads worth showing a reader: deleted threads, deleted and
// system comments are dropped, and so are threads left without comments. The input is not modified.
func VisibleThreads(threads []CommentThread) []CommentThread {
	var out []CommentThread
	for _, t := range threads {
		if t.IsDeleted {
			continue
		}
		var comments []Comment
		for _, c := range t.Comments {
			if !c.IsDeleted && c.Type != "system" {
				comments = append(comments, c)
			}
		}
		if len(comments) > 0 {
			t.Comments = comments
			out = append(out, t)
		}
	}
	return out
}
