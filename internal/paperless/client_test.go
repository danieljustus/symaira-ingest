package paperless

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestListDocuments(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token test-token" {
			t.Errorf("Authorization header = %q, want Token test-token", r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode(listResponse[Document]{
			Count: 1,
			Results: []Document{
				{ID: 1, Title: "Test Doc", CreatedDate: FlexDate{time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)}},
			},
			Next: "",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(srv.URL, "test-token")
	docs, err := c.ListDocuments(time.Time{})
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("len(docs) = %d, want 1", len(docs))
	}
	if docs[0].Title != "Test Doc" {
		t.Errorf("docs[0].Title = %q, want Test Doc", docs[0].Title)
	}
}

func TestListDocuments_Pagination(t *testing.T) {
	callCount := 0
	var baseURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			json.NewEncoder(w).Encode(listResponse[Document]{
				Count:   2,
				Results: []Document{{ID: 1, Title: "Doc 1"}},
				Next:    baseURL + "/api/documents/?format=json&page=2",
			})
		} else {
			json.NewEncoder(w).Encode(listResponse[Document]{
				Count:   2,
				Results: []Document{{ID: 2, Title: "Doc 2"}},
				Next:    "",
			})
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	baseURL = srv.URL

	c := NewClient(srv.URL, "test-token")
	docs, err := c.ListDocuments(time.Time{})
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("len(docs) = %d, want 2", len(docs))
	}
	if callCount != 2 {
		t.Errorf("callCount = %d, want 2", callCount)
	}
}

