package handlers

import (
	"net/http"

	"mirador/internal/db"
)

type tagRequest struct {
	Name  *string `json:"name"`
	Color *string `json:"color"`
}

func (s *Server) listTags(w http.ResponseWriter, r *http.Request) {
	tags, err := s.store.ListTags(queryBool(r, "include_deleted"))
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	if tags == nil {
		tags = []db.Tag{}
	}
	s.writeJSON(w, http.StatusOK, tags)
}

func (s *Server) getTag(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	tag, err := s.store.GetTag(id)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, tag)
}

func (s *Server) createTag(w http.ResponseWriter, r *http.Request) {
	var request tagRequest
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
	tag, err := s.store.CreateTag(name, color)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, tag)
}

func (s *Server) updateTag(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	current, err := s.store.GetTag(id)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	var request tagRequest
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
	tag, err := s.store.UpdateTag(id, name, color)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, tag)
}

func (s *Server) deleteTag(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	if err := s.store.DeleteTag(id); err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
