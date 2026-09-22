package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"statuspulse/internal/model"
	"statuspulse/internal/store"
)

func requestAPI(app http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}

func TestAPICreateListGetDelete(t *testing.T) {
	app := NewHandler(store.NewMemoryStore(), time.Minute)
	empty := requestAPI(app, "GET", "/api/services", "")
	if empty.Code != 200 || strings.TrimSpace(empty.Body.String()) != "[]" {
		t.Fatalf("empty list: %d %s", empty.Code, empty.Body)
	}
	created := requestAPI(app, "POST", "/api/services", `{"name":"  Example  ","url":" https://example.com "}`)
	var service model.Service
	if err := json.Unmarshal(created.Body.Bytes(), &service); err != nil {
		t.Fatal(err)
	}
	if created.Code != 201 || created.Header().Get("Location") != "/api/services/1" || service.Name != "Example" || service.URL != "https://example.com" || service.CreatedAt.IsZero() {
		t.Fatalf("create: %d %+v", created.Code, service)
	}
	for _, path := range []string{"/api/services", "/api/services/1", "/api/services/1/summary", "/api/services/1/checks"} {
		w := requestAPI(app, "GET", path, "")
		if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("GET %s: %d", path, w.Code)
		}
	}
	deleted := requestAPI(app, "DELETE", "/api/services/1", "")
	if deleted.Code != 204 || deleted.Body.Len() != 0 {
		t.Fatalf("delete: %d %s", deleted.Code, deleted.Body)
	}
	for _, method := range []string{"GET", "DELETE"} {
		if w := requestAPI(app, method, "/api/services/1", ""); w.Code != 404 {
			t.Fatalf("missing %s: %d", method, w.Code)
		}
	}
}

func TestAPIRejectsInvalidBodiesWithoutSaving(t *testing.T) {
	cases := map[string]string{
		"empty": "", "malformed": "{", "null": "null", "array": "[]",
		"wrong type":       `{"name":3,"url":"https://example.com"}`,
		"unknown field":    `{"name":"A","url":"https://example.com","extra":true}`,
		"multiple objects": `{"name":"A","url":"https://example.com"}{}`,
		"blank name":       `{"name":"  ","url":"https://example.com"}`,
		"long name":        `{"name":"` + strings.Repeat("a", 101) + `","url":"https://example.com"}`,
		"missing host":     `{"name":"A","url":"https:///path"}`,
		"relative URL":     `{"name":"A","url":"/path"}`,
		"credentials":      `{"name":"A","url":"https://user:password@example.com"}`,
		"fragment":         `{"name":"A","url":"https://example.com/#part"}`,
		"scheme":           `{"name":"A","url":"file:///tmp/test"}`,
		"oversized body":   `{"name":"` + strings.Repeat("a", maxRequestBodySize) + `"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			s := store.NewMemoryStore()
			w := requestAPI(NewHandler(s, time.Minute), "POST", "/api/services", body)
			var failure errorResponse
			if err := json.Unmarshal(w.Body.Bytes(), &failure); err != nil {
				t.Fatal(err)
			}
			if w.Code != 400 || failure.Error == "" {
				t.Fatalf("response: %d %s", w.Code, w.Body)
			}
			services, err := s.List(context.Background())
			if err != nil || len(services) != 0 {
				t.Fatalf("invalid request saved data: %v %v", services, err)
			}
		})
	}
}

func TestAPIIDsAndPagination(t *testing.T) {
	app := NewHandler(store.NewMemoryStore(), time.Minute)
	for _, path := range []string{"/api/services/nope", "/api/services/0", "/api/services/-1", "/api/services/999999999999999999999", "/api/services/1/checks?limit=0", "/api/services/1/checks?limit=101", "/api/services/1/checks?limit=x", "/api/services/1/checks?offset=-1", "/api/services/1/checks?offset=x"} {
		if w := requestAPI(app, "GET", path, ""); w.Code != 400 {
			t.Fatalf("GET %s = %d", path, w.Code)
		}
	}
	if w := requestAPI(app, "PUT", "/api/services", ""); w.Code != 405 {
		t.Fatalf("unsupported method: %d", w.Code)
	}
}

type unavailableStore struct{ store.ServiceStore }

func (unavailableStore) List(context.Context) ([]model.Service, error) {
	return nil, errors.New("private database path and SQL details")
}

func TestStoreErrorsDoNotLeak(t *testing.T) {
	app := NewHandler(unavailableStore{}, time.Minute)
	for _, path := range []string{"/api/services", "/"} {
		w := requestAPI(app, "GET", path, "")
		if w.Code != 500 || strings.Contains(w.Body.String(), "private database") {
			t.Fatalf("internal error leaked: %d %s", w.Code, w.Body)
		}
	}
}

func TestBrowserOriginChecks(t *testing.T) {
	for _, test := range []struct {
		name, method, origin, site string
		want                       int
	}{
		{"same origin", "POST", "http://example.com", "same-origin", 204},
		{"CLI", "POST", "", "", 204},
		{"other host", "POST", "http://other.example", "", 403},
		{"other scheme", "POST", "https://example.com", "", 403},
		{"opaque", "POST", "null", "", 403},
		{"cross-site metadata", "POST", "", "cross-site", 403},
		{"cross-site deletion", "DELETE", "http://other.example", "cross-site", 403},
		{"safe navigation", "GET", "http://other.example", "cross-site", 204},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(test.method, "http://example.com/services", nil)
			r.Header.Set("Origin", test.origin)
			r.Header.Set("Sec-Fetch-Site", test.site)
			w := httptest.NewRecorder()
			protectBrowserMutations(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, r)
			if w.Code != test.want {
				t.Fatalf("got %d want %d", w.Code, test.want)
			}
		})
	}
}
