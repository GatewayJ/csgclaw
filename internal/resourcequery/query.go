// Package resourcequery owns deterministic resource selection independently of HTTP and storage.
package resourcequery

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
)

var ErrChanged = errors.New("resource list changed")
var ErrInvalid = errors.New("invalid resource query")

type Query struct {
	Mode     string   `json:"mode"`
	Page     int      `json:"page"`
	Size     int      `json:"size"`
	Search   string   `json:"search,omitempty"`
	Enabled  *bool    `json:"enabled,omitempty"`
	Cursor   string   `json:"cursor,omitempty"`
	Revision string   `json:"revision,omitempty"`
	Exclude  []string `json:"exclude,omitempty"`
}
type Entry struct {
	Name    string
	Enabled bool
}
type Metadata struct {
	Page       int    `json:"page,omitempty"`
	Per        int    `json:"per,omitempty"`
	Total      int    `json:"total"`
	Revision   string `json:"list_revision"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}
type cursor struct {
	After    string
	Revision string
}

func Match(name, query string) bool {
	text := []rune(strings.ToLower(name))
	offset := 0
	for _, ch := range strings.ToLower(query) {
		for offset < len(text) && text[offset] != ch {
			offset++
		}
		if offset == len(text) {
			return false
		}
		offset++
	}
	return true
}

func Select(entries []Entry, q Query, scope string) ([]string, Metadata, error) {
	if (q.Mode != "page" && q.Mode != "cursor") || q.Size < 1 || q.Size > 100 || (q.Mode == "page" && (q.Page < 1 || q.Cursor != "")) {
		return nil, Metadata{}, ErrInvalid
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if (q.Enabled == nil || *q.Enabled == e.Enabled) && Match(e.Name, q.Search) && !slices.Contains(q.Exclude, e.Name) {
			names = append(names, e.Name)
		}
	}
	slices.Sort(names)
	names = slices.Compact(names)
	identity, _ := json.Marshal(struct {
		Scope, Search  string
		Enabled        *bool
		Names, Exclude []string
	}{scope, q.Search, q.Enabled, names, q.Exclude})
	digest := sha256.Sum256(identity)
	revision := hex.EncodeToString(digest[:])
	meta := Metadata{Total: len(names), Revision: revision}
	if q.Revision != "" && q.Revision != revision {
		return nil, meta, ErrChanged
	}
	start := 0
	if q.Mode == "page" {
		meta.Page, meta.Per = q.Page, q.Size
		// Bound before multiplying to avoid integer overflow on untrusted page values.
		if q.Page-1 > len(names)/q.Size {
			start = len(names)
		} else {
			start = (q.Page - 1) * q.Size
		}
	} else if q.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)
		var c cursor
		if err != nil || json.Unmarshal(raw, &c) != nil || c.After == "" || c.Revision == "" {
			return nil, meta, ErrInvalid
		}
		if c.Revision != revision {
			return nil, meta, ErrChanged
		}
		start, _ = slices.BinarySearch(names, c.After)
		if start < len(names) && names[start] == c.After {
			start++
		}
	}
	end := min(start+q.Size, len(names))
	meta.HasMore = end < len(names)
	if q.Mode == "cursor" && meta.HasMore {
		raw, _ := json.Marshal(cursor{After: names[end-1], Revision: revision})
		meta.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return names[start:end], meta, nil
}
