package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"statuspulse/internal/model"
	"statuspulse/internal/store"
)

func TestDashboardRendersEscapedServiceState(t *testing.T) {
	serviceStore := store.NewMemoryStore()
	service, err := serviceStore.Create(context.Background(), model.Service{
		Name: "<script>alert('x')</script>",
		URL:  "https://example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	code, latency := http.StatusOK, int64(27)
	if err := serviceStore.SaveCheck(context.Background(), model.HealthCheck{
		ServiceID:      service.ID,
		Status:         model.StatusUp,
		HTTPStatusCode: &code,
		ResponseTimeMS: &latency,
		CheckedAt:      time.Now().Add(-3 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	NewHandler(serviceStore, time.Minute).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Header())
	}
	for _, expected := range []string{"&lt;script&gt;alert", "27 ms", "100.0%", "Stale"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("dashboard missing %q", expected)
		}
	}
	if strings.Contains(body, "<script>alert('x')</script>") {
		t.Fatal("service name was not HTML escaped")
	}
}

func TestHTMLFormValidationAndCrossOriginProtection(t *testing.T) {
	serviceStore := store.NewMemoryStore()
	app := NewHandler(serviceStore, time.Minute)

	invalid := formRequest("/services", url.Values{"name": {"My service"}, "url": {"ftp://example.com"}})
	invalidResponse := httptest.NewRecorder()
	app.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(invalidResponse.Body.String(), "URL must use the http or https scheme") ||
		!strings.Contains(invalidResponse.Body.String(), "My service") {
		t.Fatalf("invalid form response: %d %s", invalidResponse.Code, invalidResponse.Body)
	}

	crossSite := formRequest("/services", url.Values{"name": {"Blocked"}, "url": {"https://example.com"}})
	crossSite.Header.Set("Origin", "https://attacker.example")
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	crossSiteResponse := httptest.NewRecorder()
	app.ServeHTTP(crossSiteResponse, crossSite)
	services, err := serviceStore.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if crossSiteResponse.Code != http.StatusForbidden || len(services) != 0 {
		t.Fatalf("cross-site mutation was not blocked: %d, %d services", crossSiteResponse.Code, len(services))
	}
}

func TestHTMLCreateDetailAndDelete(t *testing.T) {
	serviceStore := store.NewMemoryStore()
	app := NewHandler(serviceStore, time.Minute)

	create := formRequest("/services", url.Values{"name": {"Example"}, "url": {"https://example.com"}})
	create.Header.Set("Origin", "http://example.com")
	create.Host = "example.com"
	created := httptest.NewRecorder()
	app.ServeHTTP(created, create)
	if created.Code != http.StatusSeeOther || created.Header().Get("Location") != "/" {
		t.Fatalf("create response: %d %s", created.Code, created.Header().Get("Location"))
	}

	detail := httptest.NewRecorder()
	app.ServeHTTP(detail, httptest.NewRequest(http.MethodGet, "/services/1", nil))
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "No completed checks") {
		t.Fatalf("detail response: %d %s", detail.Code, detail.Body)
	}

	deleteRequest := formRequest("/services/1/delete", nil)
	deleted := httptest.NewRecorder()
	app.ServeHTTP(deleted, deleteRequest)
	if deleted.Code != http.StatusSeeOther {
		t.Fatalf("delete response: %d", deleted.Code)
	}
	missing := httptest.NewRecorder()
	app.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/services/1", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("deleted detail response: %d", missing.Code)
	}
}

func formRequest(path string, values url.Values) *http.Request {
	body := io.Reader(strings.NewReader(values.Encode()))
	request := httptest.NewRequest(http.MethodPost, path, body)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return request
}