func TestGetDocument(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/documents/42/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Document{ID: 42, Title: "Specific Doc"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(srv.URL, "test-token")
	doc, err := c.GetDocument(42)
	if err != nil {
		t.Fatalf("GetDocument: %v", err)
	}
	if doc.ID != 42 || doc.Title != "Specific Doc" {
		t.Errorf("doc = %+v, want ID=42 Title=Specific Doc", doc)
	}
}

func TestDownloadDocument(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/documents/5/download/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("file content here"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(srv.URL, "test-token")
	var buf []byte
	err := c.DownloadDocument(5, (*mockWriter)(&buf))
	if err != nil {
		t.Fatalf("DownloadDocument: %v", err)
	}
	if string(buf) != "file content here" {
		t.Errorf("downloaded content = %q, want file content here", string(buf))
	}
}

func TestListTags(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(listResponse[Tag]{
			Results: []Tag{{ID: 1, Name: "invoice", Slug: "invoice"}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(srv.URL, "test-token")
	tags, err := c.ListTags()
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(tags) != 1 || tags[0].Name != "invoice" {
		t.Errorf("tags = %+v, want [{ID:1 Name:invoice}]", tags)
	}
}

func TestListCorrespondents(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/correspondents/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(listResponse[Correspondent]{
			Results: []Correspondent{{ID: 1, Name: "Acme Corp"}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(srv.URL, "test-token")
	corrs, err := c.ListCorrespondents()
	if err != nil {
		t.Fatalf("ListCorrespondents: %v", err)
	}
	if len(corrs) != 1 || corrs[0].Name != "Acme Corp" {
		t.Errorf("correspondents = %+v, want [{ID:1 Name:Acme Corp}]", corrs)
	}
}

func TestListDocumentTypes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/document_types/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(listResponse[DocumentType]{
			Results: []DocumentType{{ID: 1, Name: "Invoice"}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(srv.URL, "test-token")
	types, err := c.ListDocumentTypes()
	if err != nil {
		t.Fatalf("ListDocumentTypes: %v", err)
	}
	if len(types) != 1 || types[0].Name != "Invoice" {
		t.Errorf("types = %+v, want [{ID:1 Name:Invoice}]", types)
	}
}

func TestListStoragePaths(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/storage_paths/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(listResponse[StoragePath]{
			Results: []StoragePath{{ID: 11, Name: "Finance/Invoices"}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(srv.URL, "test-token")
	paths, err := c.ListStoragePaths()
	if err != nil {
		t.Fatalf("ListStoragePaths: %v", err)
	}
	if len(paths) != 1 || paths[0].Name != "Finance/Invoices" {
		t.Errorf("paths = %+v, want [{ID:11 Name:Finance/Invoices}]", paths)
	}
}

func TestListDocuments_RealAPIShape(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"count": 1,
			"results": [{
				"id": 1,
				"title": "Invoice",
				"created_date": "2026-10-15",
				"created": "2026-10-15",
				"correspondent": 18,
				"document_type": 18,
				"tags": [1, 875, 986],
				"storage_path": 11,
				"mime_type": "application/pdf",
				"original_file_name": "invoice.pdf",
				"archived_file_name": "invoice-archived.pdf",
				"page_count": 3
			}],
			"next": null
		}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(srv.URL, "test-token")
	docs, err := c.ListDocuments(time.Time{})
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("len(docs) = %d, want 1", len(docs))
	}
	doc := docs[0]

	wantDate := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	if !doc.CreatedDate.Equal(wantDate) {
		t.Errorf("doc.CreatedDate = %v, want %v", doc.CreatedDate, wantDate)
	}
	if !doc.Created.Equal(wantDate) {
		t.Errorf("doc.Created = %v, want %v", doc.Created, wantDate)
	}
	if doc.Correspondent == nil || doc.Correspondent.ID != 18 {
		t.Errorf("doc.Correspondent = %+v, want ID=18", doc.Correspondent)
	}
	if doc.DocumentType == nil || doc.DocumentType.ID != 18 {
		t.Errorf("doc.DocumentType = %+v, want ID=18", doc.DocumentType)
	}
	if doc.StoragePath == nil || doc.StoragePath.ID != 11 {
		t.Errorf("doc.StoragePath = %+v, want ID=11", doc.StoragePath)
	}
	wantTagIDs := []int{1, 875, 986}
	if len(doc.Tags) != len(wantTagIDs) {
		t.Fatalf("len(doc.Tags) = %d, want %d", len(doc.Tags), len(wantTagIDs))
	}
	for i, id := range wantTagIDs {
		if doc.Tags[i].ID != id {
			t.Errorf("doc.Tags[%d].ID = %d, want %d", i, doc.Tags[i].ID, id)
		}
	}
	if doc.MimeType != "application/pdf" {
		t.Errorf("doc.MimeType = %q, want application/pdf", doc.MimeType)
	}
	if doc.OriginalFileName != "invoice.pdf" {
		t.Errorf("doc.OriginalFileName = %q, want invoice.pdf", doc.OriginalFileName)
	}
	if doc.ArchivedFileName != "invoice-archived.pdf" {
		t.Errorf("doc.ArchivedFileName = %q, want invoice-archived.pdf", doc.ArchivedFileName)
	}
	if doc.PageCount != 3 {
		t.Errorf("doc.PageCount = %d, want 3", doc.PageCount)
	}
}

func TestListDocuments_EmbeddedRefShape(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"count": 1,
			"results": [{
				"id": 1,
				"title": "Invoice",
				"created_date": "2026-01-15T00:00:00Z",
				"correspondent": {"id": 1, "name": "Acme Corp"},
				"document_type": {"id": 2, "name": "Invoice"},
				"tags": [{"id": 1, "name": "financial"}],
				"storage_path": null
			}],
			"next": null
		}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(srv.URL, "test-token")
	docs, err := c.ListDocuments(time.Time{})
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	doc := docs[0]
	if doc.Correspondent == nil || doc.Correspondent.Name != "Acme Corp" {
		t.Errorf("doc.Correspondent = %+v, want Name=Acme Corp", doc.Correspondent)
	}
	if doc.DocumentType == nil || doc.DocumentType.Name != "Invoice" {
		t.Errorf("doc.DocumentType = %+v, want Name=Invoice", doc.DocumentType)
	}
	if len(doc.Tags) != 1 || doc.Tags[0].Name != "financial" {
		t.Errorf("doc.Tags = %+v, want [{Name:financial}]", doc.Tags)
	}
	if doc.StoragePath != nil {
		t.Errorf("doc.StoragePath = %+v, want nil", doc.StoragePath)
	}
}

func TestClient_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("unauthorized"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "bad-token")
	_, err := c.ListDocuments(time.Time{})
	if err == nil {
		t.Fatal("expected error for 401 status")
	}
}

type mockWriter []byte

func (m *mockWriter) Write(p []byte) (n int, err error) {
	*m = append(*m, p...)
	return len(p), nil
}
