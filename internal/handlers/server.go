// Package handlers contains the HTTP API and the server-rendered pages.
package handlers

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"mirador/internal/db"
	"mirador/internal/static"
)

const csrfCookieName = "mirador_csrf"

type contextKey string

const (
	csrfContextKey      contextKey = "csrf"
	requestIDContextKey contextKey = "request-id"
)

// Server wires the database, templates, and HTTP routes together.
type Server struct {
	store     *db.DB
	templates map[string]*template.Template
	logger    *slog.Logger
	version   string
}

// New returns the fully wrapped HTTP handler.
func New(store *db.DB, templates map[string]*template.Template, logger *slog.Logger, version string) http.Handler {
	s := &Server{store: store, templates: templates, logger: logger, version: version}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.pageIndex)
	mux.HandleFunc("GET /tasks", s.pageTasks)
	mux.HandleFunc("GET /completed", s.pageCompleted)
	mux.HandleFunc("GET /tags", s.pageTags)
	mux.HandleFunc("GET /archive", s.pageTrash)
	mux.HandleFunc("GET /api", s.pageAPIKeys)
	mux.HandleFunc("GET /api-docs", s.pageAPIDocs)
	mux.HandleFunc("GET /health", s.health)

	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static.FS))))

	mux.HandleFunc("GET /api/v1/csrf", s.apiCSRF)

	mux.HandleFunc("GET /api/v1/categories", s.listCategories)
	mux.HandleFunc("POST /api/v1/categories", s.createCategory)
	mux.HandleFunc("GET /api/v1/categories/{id}", s.getCategory)
	mux.HandleFunc("PUT /api/v1/categories/{id}", s.updateCategory)
	mux.HandleFunc("DELETE /api/v1/categories/{id}", s.deleteCategory)
	mux.HandleFunc("POST /api/v1/categories/{id}/restore", s.restoreCategory)
	mux.HandleFunc("PATCH /api/v1/categories/{id}/reorder", s.reorderCategory)

	mux.HandleFunc("GET /api/v1/accounts", s.listAccounts)
	mux.HandleFunc("POST /api/v1/accounts", s.createAccount)
	mux.HandleFunc("GET /api/v1/accounts/{id}", s.getAccount)
	mux.HandleFunc("PUT /api/v1/accounts/{id}", s.updateAccount)
	mux.HandleFunc("DELETE /api/v1/accounts/{id}", s.deleteAccount)
	mux.HandleFunc("PATCH /api/v1/accounts/{id}/move", s.moveAccount)
	mux.HandleFunc("POST /api/v1/accounts/{id}/restore", s.restoreAccount)

	mux.HandleFunc("GET /api/v1/tasks", s.listTasks)
	mux.HandleFunc("POST /api/v1/tasks", s.createTask)
	mux.HandleFunc("GET /api/v1/tasks/{id}", s.getTask)
	mux.HandleFunc("GET /api/v1/tasks/{id}/notes", s.listTaskNotes)
	mux.HandleFunc("POST /api/v1/tasks/{id}/notes", s.addTaskNote)
	mux.HandleFunc("PUT /api/v1/tasks/{id}/notes/{noteId}", s.editTaskNote)
	mux.HandleFunc("DELETE /api/v1/tasks/{id}/notes/{noteId}", s.deleteTaskNote)
	mux.HandleFunc("PUT /api/v1/tasks/{id}", s.updateTask)
	mux.HandleFunc("PATCH /api/v1/tasks/{id}/reorder", s.reorderTask)
	mux.HandleFunc("DELETE /api/v1/tasks/{id}", s.deleteTask)
	mux.HandleFunc("POST /api/v1/tasks/{id}/restore", s.restoreTask)
	mux.HandleFunc("GET /api/v1/archives", s.archives)
	mux.HandleFunc("POST /api/v1/archives/restore", s.restoreAll)

	mux.HandleFunc("GET /api/v1/tags", s.listTags)
	mux.HandleFunc("POST /api/v1/tags", s.createTag)
	mux.HandleFunc("GET /api/v1/tags/{id}", s.getTag)
	mux.HandleFunc("PUT /api/v1/tags/{id}", s.updateTag)
	mux.HandleFunc("DELETE /api/v1/tags/{id}", s.deleteTag)

	mux.HandleFunc("GET /api/v1/trash", s.apiTrash)
	mux.HandleFunc("GET /api/v1/export", s.exportCSV)
	mux.HandleFunc("POST /api/v1/export", s.exportCSV)
	mux.HandleFunc("POST /api/v1/import", s.importCSV)

	mux.HandleFunc("GET /api/v1/api-keys", s.listAPIKeys)
	mux.HandleFunc("GET /api/v1/api-keys/{id}", s.getAPIKey)
	mux.HandleFunc("POST /api/v1/api-keys", s.createAPIKey)
	mux.HandleFunc("DELETE /api/v1/api-keys/{id}", s.deleteAPIKey)
	mux.HandleFunc("GET /api/v1/archives/{id}", s.getArchive)

	return s.recoverPanics(s.requestID(s.logRequests(s.securityHeaders(s.apiKeyAuth(s.csrfProtection(mux))))))
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.store.IntegrityCheck(); err != nil {
		s.writeError(w, http.StatusInternalServerError, "database integrity check failed")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) apiCSRF(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{"csrf_token": csrfToken(r)})
}

