// Package ado is a small client for the Azure DevOps Server REST API.
package ado

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Client talks to one Azure DevOps Server collection.
type Client struct {
	collection string // without trailing slash
	apiVersion string
	auth       string
	http       *http.Client
	// UserIdentity, when set, is what "@me" resolves to instead of asking the server.
	UserIdentity string

	meOnce sync.Once
	me     me
	meErr  error
}

// New creates a client. collection is the collection URL, e.g. https://host/DefaultCollection.
func New(collection, apiVersion, pat string) *Client {
	collection = strings.TrimRight(collection, "/")
	return &Client{
		collection: collection,
		apiVersion: apiVersion,
		auth:       "Basic " + base64.StdEncoding.EncodeToString([]byte(":"+pat)),
		http:       &http.Client{Timeout: 60 * time.Second, CheckRedirect: sameOrigin(collection)},
	}
}

// sameOrigin refuses redirects off the collection's scheme and host, so the token is never
// forwarded elsewhere (net/http keeps Authorization on a same-host https -> http redirect).
func sameOrigin(collection string) func(*http.Request, []*http.Request) error {
	base, _ := url.Parse(collection)
	return func(req *http.Request, via []*http.Request) error {
		if base == nil || !strings.EqualFold(req.URL.Scheme, base.Scheme) || !strings.EqualFold(req.URL.Host, base.Host) {
			return fmt.Errorf("refusing redirect to %s://%s: not the collection's server", req.URL.Scheme, req.URL.Host)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
}

// Collection returns the collection URL.
func (c *Client) Collection() string { return c.collection }

// ErrorKind classifies API failures.
type ErrorKind int

// Error kinds.
const (
	ErrOther ErrorKind = iota
	ErrUnauthorized
	ErrForbidden
	ErrNotFound
	ErrConflict
	ErrServer
)

// APIError is a non-2xx response from the server.
type APIError struct {
	Kind       ErrorKind
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	hint := ""
	switch e.Kind {
	case ErrUnauthorized:
		hint = " (check that the personal access token is valid and not expired)"
	case ErrForbidden:
		hint = " (the token lacks the required scope or you lack permission)"
	}
	return fmt.Sprintf("server returned %d: %s%s", e.StatusCode, e.Message, hint)
}

// IsKind reports whether err is an APIError of the given kind.
func IsKind(err error, kind ErrorKind) bool {
	var e *APIError
	return errors.As(err, &e) && e.Kind == kind
}

var credentialPattern = regexp.MustCompile(`(?i)(?:Basic|Bearer)\s+[A-Za-z0-9+/=._\-]+`)

func sanitize(body string) string {
	body = credentialPattern.ReplaceAllString(strings.TrimSpace(body), "[REDACTED]")
	if len(body) > 500 {
		body = body[:500] + "…"
	}
	return body
}

func newAPIError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var payload struct {
		Message string `json:"message"`
	}
	var msg string
	switch {
	case json.Unmarshal(raw, &payload) == nil && payload.Message != "":
		msg = payload.Message
	case strings.Contains(resp.Header.Get("Content-Type"), "html"):
		msg = http.StatusText(resp.StatusCode)
	default:
		msg = sanitize(string(raw))
	}
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	e := &APIError{StatusCode: resp.StatusCode, Message: sanitize(msg)}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		e.Kind = ErrUnauthorized
	case resp.StatusCode == http.StatusForbidden:
		e.Kind = ErrForbidden
	case resp.StatusCode == http.StatusNotFound:
		e.Kind = ErrNotFound
	case resp.StatusCode == http.StatusConflict:
		e.Kind = ErrConflict
	case resp.StatusCode >= 500:
		e.Kind = ErrServer
	}
	return e
}

// url builds an absolute API URL. path is appended to the collection (it may start with an
// escaped project segment); query values are added along with api-version unless overridden.
func (c *Client) url(path string, q url.Values) string {
	if q == nil {
		q = url.Values{}
	}
	if q.Get("api-version") == "" {
		q.Set("api-version", c.apiVersion)
	}
	return c.collection + path + "?" + encodeQuery(q)
}

// encodeQuery encodes like url.Values.Encode but keeps '$' and ',' readable, which the server
// accepts and which makes requests easier to debug.
func encodeQuery(q url.Values) string {
	return strings.NewReplacer("%24", "$", "%2C", ",").Replace(q.Encode())
}

func seg(s string) string { return url.PathEscape(s) }

func (c *Client) do(ctx context.Context, method, u, contentType string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(body); err != nil {
			return err
		}
		rd = &buf
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request to %s failed: %w", req.URL.Host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return newAPIError(resp)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		if strings.Contains(resp.Header.Get("Content-Type"), "html") {
			return fmt.Errorf("server returned an HTML page instead of JSON; check the collection URL and token")
		}
		return fmt.Errorf("could not decode server response: %w", err)
	}
	return nil
}

func (c *Client) get(ctx context.Context, u string, out any) error {
	return c.do(ctx, http.MethodGet, u, "", nil, out)
}

func (c *Client) postJSON(ctx context.Context, u string, body, out any) error {
	return c.do(ctx, http.MethodPost, u, "application/json", body, out)
}

const jsonPatch = "application/json-patch+json"

// CurrentUser returns the authenticated user's unique name (or mail / display name).
func (c *Client) CurrentUser(ctx context.Context) (string, error) {
	if c.UserIdentity != "" {
		return c.UserIdentity, nil
	}
	// Azure DevOps Server returns the account only under properties.Account, and the name as
	// providerDisplayName / customDisplayName.
	var payload struct {
		AuthenticatedUser struct {
			UniqueName          string `json:"uniqueName"`
			MailAddress         string `json:"mailAddress"`
			DisplayName         string `json:"displayName"`
			CustomDisplayName   string `json:"customDisplayName"`
			ProviderDisplayName string `json:"providerDisplayName"`
			Properties          struct {
				Account struct {
					Value string `json:"$value"`
				} `json:"Account"`
			} `json:"properties"`
		} `json:"authenticatedUser"`
	}
	if err := c.get(ctx, c.collection+"/_apis/connectionData", &payload); err != nil {
		return "", err
	}
	u := payload.AuthenticatedUser
	for _, v := range []string{u.UniqueName, u.Properties.Account.Value, u.MailAddress, u.DisplayName, u.CustomDisplayName, u.ProviderDisplayName} {
		if strings.TrimSpace(v) != "" {
			return v, nil
		}
	}
	return "", errors.New("server did not return an identity for the authenticated user")
}

// ResolveIdentity maps "@me" to the current user and returns other values unchanged.
func (c *Client) ResolveIdentity(ctx context.Context, identity string) (string, error) {
	if !IsMe(identity) {
		return strings.TrimSpace(identity), nil
	}
	return c.CurrentUser(ctx)
}

// IsMe reports whether the identity is the "@me" shorthand.
func IsMe(identity string) bool { return strings.EqualFold(strings.TrimSpace(identity), "@me") }

// BoardURL is the web URL of a project's boards.
func (c *Client) BoardURL(project string) string {
	return c.collection + "/" + seg(project) + "/_boards"
}

// WorkItemWebURL is the web URL of a work item.
func (c *Client) WorkItemWebURL(project string, id int) string {
	if project == "" {
		return fmt.Sprintf("%s/_workitems/edit/%d", c.collection, id)
	}
	return fmt.Sprintf("%s/%s/_workitems/edit/%d", c.collection, seg(project), id)
}
