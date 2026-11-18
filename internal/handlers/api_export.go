package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"mirador/internal/db"
	"mirador/internal/export"
)

func (s *Server) exportCSV(w http.ResponseWriter, r *http.Request) {
	includeDeleted := !queryBool(r, "active_only")
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	sections, err := export.ParseSections(r.URL.Query().Get("sections"))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if format != "csv" {
		data, err := export.ExportJSON(s.store, includeDeleted, sections)
		if err != nil {
			s.handleDBError(w, err)
			return
		}
		filename := fmt.Sprintf("mirador-%s.json", time.Now().Format("20060102-150405"))
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(data); err != nil {
			s.logger.Error("write export response", "error", err)
		}
		return
	}

	data, err := export.Export(s.store, includeDeleted, sections)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	filename := fmt.Sprintf("mirador-%s.csv", time.Now().Format("20060102-150405"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		s.logger.Error("write export response", "error", err)
	}
}

func (s *Server) importCSV(w http.ResponseWriter, r *http.Request) {
	conflict := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("conflict")))
	if conflict == "" {
		conflict = "duplicate"
	}
	sections, err := export.ParseSections(r.URL.Query().Get("sections"))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if conflict != "override" && conflict != "duplicate" {
		s.writeError(w, http.StatusBadRequest, "conflict must be override or duplicate")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 20<<20)
	contentType := r.Header.Get("Content-Type")

	// CSV import: multipart file upload only.
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(20 << 20); err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid multipart upload")
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "multipart file 'file' is required")
			return
		}
		defer file.Close()
		result, err := export.ImportCSV(s.store, file, conflict, sections)
		if err != nil {
			s.handleDBError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, result)
		return
	}

	// JSON import: default for all non-multipart requests.
	result, err := export.ImportJSON(s.store, r.Body, conflict, sections)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, result)
}

func (s *Server) apiTrash(w http.ResponseWriter, r *http.Request) {
	categories, err := s.store.ListDeletedCategories()
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	accounts, err := s.store.ListDeletedAccounts()
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	tasks, err := s.store.ListDeletedTasks()
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	if categories == nil {
		categories = []db.Category{}
	}
	if accounts == nil {
		accounts = []db.Account{}
	}
	if tasks == nil {
		tasks = []db.Task{}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"categories": categories,
		"accounts":   accounts,
		"tasks":      tasks,
	})
}
