package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"mirador/internal/db"
)

type accountRequest struct {
	CategoryID   *int64          `json:"category_id"`
	Name         *string         `json:"name"`
	SortOrder    *float64        `json:"sort_order"`
	SFDC         *string         `json:"sfdc"`
	Subdomain    *string         `json:"subdomain"`
	CSMName      *string         `json:"csm_name"`
	CSMLink      *string         `json:"csm_link"`
	AEName       *string         `json:"ae_name"`
	AELink       *string         `json:"ae_link"`
	SlackChannel *string         `json:"chatapp_channel"`
	Arr          *string         `json:"arr"`
	ChurnRisk    json.RawMessage `json:"churn_risk"`
	RenewalDate  *string         `json:"renewal_date"`
	Details      *string         `json:"details"`
	Tags         *[]db.Tag       `json:"tags"`
}

func (s *Server) listAccounts(w http.ResponseWriter, r *http.Request) {
	includeDeleted := queryBool(r, "include_deleted")
	accounts, err := s.store.ListAccounts(db.AccountFilter{
		CategoryIDs:    queryIDs(r, "category_id"),
		TagIDs:         queryIDs(r, "tag"),
		Name:           strings.TrimSpace(r.URL.Query().Get("q")),
		CSMNames:       queryStrings(r, "csm"),
		AENames:        queryStrings(r, "ae"),
		ChurnRisks:     parseChurnFilters(queryStrings(r, "churn")),
		IncludeDeleted: includeDeleted,
	})
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	if accounts == nil {
		accounts = []db.Account{}
	}
	tasks, err := s.store.ListTasks(db.TaskFilter{IncludeDeleted: includeDeleted})
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	tasksByAccount := make(map[int64][]db.Task, len(accounts))
	for _, task := range tasks {
		tasksByAccount[task.AccountID] = append(tasksByAccount[task.AccountID], task)
	}
	for i := range accounts {
		accounts[i].Tasks = tasksByAccount[accounts[i].ID]
		if accounts[i].Tasks == nil {
			accounts[i].Tasks = []db.Task{}
		}
	}
	s.writeJSON(w, http.StatusOK, accounts)
}

func (s *Server) getAccount(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	account, err := s.store.GetAccount(id, queryBool(r, "include_deleted"))
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, account)
}

func (s *Server) createAccount(w http.ResponseWriter, r *http.Request) {
	var request accountRequest
	if err := decodeJSON(w, r, &request); err != nil {
		s.handleDBError(w, err)
		return
	}
	if request.CategoryID == nil || *request.CategoryID <= 0 {
		s.writeError(w, http.StatusBadRequest, "category_id is required")
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
	sfdc, err := cleanText(stringOrDefault(request.SFDC, ""), 2000)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	subdomain, err := cleanText(stringOrDefault(request.Subdomain, ""), 2000)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	csmName, err := cleanText(stringOrDefault(request.CSMName, ""), 2000)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	csmLink, err := cleanText(stringOrDefault(request.CSMLink, ""), 2000)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	if csmLink != "" && !strings.HasPrefix(csmLink, "http://") && !strings.HasPrefix(csmLink, "https://") {
		s.writeError(w, http.StatusBadRequest, "csm_link must be a valid URL starting with http:// or https://")
		return
	}
	aeName, err := cleanText(stringOrDefault(request.AEName, ""), 2000)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	aeLink, err := cleanText(stringOrDefault(request.AELink, ""), 2000)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	if aeLink != "" && !strings.HasPrefix(aeLink, "http://") && !strings.HasPrefix(aeLink, "https://") {
		s.writeError(w, http.StatusBadRequest, "ae_link must be a valid URL starting with http:// or https://")
		return
	}
	slackChannel, err := cleanText(stringOrDefault(request.SlackChannel, ""), 2000)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	arr, err := cleanText(stringOrDefault(request.Arr, ""), 2000)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	churnRisk, err := parseChurnRiskRaw(request.ChurnRisk)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	renewalDate, err := validateOptionalDate(stringOrDefault(request.RenewalDate, ""), "renewal_date")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	details, err := cleanText(stringOrDefault(request.Details, ""), 10000)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	// Accounts are alphabetical by default; an explicit sort_order opts into
	// custom ordering.
	sortOrder := 0.0
	if request.SortOrder != nil {
		sortOrder = *request.SortOrder
	}
	tagIDs := []int64{}
	if request.Tags != nil {
		for _, tag := range *request.Tags {
			tagIDs = append(tagIDs, tag.ID)
		}
	}
	account, err := s.store.CreateAccount(*request.CategoryID, name, sortOrder, sfdc, subdomain, csmName, csmLink, aeName, aeLink, slackChannel, arr, churnRisk, renewalDate, details, tagIDs)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, account)
}

