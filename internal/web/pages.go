package web

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"statuspulse/internal/model"
	"statuspulse/internal/store"
)

//go:embed templates/*.html static/*.css
var pageAssets embed.FS

var pageTemplates = template.Must(template.ParseFS(pageAssets, "templates/*.html"))

type serviceRow struct {
	ID             int64
	Name           string
	URL            string
	CreatedAt      string
	Status         string
	StatusClass    string
	ResponseTime   string
	LastChecked    string
	LastCheckedAge string
	Uptime         string
	SampleCount    int
	Stale          bool
}

type dashboardData struct {
	Services  []serviceRow
	FormName  string
	FormURL   string
	FormError string
}

type checkRow struct {
	Status       string
	StatusClass  string
	HTTPCode     string
	ResponseTime string
	CheckedAt    string
	CheckedAge   string
	Error        string
}

type detailData struct {
	Service serviceRow
	Checks  []checkRow
}

func staticHandler() http.Handler {
	staticFiles, err := fs.Sub(pageAssets, "static")
	if err != nil {
		panic(err)
	}
	return http.StripPrefix("/static/", http.FileServer(http.FS(staticFiles)))
}

func (h handler) dashboard(w http.ResponseWriter, r *http.Request) {
	h.renderDashboard(w, r, dashboardData{}, http.StatusOK)
}

func (h handler) renderDashboard(w http.ResponseWriter, r *http.Request, data dashboardData, status int) {
	services, err := h.store.List(r.Context())
	if err != nil {
		htmlStoreError(w, r, err)
		return
	}
	now := time.Now().UTC()
	data.Services = make([]serviceRow, 0, len(services))
	for _, service := range services {
		summary, err := h.store.Summary(r.Context(), service.ID, now)
		if err != nil {
			htmlStoreError(w, r, err)
			return
		}
		data.Services = append(data.Services, presentService(summary, now, h.checkInterval))
	}
	executePage(w, "dashboard", data, status)
}

func (h handler) createServicePage(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
	if err := r.ParseForm(); err != nil {
		h.renderDashboard(w, r, dashboardData{FormError: "The submitted form could not be read."}, http.StatusBadRequest)
		return
	}

	name, serviceURL, err := validateService(r.FormValue("name"), r.FormValue("url"))
	if err != nil {
		h.renderDashboard(w, r, dashboardData{
			FormName:  r.FormValue("name"),
			FormURL:   r.FormValue("url"),
			FormError: err.Error(),
		}, http.StatusUnprocessableEntity)
		return
	}

	if _, err := h.store.Create(r.Context(), model.Service{Name: name, URL: serviceURL}); err != nil {
		htmlStoreError(w, r, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h handler) servicePage(w http.ResponseWriter, r *http.Request) {
	id, err := serviceID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	summary, err := h.store.Summary(r.Context(), id, now)
	if err != nil {
		htmlStoreError(w, r, err)
		return
	}
	checks, err := h.store.ListChecks(r.Context(), id, 50, 0)
	if err != nil {
		htmlStoreError(w, r, err)
		return
	}
	data := detailData{
		Service: presentService(summary, now, h.checkInterval),
		Checks:  make([]checkRow, 0, len(checks)),
	}
	for _, check := range checks {
		data.Checks = append(data.Checks, presentCheck(check, now))
	}
	executePage(w, "service", data, http.StatusOK)
}

func (h handler) deleteServicePage(w http.ResponseWriter, r *http.Request) {
	id, err := serviceID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.store.Delete(r.Context(), id); err != nil {
		htmlStoreError(w, r, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func presentService(summary model.ServiceSummary, now time.Time, interval time.Duration) serviceRow {
	row := serviceRow{
		ID:           summary.ID,
		Name:         summary.Name,
		URL:          summary.URL,
		CreatedAt:    formatTimestamp(summary.CreatedAt),
		Status:       "Not checked",
		StatusClass:  "unknown",
		ResponseTime: "—",
		LastChecked:  "Never",
		Uptime:       "N/A",
		SampleCount:  summary.SampleCount,
	}
	if summary.UptimePercent != nil {
		row.Uptime = fmt.Sprintf("%.1f%%", *summary.UptimePercent)
	}
	if summary.LatestCheck == nil {
		return row
	}
	row.Status = string(summary.LatestCheck.Status)
	row.StatusClass = strings.ToLower(row.Status)
	row.ResponseTime = formatLatency(summary.LatestCheck.ResponseTimeMS)
	row.LastChecked = formatTimestamp(summary.LatestCheck.CheckedAt)
	row.LastCheckedAge = formatAge(now.Sub(summary.LatestCheck.CheckedAt))
	row.Stale = now.Sub(summary.LatestCheck.CheckedAt) > 2*interval
	return row
}

func presentCheck(check model.HealthCheck, now time.Time) checkRow {
	code := "—"
	if check.HTTPStatusCode != nil {
		code = fmt.Sprintf("%d", *check.HTTPStatusCode)
	}
	errorText := check.ErrorMessage
	if check.ErrorKind != "" && errorText == "" {
		errorText = check.ErrorKind
	}
	return checkRow{
		Status:       string(check.Status),
		StatusClass:  strings.ToLower(string(check.Status)),
		HTTPCode:     code,
		ResponseTime: formatLatency(check.ResponseTimeMS),
		CheckedAt:    formatTimestamp(check.CheckedAt),
		CheckedAge:   formatAge(now.Sub(check.CheckedAt)),
		Error:        errorText,
	}
}

func formatLatency(milliseconds *int64) string {
	if milliseconds == nil {
		return "—"
	}
	return fmt.Sprintf("%d ms", *milliseconds)
}

func formatTimestamp(value time.Time) string {
	return value.UTC().Format("02 Jan 2006, 15:04:05 UTC")
}

func formatAge(age time.Duration) string {
	if age < 0 {
		age = 0
	}
	switch {
	case age < time.Minute:
		seconds := int(age.Seconds())
		if seconds < 1 {
			return "just now"
		}
		return fmt.Sprintf("%d seconds ago", seconds)
	case age < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(age.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(age.Hours()/24))
	}
}

func executePage(w http.ResponseWriter, name string, data any, status int) {
	var output bytes.Buffer
	if err := pageTemplates.ExecuteTemplate(&output, name, data); err != nil {
		slog.Error("render HTML page", "template", name, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = output.WriteTo(w)
}

func htmlStoreError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "service not found", http.StatusNotFound)
		return
	}
	slog.Error("render page from store", "method", r.Method, "path", r.URL.Path, "error", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

// protectBrowserMutations rejects form submissions a browser identifies as
// cross-site and validates Origin when it is present. CLI clients may omit both.
func protectBrowserMutations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
				http.Error(w, "cross-origin request denied", http.StatusForbidden)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				parsed, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(parsed.Host, r.Host) {
					http.Error(w, "cross-origin request denied", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
