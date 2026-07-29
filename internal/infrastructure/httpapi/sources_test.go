package httpapi_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/incu6us/markdex/internal/infrastructure/httpapi"
)

func TestHandleListSources(t *testing.T) {
	lister := &stubSourceLister{sources: []httpapi.Source{
		{SourceID: "01_company.md", Title: "Company", Chunks: 3},
		{SourceID: "05_orders.md", Title: "Orders & delivery", Chunks: 12},
	}}
	handler := newTestServer(t, testDeps{sourceLister: lister})

	rec := doRequest(t, handler, http.MethodGet, "/api/collections/kb/sources", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if lister.gotCollection != "kb" {
		t.Errorf("collection = %q, want %q", lister.gotCollection, "kb")
	}
	out := decodeBody[struct {
		Sources []httpapi.Source `json:"sources"`
	}](t, rec)
	if len(out.Sources) != 2 {
		t.Fatalf("sources = %+v, want 2", out.Sources)
	}
	if out.Sources[1].SourceID != "05_orders.md" || out.Sources[1].Chunks != 12 || out.Sources[1].Title != "Orders & delivery" {
		t.Errorf("sources[1] = %+v, want the orders doc with its title and chunk count", out.Sources[1])
	}
}

// An unconfigured source port degrades to an empty list, mirroring how
// handleListMemories treats a nil MemoryLister — the UI renders, it just has
// nothing to show.
func TestHandleListSourcesNotConfigured(t *testing.T) {
	handler := newTestServer(t, testDeps{})

	rec := doRequest(t, handler, http.MethodGet, "/api/collections/kb/sources", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	out := decodeBody[struct {
		Sources []httpapi.Source `json:"sources"`
	}](t, rec)
	if len(out.Sources) != 0 {
		t.Errorf("sources = %+v, want empty", out.Sources)
	}
}

func TestHandleListSourcesError(t *testing.T) {
	handler := newTestServer(t, testDeps{
		sourceLister: &stubSourceLister{err: errors.New("qdrant is down")},
	})

	rec := doRequest(t, handler, http.MethodGet, "/api/collections/kb/sources", nil)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestHandleDeleteSource(t *testing.T) {
	deleter := &stubSourceDeleter{}
	handler := newTestServer(t, testDeps{sourceDeleter: deleter})

	rec := doRequest(t, handler, http.MethodDelete, "/api/collections/kb/sources?source_id=05_orders.md", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	if deleter.deletedFrom != "kb" {
		t.Errorf("collection = %q, want %q", deleter.deletedFrom, "kb")
	}
	if len(deleter.deletedSourceIDs) != 1 || deleter.deletedSourceIDs[0] != "05_orders.md" {
		t.Errorf("deleted = %v, want [05_orders.md]", deleter.deletedSourceIDs)
	}
}

func TestHandleDeleteSourceValidation(t *testing.T) {
	deleter := &stubSourceDeleter{}
	handler := newTestServer(t, testDeps{sourceDeleter: deleter})

	cases := []struct {
		name string
		path string
		want int
	}{
		{name: "missing source_id", path: "/api/collections/kb/sources", want: http.StatusBadRequest},
		{name: "blank source_id", path: "/api/collections/kb/sources?source_id=%20", want: http.StatusBadRequest},
	}
	for _, tc := range cases {
		rec := doRequest(t, handler, http.MethodDelete, tc.path, nil)
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (body: %s)", tc.name, rec.Code, tc.want, rec.Body.String())
			continue
		}
	}
	if len(deleter.deletedSourceIDs) != 0 {
		t.Errorf("deleted = %v, want nothing deleted on invalid input", deleter.deletedSourceIDs)
	}
}

func TestHandleDeleteSourceNotConfigured(t *testing.T) {
	handler := newTestServer(t, testDeps{})

	rec := doRequest(t, handler, http.MethodDelete, "/api/collections/kb/sources?source_id=a.md", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestHandleDeleteSourceError(t *testing.T) {
	handler := newTestServer(t, testDeps{
		sourceDeleter: &stubSourceDeleter{err: errors.New("qdrant is down")},
	})

	rec := doRequest(t, handler, http.MethodDelete, "/api/collections/kb/sources?source_id=a.md", nil)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body: %s)", rec.Code, rec.Body.String())
	}
}
