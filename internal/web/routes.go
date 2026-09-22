package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"statuspulse/internal/model"
	"statuspulse/internal/store"
)

const (
	maxRequestBodySize  = 1 << 20
	maxServiceNameRunes = 100
	maxServiceURLLength = 2048
)

type handler struct {
	store store.ServiceStore
}

// NewHandler builds the HTTP handler used by the StatusPulse server.
func NewHandler(serviceStore store.ServiceStore) http.Handler {
	h := handler{store: serviceStore}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", home)
	mux.HandleFunc("GET /api/services", h.listServices)
	mux.HandleFunc("POST /api/services", h.createService)
	mux.HandleFunc("GET /api/services/{id}", h.getService)
	mux.HandleFunc("DELETE /api/services/{id}", h.deleteService)
	mux.HandleFunc("GET /api/services/{id}/checks", h.listChecks)
	mux.HandleFunc("GET /api/services/{id}/summary", h.serviceSummary)
	return mux
}

func home(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintln(w, "StatusPulse is running")
}

type createServiceRequest struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func (h handler) createService(w http.ResponseWriter, r *http.Request) {
	var input createServiceRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	name, serviceURL, err := validateService(input.Name, input.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	service, err := h.store.Create(r.Context(), model.Service{Name: name, URL: serviceURL})
	if err != nil {
		handleStoreError(w, r, err)
		return
	}

	w.Header().Set("Location", fmt.Sprintf("/api/services/%d", service.ID))
	writeJSON(w, http.StatusCreated, service)
}

func (h handler) listServices(w http.ResponseWriter, r *http.Request) {
	services, err := h.store.List(r.Context())
	if err != nil {
		handleStoreError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, services)
}

func (h handler) getService(w http.ResponseWriter, r *http.Request) {
	id, err := serviceID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	service, err := h.store.Get(r.Context(), id)
	if err != nil {
		handleStoreError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, service)
}

func (h handler) deleteService(w http.ResponseWriter, r *http.Request) {
	id, err := serviceID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.store.Delete(r.Context(), id); err != nil {
		handleStoreError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func serviceID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("service ID must be a positive integer")
	}
	return id, nil
}

func (h handler) serviceSummary(w http.ResponseWriter, r *http.Request) {
	id, err := serviceID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	summary, err := h.store.Summary(r.Context(), id, time.Now().UTC())
	if err != nil {
		handleStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (h handler) listChecks(w http.ResponseWriter, r *http.Request) {
	id, err := serviceID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit, offset := 50, 0
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 100 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 100")
			return
		}
	}
	if value := r.URL.Query().Get("offset"); value != "" {
		offset, err = strconv.Atoi(value)
		if err != nil || offset < 0 {
			writeError(w, http.StatusBadRequest, "offset must be a nonnegative integer")
			return
		}
	}
	checks, err := h.store.ListChecks(r.Context(), id, limit, offset)
	if err != nil {
		handleStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, checks)
}

func validateService(name, serviceURL string) (string, string, error) {
	name = strings.TrimSpace(name)
	serviceURL = strings.TrimSpace(serviceURL)

	if name == "" {
		return "", "", errors.New("name is required")
	}
	if utf8.RuneCountInString(name) > maxServiceNameRunes {
		return "", "", fmt.Errorf("name must be at most %d characters", maxServiceNameRunes)
	}
	if serviceURL == "" {
		return "", "", errors.New("URL is required")
	}
	if len(serviceURL) > maxServiceURLLength {
		return "", "", fmt.Errorf("URL must be at most %d characters", maxServiceURLLength)
	}

	parsedURL, err := url.Parse(serviceURL)
	if err != nil || parsedURL.Hostname() == "" {
		return "", "", errors.New("URL must be a valid absolute HTTP or HTTPS URL")
	}
	if !strings.EqualFold(parsedURL.Scheme, "http") && !strings.EqualFold(parsedURL.Scheme, "https") {
		return "", "", errors.New("URL must use the http or https scheme")
	}
	if parsedURL.User != nil {
		return "", "", errors.New("URL must not contain embedded credentials")
	}
	if parsedURL.Fragment != "" {
		return "", "", errors.New("URL must not contain a fragment")
	}

	return name, serviceURL, nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}

	return nil
}

func handleStoreError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}

	slog.Error("service store operation failed", "method", r.Method, "path", r.URL.Path, "error", err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("encode JSON response", "error", err)
	}
}
