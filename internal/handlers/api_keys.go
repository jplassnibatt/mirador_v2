package handlers

import (
	"net/http"

	"mirador/internal/db"
)

type apiKeyRequest struct {
	Name *string `json:"name"`
}

func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.store.ListAPIKeys()
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, keys)
}

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	var req apiKeyRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.handleDBError(w, err)
		return
	}
	name := stringOrDefault(req.Name, "API key")
	key, fullKey, err := s.store.CreateAPIKey(name)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	// Return the full key only once.
	type createResponse struct {
		db.APIKey
		Key string `json:"key"`
	}
	s.writeJSON(w, http.StatusCreated, createResponse{APIKey: key, Key: fullKey})
}

func (s *Server) deleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	if err := s.store.DeleteAPIKey(id); err != nil {
		s.handleDBError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	key, err := s.store.GetAPIKey(id)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, key)
}
