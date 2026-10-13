package domain

import (
	"fmt"
	"net/url"
	"strings"
)

// RepositoryRef is the project and repository a git remote points at.
type RepositoryRef struct {
	Project    string
	Repository string
}

// ParseRemote parses an Azure DevOps Server remote URL of the form
// https://host/collection/{project}/_git/{repo} or ssh://host:22/collection/{project}/_git/{repo}.
func ParseRemote(remote string) (RepositoryRef, error) {
	raw := strings.TrimSpace(remote)
	if raw == "" {
		return RepositoryRef{}, fmt.Errorf("git remote URL is empty; cannot determine the repository")
	}
	// scp-like syntax (user@host:path) -> ssh://user@host/path
	if !strings.Contains(raw, "://") {
		if at := strings.Index(raw, ":"); at > 0 {
			raw = "ssh://" + raw[:at] + "/" + strings.TrimPrefix(raw[at+1:], "/")
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return RepositoryRef{}, fmt.Errorf("unsupported git remote URL %q (expected a URL containing /_git/)", remote)
	}
	segs := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
	gi := -1
	for i, s := range segs {
		if s == "_git" {
			gi = i
			break
		}
	}
	if gi <= 0 || gi+1 >= len(segs) {
		return RepositoryRef{}, fmt.Errorf("git remote %q is not an Azure DevOps repository URL (expected .../{project}/_git/{repo})", remote)
	}
	project, _ := url.PathUnescape(segs[gi-1])
	repo, _ := url.PathUnescape(segs[gi+1])
	repo = strings.TrimSuffix(repo, ".git")
	return RepositoryRef{Project: project, Repository: repo}, nil
}

// PullRequestCreateURL builds the web URL that opens the "create pull request" page.
func PullRequestCreateURL(collection string, ref RepositoryRef, source, target, repositoryID string) string {
	escRef := func(s string) string {
		return strings.NewReplacer("+", "%20", "%2F", "/").Replace(url.QueryEscape(s))
	}
	return fmt.Sprintf("%s/%s/_git/%s/pullrequestcreate?sourceRef=%s&targetRef=%s&sourceRepositoryId=%s&targetRepositoryId=%s",
		strings.TrimRight(collection, "/"),
		url.PathEscape(ref.Project), url.PathEscape(ref.Repository),
		escRef(source), escRef(target), repositoryID, repositoryID)
}
