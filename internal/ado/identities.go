package ado

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
)

// descriptor is an identity descriptor in the lower-cased "type;identifier" form. The server
// sends descriptors as such strings, though its schema describes an object with the two parts,
// so both are accepted.
type descriptor string

func (d *descriptor) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*d = descriptor(strings.ToLower(s))
		return nil
	}
	var o struct {
		IdentityType string `json:"identityType"`
		Identifier   string `json:"identifier"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return err
	}
	*d = ""
	if o.IdentityType != "" || o.Identifier != "" {
		*d = descriptor(strings.ToLower(o.IdentityType + ";" + o.Identifier))
	}
	return nil
}

type identityResponse struct {
	ID         string       `json:"id"`
	Descriptor descriptor   `json:"descriptor"`
	MemberOf   []descriptor `json:"memberOf"`
}

// identityChunk is how many identities one lookup asks for, keeping the query string well
// below the 2048 bytes IIS allows by default.
const identityChunk = 40

func (c *Client) readIdentities(ctx context.Context, ids []string, membership string) ([]identityResponse, error) {
	q := url.Values{"identityIds": {strings.Join(ids, ",")}}
	if membership != "" {
		q.Set("queryMembership", membership)
	}
	var payload struct {
		Value []*identityResponse `json:"value"`
	}
	if err := c.get(ctx, c.url("/_apis/identities", q), &payload); err != nil {
		return nil, err
	}
	out := make([]identityResponse, 0, len(payload.Value))
	for _, r := range payload.Value {
		if r != nil && r.ID != "" {
			out = append(out, *r)
		}
	}
	return out, nil
}

// MemberGroups returns the lower-cased ids among ids of the groups the authenticated user belongs
// to, directly or through other groups. Reading memberships needs the Identity "Read" scope.
// Answers are cached, so repeated calls only look up identities not seen before.
func (c *Client) MemberGroups(ctx context.Context, ids []string) (map[string]bool, error) {
	who, err := c.whoAmI(ctx)
	if err != nil {
		return nil, err
	}
	if who.ID == "" {
		return nil, errors.New("server did not return an id for the authenticated user")
	}
	c.groupsMu.Lock()
	defer c.groupsMu.Unlock()
	if c.memberOf == nil {
		me, err := c.readIdentities(ctx, []string{who.ID}, "expanded")
		if err != nil {
			return nil, err
		}
		memberOf := map[string]bool{}
		for _, r := range me {
			if strings.EqualFold(r.ID, who.ID) {
				for _, d := range r.MemberOf {
					memberOf[string(d)] = true
				}
			}
		}
		c.memberOf, c.inGroup = memberOf, map[string]bool{}
	}
	var unknown []string
	for _, id := range ids {
		id = strings.ToLower(strings.TrimSpace(id))
		if _, ok := c.inGroup[id]; !ok && id != "" && !slices.Contains(unknown, id) {
			unknown = append(unknown, id)
		}
	}
	for chunk := range slices.Chunk(unknown, identityChunk) {
		found, err := c.readIdentities(ctx, chunk, "")
		if err != nil {
			return nil, err
		}
		for _, id := range chunk {
			c.inGroup[id] = false
		}
		for _, r := range found {
			c.inGroup[strings.ToLower(r.ID)] = r.Descriptor != "" && c.memberOf[string(r.Descriptor)]
		}
	}
	out := map[string]bool{}
	for _, id := range ids {
		if id = strings.ToLower(strings.TrimSpace(id)); c.inGroup[id] {
			out[id] = true
		}
	}
	return out, nil
}
