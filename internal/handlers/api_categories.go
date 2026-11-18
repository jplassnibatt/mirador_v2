package handlers

import (
	"net/http"

	"mirador/internal/db"
)

type categoryRequest struct {
	Name      *string  `json:"name"`
	Color     *string  `json:"color"`
	SortOrder *float64 `json:"sort_order"`
	Expanded  *bool    `json:"expanded"`
}

func (s *Server) listCategories(w http.ResponseWriter, r *http.Request) {
	categories, err := s.store.ListCategories(queryBool(r, "include_deleted"))
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	if categories == nil {
		categories = []db.Category{}
	}
	s.writeJSON(w, http.StatusOK, categories)
}

func (s *Server) getCategory(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	category, err := s.store.GetCategory(id, queryBool(r, "include_deleted"))
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, category)
}

func (s *Server) createCategory(w http.ResponseWriter, r *http.Request) {
	var request categoryRequest
	if err := decodeJSON(w, r, &request); err != nil {
		s.handleDBError(w, err)
		return
	}
	if request.Name == nil {
		s.writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	name, err := cleanName(*request.Name)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	color, err := validateColor(stringOrDefault(request.Color, defaultColor))
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	sortOrder := 0.0
	if request.SortOrder == nil {
		sortOrder, err = s.store.NextCategorySortOrder()
		if err != nil {
			s.handleDBError(w, err)
			return
		}
	} else {
		sortOrder = *request.SortOrder
	}
	category, err := s.store.CreateCategory(name, color, sortOrder, boolOrDefault(request.Expanded, true))
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, category)
}

func (s *Server) updateCategory(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	current, err := s.store.GetCategory(id, false)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	var request categoryRequest
	if err := decodeJSON(w, r, &request); err != nil {
		s.handleDBError(w, err)
		return
	}
	name := current.Name
	if request.Name != nil {
		name, err = cleanName(*request.Name)
		if err != nil {
			s.handleDBError(w, err)
			return
		}
	}
	color := current.Color
	if request.Color != nil {
		color, err = validateColor(*request.Color)
		if err != nil {
			s.handleDBError(w, err)
			return
		}
	}
	sortOrder := parseSortOrder(request.SortOrder, current.SortOrder)
	expanded := boolOrDefault(request.Expanded, current.Expanded)

	category, err := s.store.UpdateCategory(id, name, color, sortOrder, expanded)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, category)
}

func (s *Server) deleteCategory(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	if queryBool(r, "permanent") {
		err = s.store.HardDeleteCategory(id)
	} else {
		err = s.store.SoftDeleteCategory(id)
	}
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (s *Server) restoreCategory(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	if err := s.store.RestoreCategory(id, queryBool(r, "with_children")); err != nil {
		s.handleDBError(w, err)
		return
	}
	category, err := s.store.GetCategory(id, false)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, category)
}

func (s *Server) reorderCategory(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	var request struct {
		SortOrder float64 `json:"sort_order"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		s.handleDBError(w, err)
		return
	}
	if err := s.store.ReorderCategory(id, request.SortOrder); err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"reordered": true})
}