func (s *Server) updateAccount(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	current, err := s.store.GetAccount(id, false)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	var request accountRequest
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
	sfdc, err := cleanText(stringOrDefault(request.SFDC, current.SFDC), 2000)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	subdomain, err := cleanText(stringOrDefault(request.Subdomain, current.Subdomain), 2000)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	csmName := current.CSMName
	if request.CSMName != nil {
		csmName, err = cleanText(*request.CSMName, 2000)
		if err != nil {
			s.handleDBError(w, err)
			return
		}
	}
	csmLink := current.CSMLink
	if request.CSMLink != nil {
		csmLink, err = cleanText(*request.CSMLink, 2000)
		if err != nil {
			s.handleDBError(w, err)
			return
		}
		if csmLink != "" && !strings.HasPrefix(csmLink, "http://") && !strings.HasPrefix(csmLink, "https://") {
			s.writeError(w, http.StatusBadRequest, "csm_link must be a valid URL starting with http:// or https://")
			return
		}
	}
	aeName := current.AEName
	if request.AEName != nil {
		aeName, err = cleanText(*request.AEName, 2000)
		if err != nil {
			s.handleDBError(w, err)
			return
		}
	}
	aeLink := current.AELink
	if request.AELink != nil {
		aeLink, err = cleanText(*request.AELink, 2000)
		if err != nil {
			s.handleDBError(w, err)
			return
		}
		if aeLink != "" && !strings.HasPrefix(aeLink, "http://") && !strings.HasPrefix(aeLink, "https://") {
			s.writeError(w, http.StatusBadRequest, "ae_link must be a valid URL starting with http:// or https://")
			return
		}
	}
	slackChannel, err := cleanText(stringOrDefault(request.SlackChannel, current.SlackChannel), 2000)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	arr := current.Arr
	if request.Arr != nil {
		arr, err = cleanText(*request.Arr, 2000)
		if err != nil {
			s.handleDBError(w, err)
			return
		}
	}
	churnRisk := current.ChurnRisk
	if len(request.ChurnRisk) > 0 {
		parsed, err := parseChurnRiskRaw(request.ChurnRisk)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		churnRisk = parsed
	}
	renewalDate := current.RenewalDate
	if request.RenewalDate != nil {
		renewalDate, err = validateOptionalDate(*request.RenewalDate, "renewal_date")
		if err != nil {
			s.handleDBError(w, err)
			return
		}
	}
	details, err := cleanText(stringOrDefault(request.Details, current.Notes), 10000)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	sortOrder := parseSortOrder(request.SortOrder, current.SortOrder)

	var tagIDs *[]int64
	if request.Tags != nil {
		ids := make([]int64, len(*request.Tags))
		for i, tag := range *request.Tags {
			ids[i] = tag.ID
		}
		tagIDs = &ids
	}

	account, err := s.store.UpdateAccount(id, name, sortOrder, sfdc, subdomain, csmName, csmLink, aeName, aeLink, slackChannel, arr, churnRisk, renewalDate, details, tagIDs)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, account)
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	if queryBool(r, "permanent") {
		err = s.store.HardDeleteAccount(id)
	} else {
		err = s.store.SoftDeleteAccount(id)
	}
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (s *Server) restoreAccount(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	if err := s.store.RestoreAccount(id, queryBool(r, "with_children")); err != nil {
		s.handleDBError(w, err)
		return
	}
	// The account is restored even if its category is still in the trash, so
	// look it up without requiring the category to be active (c.deleted_at
	// IS NULL). Returning the restored account confirms success to the client.
	account, err := s.store.GetAccount(id, true)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, account)
}

func (s *Server) moveAccount(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	var request struct {
		CategoryID int64   `json:"category_id"`
		SortOrder  float64 `json:"sort_order"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		s.handleDBError(w, err)
		return
	}
	if err := s.store.MoveAccount(id, request.CategoryID, request.SortOrder); err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"moved": true})
}

// parseChurnRiskRaw decodes a json.RawMessage churn_risk value into *bool.
// An empty message (field absent) returns (nil, nil) — caller keeps current.
// "null" returns (nil, nil) — explicit unknown. "true"/"false" return the bool.
// Anything else is an error.
func parseChurnRiskRaw(raw json.RawMessage) (*bool, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	switch string(raw) {
	case "null":
		return nil, nil
	case "true":
		b := true
		return &b, nil
	case "false":
		b := false
		return &b, nil
	default:
		return nil, fmt.Errorf("churn_risk must be true or false")
	}
}
