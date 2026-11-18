package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"mirador/internal/db"
)

var (
	hexColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	datePattern     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// apiKeyContextKey is the context key for storing an authenticated API key.
type apiKeyContextKeyType int

const apiKeyContextKey apiKeyContextKeyType = iota

// apiKeyFromContext returns the authenticated API key from the request context,
// or nil if none.
func apiKeyFromContext(r *http.Request) *db.APIKey {
	v, _ := r.Context().Value(apiKeyContextKey).(*db.APIKey)
	return v
}

// withAPIKeyContext returns a request context with the API key stored.
func withAPIKeyContext(r *http.Request, ak *db.APIKey) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), apiKeyContextKey, ak))
}

// extractAPIKey extracts an API key from the Authorization: Bearer header or
// the Mirador-Api-Key header.
func extractAPIKey(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") {
		return auth[7:]
	}
	if key := r.Header.Get("Mirador-Api-Key"); key != "" {
		return key
	}
	return ""
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		s.logger.Error("encode JSON response", "error", err)
	}
}

func (s *Server) writeError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, status, map[string]string{"error": message})
}

func (s *Server) handleDBError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrNotFound):
		s.writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, db.ErrCrossDate):
		s.writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, db.ErrConflict):
		s.writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, db.ErrInvalid):
		s.writeError(w, http.StatusBadRequest, err.Error())
	case db.IsConflict(err):
		s.writeError(w, http.StatusConflict, "a record with that name already exists")
	default:
		s.logger.Error("database operation failed", "error", err)
		s.writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: invalid JSON body", db.ErrInvalid)
	}
	return nil
}

func pathID(r *http.Request, name string) (int64, error) {
	value := r.PathValue(name)
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%w: invalid %s", db.ErrInvalid, name)
	}
	return id, nil
}

func queryID(r *http.Request, name string) int64 {
	value := strings.TrimSpace(r.URL.Query().Get(name))
	if value == "" {
		return 0
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

// queryIDs returns every valid positive integer for a repeated query param.
func queryIDs(r *http.Request, name string) []int64 {
	var ids []int64
	for _, raw := range r.URL.Query()[name] {
		id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

// queryStrings returns every non-empty value for a repeated query param.
func queryStrings(r *http.Request, name string) []string {
	var values []string
	for _, raw := range r.URL.Query()[name] {
		if value := strings.TrimSpace(raw); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func queryBool(r *http.Request, name string) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func queryBoolFromValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func cleanName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: name is required", db.ErrInvalid)
	}
	if len([]rune(value)) > 200 {
		return "", fmt.Errorf("%w: name is too long (maximum 200 characters)", db.ErrInvalid)
	}
	return value, nil
}

func cleanTitle(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: title is required", db.ErrInvalid)
	}
	if len([]rune(value)) > 300 {
		return "", fmt.Errorf("%w: title is too long (maximum 300 characters)", db.ErrInvalid)
	}
	return value, nil
}

func cleanText(value string, maximum int) (string, error) {
	value = strings.TrimSpace(value)
	if len([]rune(value)) > maximum {
		return "", fmt.Errorf("%w: text is too long (maximum %d characters)", db.ErrInvalid, maximum)
	}
	return value, nil
}

// defaultColor is used when a category or tag is created without an explicit
// color. It matches the neutral fallback used by the UI and by import.
const defaultColor = "#6b7280"

func validateColor(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultColor, nil
	}
	if !hexColorPattern.MatchString(value) {
		return "", fmt.Errorf("%w: color must be a six-digit hex value like #3b82f6", db.ErrInvalid)
	}
	return strings.ToLower(value), nil
}

func validateDueDate(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !datePattern.MatchString(value) {
		return "", fmt.Errorf("%w: due_date must be YYYY-MM-DD", db.ErrInvalid)
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return "", fmt.Errorf("%w: due_date is not a real date", db.ErrInvalid)
	}
	return value, nil
}

// validateOptionalDate validates a YYYY-MM-DD string when present and treats
// an empty value as "no date".
func validateOptionalDate(value, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if !datePattern.MatchString(value) {
		return "", fmt.Errorf("%w: %s must be YYYY-MM-DD", db.ErrInvalid, field)
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return "", fmt.Errorf("%w: %s is not a real date", db.ErrInvalid, field)
	}
	return value, nil
}

func parseSortOrder(value *float64, fallback float64) float64 {
	if value == nil {
		return fallback
	}
	return *value
}

func boolOrDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func stringOrDefault(value *string, fallback string) string {
	if value == nil {
		return fallback
	}
	return *value
}
