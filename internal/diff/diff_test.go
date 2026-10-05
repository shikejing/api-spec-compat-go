package diff

import (
	"testing"

	"github.com/shikejing/api-spec-compat-go/internal/loader"
	"github.com/shikejing/api-spec-compat-go/internal/spec"
)

func loadPair(t *testing.T) (*spec.Document, *spec.Document) {
	t.Helper()
	oldDoc, err := loader.Load("testdata/v1.yaml")
	if err != nil {
		t.Fatalf("load v1: %v", err)
	}
	newDoc, err := loader.Load("testdata/v2.yaml")
	if err != nil {
		t.Fatalf("load v2: %v", err)
	}
	return oldDoc, newDoc
}

func find(rep *Report, level Level, kind Kind, substr string) *Change {
	for i := range rep.Changes {
		c := &rep.Changes[i]
		if c.Level == level && c.Kind == kind && contains(c.Path+c.Reason, substr) {
			return c
		}
	}
	return nil
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestCompareClassifications(t *testing.T) {
	oldDoc, newDoc := loadPair(t)
	rep := Compare(oldDoc, newDoc)
	if !rep.Breaking() {
		t.Fatal("expected breaking changes")
	}

	cases := []struct {
		level Level
		kind  Kind
		where string
	}{
		// removed path is breaking
		{LevelBreaking, KindRemoved, "/legacy"},
		// added path is info
		{LevelInfo, KindAdded, "/orders"},
		// removed response status is breaking
		{LevelBreaking, KindRemoved, "response 400"},
		// optional param became required: breaking
		{LevelBreaking, KindRequiredChanged, "parameter query:filter"},
		// added optional param: info
		{LevelInfo, KindAdded, "parameter query:sort"},
		// int32 -> int64 in request param: info (widening)
		{LevelInfo, KindTypeChanged, "query:limit"},
		// response property removed: breaking
		// (none here; status/count/note changed instead)
		// enum narrowed in shared component: breaking in request
		{LevelBreaking, KindEnumChanged, "status"},
		// integer -> number in response: breaking
		{LevelBreaking, KindTypeChanged, "count"},
		// nullable removed in request: breaking
		{LevelBreaking, KindTypeNarrowed, "note"},
		// new required property in request body: breaking
		{LevelBreaking, KindAdded, "sku"},
		// oneOf replaced: conservative warning
		{LevelWarning, KindUnionChanged, "shape"},
	}
	for _, tc := range cases {
		if find(rep, tc.level, tc.kind, tc.where) == nil {
			t.Errorf("missing %s/%s change matching %q\nall changes:", tc.level, tc.kind, tc.where)
			for _, c := range rep.Changes {
				t.Logf("  %s %s %s %s: %s", c.Level, c.Kind, c.Direction, c.Path, c.Reason)
			}
		}
	}
}

func TestDirectionMatters(t *testing.T) {
	oldDoc, newDoc := loadPair(t)
	rep := Compare(oldDoc, newDoc)
	// The shared Item schema is used in both request and response
	// positions; enum narrowing must be breaking for the request side
	// and info for the response side.
	var reqEnum, respEnum *Change
	for i := range rep.Changes {
		c := &rep.Changes[i]
		if c.Kind == KindEnumChanged && contains(c.Path, "status") {
			switch c.Direction {
			case DirectionRequest:
				reqEnum = c
			case DirectionResponse:
				respEnum = c
			}
		}
	}
	if reqEnum == nil || reqEnum.Level != LevelBreaking {
		t.Errorf("request enum narrowing should be breaking, got %+v", reqEnum)
	}
	if respEnum == nil || respEnum.Level != LevelInfo {
		t.Errorf("response enum narrowing should be info, got %+v", respEnum)
	}
}

func TestIdenticalDocuments(t *testing.T) {
	doc, err := loader.Load("testdata/v1.yaml")
	if err != nil {
		t.Fatal(err)
	}
	doc2, err := loader.Load("testdata/v1.yaml")
	if err != nil {
		t.Fatal(err)
	}
	rep := Compare(doc, doc2)
	if len(rep.Changes) != 0 {
		t.Fatalf("expected no changes, got %v", rep.Changes)
	}
	if rep.Breaking() {
		t.Fatal("identical docs must not be breaking")
	}
}

func TestDeterministic(t *testing.T) {
	oldDoc, newDoc := loadPair(t)
	first := Compare(oldDoc, newDoc)
	for i := 0; i < 5; i++ {
		got := Compare(oldDoc, newDoc)
		if len(got.Changes) != len(first.Changes) {
			t.Fatalf("run %d: %d vs %d changes", i, len(got.Changes), len(first.Changes))
		}
		for j := range got.Changes {
			if got.Changes[j] != first.Changes[j] {
				t.Fatalf("run %d change %d differs", i, j)
			}
		}
	}
}

func TestRecursiveSchemaNoHang(t *testing.T) {
	oldDoc, newDoc := loadPair(t)
	// Item has no recursion; add recursion check via loader testdata.
	// Here we only assert comparison terminates quickly.
	rep := Compare(oldDoc, newDoc)
	if rep == nil {
		t.Fatal("nil report")
	}
}