func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := randomToken(8)
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDContextKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		s.logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
			"request_id", requestID(r),
		)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "geolocation=(), camera=(), microphone=()")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; "+
				"base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		if !strings.HasPrefix(r.URL.Path, "/static/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) csrfProtection(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API key authentication bypasses CSRF.
		if apiKeyFromContext(r) != nil {
			next.ServeHTTP(w, r)
			return
		}

		token := ""
		if cookie, err := r.Cookie(csrfCookieName); err == nil {
			token = cookie.Value
		}
		if token == "" {
			token = randomToken(32)
			http.SetCookie(w, &http.Cookie{
				Name:     csrfCookieName,
				Value:    token,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
				MaxAge:   30 * 24 * 60 * 60,
			})
		}

		// All API requests (including GET) require either an API key or a
		// valid CSRF token. The CSRF token endpoint itself is exempt, as is
		// the CSV export GET (download-only data the user can already view).
		if strings.HasPrefix(r.URL.Path, "/api/") &&
			r.URL.Path != "/api/v1/csrf" &&
			r.URL.Path != "/api/v1/export" {
			header := r.Header.Get("X-CSRF-Token")
			if header == "" || subtle.ConstantTimeCompare([]byte(header), []byte(token)) != 1 {
				// If an API key header was sent, the user intended API key auth,
				// so report an API key error rather than a CSRF error.
				if r.Header.Get("Authorization") != "" || r.Header.Get("Mirador-Api-Key") != "" {
					s.writeError(w, http.StatusUnauthorized, "invalid or missing API key")
				} else {
					s.writeError(w, http.StatusForbidden, "invalid or missing CSRF token")
				}
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(r, origin) {
				s.writeError(w, http.StatusForbidden, "cross-origin request blocked")
				return
			}
		}

		ctx := context.WithValue(r.Context(), csrfContextKey, token)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// apiKeyAuth authenticates requests that carry an API key in the
// Authorization: Bearer header or Mirador-Api-Key header. Valid keys bypass CSRF.
func (s *Server) apiKeyAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := extractAPIKey(r)
		if key != "" {
			if ak, err := s.store.AuthenticateAPIKey(key); err == nil {
				r = withAPIKeyContext(r, ak)
			} else {
				s.writeError(w, http.StatusUnauthorized, "invalid API key")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("panic recovered", "panic", recovered, "path", r.URL.Path, "request_id", requestID(r))
				s.writeError(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func isUnsafe(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

func sameOrigin(r *http.Request, origin string) bool {
	origin = strings.TrimSuffix(origin, "/")
	return origin == "http://"+r.Host || origin == "https://"+r.Host
}

func randomToken(bytes int) string {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(buffer)
}

func requestID(r *http.Request) string {
	value, _ := r.Context().Value(requestIDContextKey).(string)
	return value
}

func csrfToken(r *http.Request) string {
	value, _ := r.Context().Value(csrfContextKey).(string)
	return value
}
