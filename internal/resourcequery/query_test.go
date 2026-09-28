package resourcequery

import (
	"errors"
	"reflect"
	"testing"
)

func TestSelectionOrderAndCursor(t *testing.T) {
	entries := []Entry{{"delta", true}, {"beta", false}, {"alpha", true}, {"charlie", true}}
	first, meta, err := Select(entries, Query{Mode: "page", Page: 1, Size: 2}, "agent")
	if err != nil || !reflect.DeepEqual(first, []string{"alpha", "beta"}) || meta.Total != 4 {
		t.Fatalf("first: %v %+v %v", first, meta, err)
	}
	second, _, err := Select(entries, Query{Mode: "page", Page: 2, Size: 2, Revision: meta.Revision}, "agent")
	if err != nil || !reflect.DeepEqual(second, []string{"charlie", "delta"}) {
		t.Fatalf("second: %v %v", second, err)
	}
	enabled := true
	first, meta, err = Select(entries, Query{Mode: "cursor", Size: 2, Enabled: &enabled}, "agent")
	if err != nil || !reflect.DeepEqual(first, []string{"alpha", "charlie"}) || meta.NextCursor == "" {
		t.Fatalf("cursor first: %v %+v %v", first, meta, err)
	}
	second, last, err := Select(entries, Query{Mode: "cursor", Size: 2, Enabled: &enabled, Cursor: meta.NextCursor}, "agent")
	if err != nil || !reflect.DeepEqual(second, []string{"delta"}) || last.HasMore {
		t.Fatalf("cursor second: %v %+v %v", second, last, err)
	}
	_, _, err = Select(append(entries, Entry{"aardvark", true}), Query{Mode: "cursor", Size: 2, Enabled: &enabled, Cursor: meta.NextCursor}, "agent")
	if !errors.Is(err, ErrChanged) {
		t.Fatalf("changed: %v", err)
	}
	_, _, err = Select(entries, Query{Mode: "cursor", Size: 2, Cursor: "invalid"}, "agent")
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid: %v", err)
	}
}

func TestSearchAndBounds(t *testing.T) {
	entries := []Entry{{"git-commit", true}, {"git-review", true}, {"new", true}, {"创建智能体", true}}
	names, meta, err := Select(entries, Query{Mode: "page", Page: 1, Size: 10, Search: "GTc"}, "")
	if err != nil || !reflect.DeepEqual(names, []string{"git-commit"}) || meta.Total != 1 {
		t.Fatalf("search: %v %+v %v", names, meta, err)
	}
	names, _, err = Select(entries, Query{Mode: "page", Page: int(^uint(0) >> 1), Size: 100}, "")
	if err != nil || len(names) != 0 {
		t.Fatalf("overflow page: %v %v", names, err)
	}
	if !Match("创建智能体", "创体") {
		t.Fatal("unicode subsequence")
	}
	names, meta, err = Select(entries, Query{Mode: "cursor", Size: 2, Exclude: []string{"new", "创建智能体"}}, "")
	if err != nil || meta.HasMore || len(names) != 2 {
		t.Fatalf("exclude: %v %+v %v", names, meta, err)
	}
}
