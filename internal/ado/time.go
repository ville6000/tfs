package ado

import (
	"bytes"
	"time"
)

// apiTime decodes the timestamps the server sends. Unset times arrive as null, "" or
// 0001-01-01T00:00:00 (without a zone), and some APIs omit the zone on real times too; all of
// these decode without error, unset ones to the zero time.
type apiTime struct{ time.Time }

var apiTimeLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"}

func (t *apiTime) UnmarshalJSON(b []byte) error {
	s := string(bytes.Trim(b, `"`))
	if s == "" || s == "null" {
		return nil
	}
	for _, layout := range apiTimeLayouts {
		if parsed, err := time.Parse(layout, s); err == nil {
			if parsed.Year() > 1 {
				t.Time = parsed
			}
			return nil
		}
	}
	return nil // an unreadable timestamp is not worth failing the whole response for
}
