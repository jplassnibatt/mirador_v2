package handlers

import (
	"net/http"
	"sort"
	"strings"

	"mirador/internal/db"
)

type pageData struct {
	Title            string
	Nav              string
	Path             string
	CSRFToken        string
	Today            string
	Tags             []db.Tag
	TagFilters       []int64
	Search           string
	CategoryFilters  []int64
	CSMFilters       []string
	AEFilters        []string
	ChurnFilters     []bool
	FilterCategories []db.Category
	CSMOptions       []string
	AEOptions        []string
	Compact          bool
	ShowDone         bool
	Status           string
	Categories       []categoryView
	DateGroups       []dateGroup
	Trash            trashView
	APIKeys          []db.APIKey
	APISections      []apiSection
	Error            string

	Version string

	AccountCount int
	TaskCount    int
	TagCount     int

	OpenTaskCount      int
	CompletedTaskCount int
}

type categoryView struct {
	Category db.Category
	Accounts []accountView
}

type accountView struct {
	Account db.Account
	Tasks   []taskView
}

type dateGroup struct {
	Date  string
	Tasks []taskView
}

type taskView struct {
	Task            db.Task
	ShowAccount     bool
	Draggable       bool
	OpenDetails     bool
	MatchingFilters []matchingFilter
}

// matchingFilter is one applied facet filter shown next to a task's Category /
// Account crumb. Tags render as colored pills; CSM/AE/churn render as text.
type matchingFilter struct {
	Label string
	IsTag bool
	Color string
}

type trashView struct {
	Categories []db.Category
	Accounts   []db.Account
	Tasks      []db.Task
}

type apiEndpoint struct {
	Method      string
	Path        string
	Params      string
	Required    []string
	Description string
}

type apiSection struct {
	Title       string
	Description string
	Endpoints   []apiEndpoint
}

func (s *Server) baseData(r *http.Request, title, nav string) (pageData, error) {
	tags, err := s.store.ListTags(false)
	if err != nil {
		return pageData{}, err
	}
	if tags == nil {
		tags = []db.Tag{}
	}
	data := pageData{
		Title:     title,
		Nav:       nav,
		Path:      r.URL.Path,
		CSRFToken: csrfToken(r),
		Today:     db.Today(),
		Version:   s.version,
		Tags:      tags,
		// TagFilters and Search are shared across all list views.
		TagFilters: queryIDs(r, "tag"),
		Search:     strings.TrimSpace(r.URL.Query().Get("q")),
		ShowDone:   queryBool(r, "show_done"),
		Status:     strings.TrimSpace(r.URL.Query().Get("status")),
	}

	// Only the pages that render the filter bar need its option lists.
	switch nav {
	case "accounts", "tasks", "completed":
		categories, err := s.store.ListCategories(false)
		if err != nil {
			return pageData{}, err
		}
		if categories == nil {
			categories = []db.Category{}
		}
		csmNames, err := s.store.ListCSMNames()
		if err != nil {
			return pageData{}, err
		}
		aeNames, err := s.store.ListAENames()
		if err != nil {
			return pageData{}, err
		}
		data.FilterCategories = categories
		data.CategoryFilters = queryIDs(r, "category_id")
		data.CSMFilters = canonicalFoldAll(queryStrings(r, "csm"), csmNames)
		data.AEFilters = canonicalFoldAll(queryStrings(r, "ae"), aeNames)
		data.ChurnFilters = parseChurnFilters(queryStrings(r, "churn"))
		data.CSMOptions = csmNames
		data.AEOptions = aeNames

		if nav == "accounts" || nav == "tasks" || nav == "tags" || nav == "trash" {
			if cookie, err := r.Cookie("mirador_compact"); err == nil {
				data.Compact = queryBoolFromValue(cookie.Value)
			}
		}
	}

	// Section counts shown next to page titles. The account count respects the
	// active filters so it matches the displayed list rather than the total.
	data.AccountCount, _ = s.store.CountAccounts(data.accountFilter())
	data.TaskCount, _ = s.store.CountTasks()
	data.TagCount = len(tags)

	// Task completion counts for the status pills.
	data.OpenTaskCount, data.CompletedTaskCount, _ = s.store.CountTasksByCompletion()

	return data, nil
}

// canonicalFoldAll maps query values to the option spelling used in the UI so
// case-insensitive filters still show the selected option.
func canonicalFoldAll(values, options []string) []string {
	folded := make([]string, 0, len(values))
	for _, value := range values {
		folded = append(folded, canonicalFold(value, options))
	}
	return folded
}

// canonicalFold maps one query value to the option spelling used in the UI.
func canonicalFold(value string, options []string) string {
	for _, option := range options {
		if strings.EqualFold(value, option) {
			return option
		}
	}
	return value
}

// TagSelected reports whether a tag chip should be active.
func (d pageData) TagSelected(id int64) bool {
	return containsID(d.TagFilters, id)
}

// CategorySelected reports whether a category chip should be active.
func (d pageData) CategorySelected(id int64) bool {
	return containsID(d.CategoryFilters, id)
}

// CSMSelected reports whether a CSM chip should be active.
func (d pageData) CSMSelected(name string) bool {
	return containsFold(d.CSMFilters, name)
}

// AESelected reports whether an AE chip should be active.
func (d pageData) AESelected(name string) bool {
	return containsFold(d.AEFilters, name)
}

// ChurnSelected reports whether a churn chip should be active.
func (d pageData) ChurnSelected(value bool) bool {
	return containsBool(d.ChurnFilters, value)
}

func containsID(ids []int64, id int64) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

func containsFold(values []string, value string) bool {
	for _, candidate := range values {
		if strings.EqualFold(candidate, value) {
			return true
		}
	}
	return false
}

func containsBool(values []bool, value bool) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// parseChurnFilters converts query values to booleans. It accepts yes/true/1
// for true and no/false/0 for false; anything else is skipped so legacy URLs
// (e.g. ?churn=yes) keep working.
func parseChurnFilters(values []string) []bool {
	out := make([]bool, 0, len(values))
	for _, v := range values {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "yes", "true", "1":
			out = append(out, true)
		case "no", "false", "0":
			out = append(out, false)
		}
	}
	return out
}

// accountFilter returns the account-level filters shared by all list views.
func (d pageData) accountFilter() db.AccountFilter {
	return db.AccountFilter{
		CategoryIDs: d.CategoryFilters,
		TagIDs:      d.TagFilters,
		Name:        d.Search,
		CSMNames:    d.CSMFilters,
		AENames:     d.AEFilters,
		ChurnRisks:  d.ChurnFilters,
	}
}

// taskFilter returns the task filters shared by all list views. Task search is
// opt-in because the accounts page searches account names, not task text.
func (d pageData) taskFilter() db.TaskFilter {
	return db.TaskFilter{
		CategoryIDs: d.CategoryFilters,
		TagIDs:      d.TagFilters,
		CSMNames:    d.CSMFilters,
		AENames:     d.AEFilters,
		ChurnRisks:  d.ChurnFilters,
	}
}

// AccountFiltered reports whether any account-level filter is active. Used to
// hide empty category groups after filtering.
func (d pageData) AccountFiltered() bool {
	return len(d.TagFilters) > 0 || len(d.CategoryFilters) > 0 || d.Search != "" ||
		len(d.CSMFilters) > 0 || len(d.AEFilters) > 0 || len(d.ChurnFilters) > 0
}

// taskMatchingFilters returns the labels of the active facet filters that
// actually match this task's account, in a stable order: CSM, AE, churn risk,
// then tags. Category is intentionally omitted because it is already shown in
// the Category / Account crumb. Empty when nothing matches. Tags carry their
// color so they can render as pills in the template.
func taskMatchingFilters(data pageData, task db.Task, info db.AccountFilterInfo) []matchingFilter {
	var out []matchingFilter

	// CSM: the account's CSM must be one of the selected values.
	if len(data.CSMFilters) > 0 {
		for _, f := range data.CSMFilters {
			if info.CSMName == f {
				out = append(out, matchingFilter{Label: f})
				break
			}
		}
	}
	// AE: same as CSM.
	if len(data.AEFilters) > 0 {
		for _, f := range data.AEFilters {
			if info.AEName == f {
				out = append(out, matchingFilter{Label: f})
				break
			}
		}
	}
	// Churn risk.
	for _, v := range data.ChurnFilters {
		label := "Not at risk"
		if v {
			label = "At risk"
		}
		if info.ChurnRisk != nil && *info.ChurnRisk == v {
			out = append(out, matchingFilter{Label: label})
		}
	}
	return out
}

func (s *Server) pageIndex(w http.ResponseWriter, r *http.Request) {
	data, err := s.baseData(r, "Accounts", "accounts")
	if err != nil {
		s.pageError(w, err)
		return
	}

	categories := data.FilterCategories
	accounts, err := s.store.ListAccounts(data.accountFilter())
	if err != nil {
		s.pageError(w, err)
		return
	}
	taskFilter := data.taskFilter()
	taskFilter.HideDone = !data.ShowDone
	tasks, err := s.store.ListTasks(taskFilter)
	if err != nil {
		s.pageError(w, err)
		return
	}

	tasksByAccount := make(map[int64][]taskView, len(accounts))
	for _, task := range tasks {
		tasksByAccount[task.AccountID] = append(tasksByAccount[task.AccountID], taskView{
			Task:      task,
			Draggable: true,
		})
	}
	accountsByCategory := make(map[int64][]accountView, len(categories))
	for _, account := range accounts {
		accountsByCategory[account.CategoryID] = append(accountsByCategory[account.CategoryID], accountView{
			Account: account,
			Tasks:   tasksByAccount[account.ID],
		})
	}
	for _, category := range categories {
		views := accountsByCategory[category.ID]
		if data.AccountFiltered() && len(views) == 0 {
			continue
		}
		data.Categories = append(data.Categories, categoryView{
			Category: category,
			Accounts: views,
		})
	}
	s.render(w, "index.html", data)
}

func (s *Server) pageTasks(w http.ResponseWriter, r *http.Request) {
	data, err := s.baseData(r, "Due Date", "tasks")
	if err != nil {
		s.pageError(w, err)
		return
	}
	taskFilter := data.taskFilter()
	taskFilter.Search = data.Search
	// ShowDone (show_done=1) shows all tasks including completed.
	// status=completed shows only completed tasks.
	// Default shows only open tasks.
	if data.Status == "completed" {
		taskFilter.HideDone = false
		taskFilter.CompletedOnly = true
	} else if data.ShowDone {
		taskFilter.HideDone = false
		taskFilter.CompletedOnly = false
	} else {
		taskFilter.HideDone = true
		taskFilter.CompletedOnly = false
	}
	tasks, err := s.store.ListTasks(taskFilter)
	if err != nil {
		s.pageError(w, err)
		return
	}
	// Group tasks by due date. The date sections are ordered newest first,
	// oldest last. Task order inside a date is preserved from ListTasks
	// (sort_order), so drag ordering keeps working.
	tasksByDate := make(map[string][]taskView, len(tasks))
	dates := make([]string, 0, len(tasks))
	// Matching filters are computed per task against that task's account, so a
	// row only shows the filters that actually apply to it (not every active
	// facet filter).
	accountInfos, err := s.store.AccountFilterInfos()
	if err != nil {
		s.pageError(w, err)
		return
	}
	for _, task := range tasks {
		if _, seen := tasksByDate[task.DueDate]; !seen {
			dates = append(dates, task.DueDate)
		}
		tasksByDate[task.DueDate] = append(tasksByDate[task.DueDate], taskView{
			Task:            task,
			ShowAccount:     true,
			Draggable:       true,
			OpenDetails:     true,
			MatchingFilters: taskMatchingFilters(data, task, accountInfos[task.AccountID]),
		})
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	for _, date := range dates {
		data.DateGroups = append(data.DateGroups, dateGroup{
			Date:  date,
			Tasks: tasksByDate[date],
		})
	}
	s.render(w, "tasks_due_date.html", data)
}

func (s *Server) pageCompleted(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/tasks?status=completed", http.StatusFound)
}

func (s *Server) pageTags(w http.ResponseWriter, r *http.Request) {
	data, err := s.baseData(r, "Tags", "tags")
	if err != nil {
		s.pageError(w, err)
		return
	}
	s.render(w, "tags.html", data)
}

func (s *Server) pageTrash(w http.ResponseWriter, r *http.Request) {
	data, err := s.baseData(r, "Trash", "trash")
	if err != nil {
		s.pageError(w, err)
		return
	}
	categories, err := s.store.ListDeletedCategories()
	if err != nil {
		s.pageError(w, err)
		return
	}
	accounts, err := s.store.ListDeletedAccounts()
	if err != nil {
		s.pageError(w, err)
		return
	}
	tasks, err := s.store.ListDeletedTasks()
	if err != nil {
		s.pageError(w, err)
		return
	}
	data.Trash = trashView{Categories: categories, Accounts: accounts, Tasks: tasks}
	s.render(w, "archive.html", data)
}

func (s *Server) pageAPIKeys(w http.ResponseWriter, r *http.Request) {
	data, err := s.baseData(r, "API", "api")
	if err != nil {
		s.pageError(w, err)
		return
	}
	keys, err := s.store.ListAPIKeys()
	if err != nil {
		s.pageError(w, err)
		return
	}
	data.APIKeys = keys
	s.render(w, "api.html", data)
}

func (s *Server) pageAPIDocs(w http.ResponseWriter, r *http.Request) {
	data, err := s.baseData(r, "API documentation", "api")
	if err != nil {
		s.pageError(w, err)
		return
	}
	data.APISections = []apiSection{
		{
			Title:       "Accounts",
			Description: "Create, read, update, and archive accounts.",
			Endpoints: []apiEndpoint{
				{Method: "GET", Path: "/api/v1/accounts", Params: "?category_id=&tag=&q=&csm=&ae=&churn=", Description: "List accounts with their tasks (q searches the account name; repeat category_id, tag, csm, or ae to match any value)"},
				{Method: "POST", Path: "/api/v1/accounts", Params: "{category_id, name, sfdc, subdomain, csm_name, csm_link, ae_name, ae_link, chatapp_channel, arr, churn_risk, renewal_date, details, tags}", Required: []string{"category_id", "name"}, Description: "Create an account"},
				{Method: "GET", Path: "/api/v1/accounts/{id}", Description: "Get one account"},
				{Method: "PUT", Path: "/api/v1/accounts/{id}", Params: "{category_id, name, sfdc, subdomain, csm_name, csm_link, ae_name, ae_link, chatapp_channel, arr, churn_risk, renewal_date, details, tags}", Description: "Update an account (all fields optional)"},
				{Method: "DELETE", Path: "/api/v1/accounts/{id}", Params: "?permanent=1", Description: "Archive (soft delete) or permanently delete an account"},
				{Method: "POST", Path: "/api/v1/accounts/{id}/restore", Description: "Restore an archived account"},
				{Method: "PATCH", Path: "/api/v1/accounts/{id}/move", Params: "{category_id, sort_order}", Required: []string{"category_id", "sort_order"}, Description: "Move an account to another category / reorder"},
			},
		},
		{
			Title:       "Categories",
			Description: "Create, read, update, and archive categories.",
			Endpoints: []apiEndpoint{
				{Method: "GET", Path: "/api/v1/categories", Description: "List categories"},
				{Method: "POST", Path: "/api/v1/categories", Params: "{name, color}", Required: []string{"name"}, Description: "Create a category (color defaults to #6b7280)"},
				{Method: "GET", Path: "/api/v1/categories/{id}", Description: "Get one category"},
				{Method: "PUT", Path: "/api/v1/categories/{id}", Params: "{name, color, sort_order, expanded}", Description: "Update a category (all fields optional)"},
				{Method: "DELETE", Path: "/api/v1/categories/{id}", Params: "?permanent=1", Description: "Archive or permanently delete a category (cascades to accounts and tasks)"},
				{Method: "POST", Path: "/api/v1/categories/{id}/restore", Params: "?with_children=1", Description: "Restore an archived category (optionally with its accounts and tasks)"},
				{Method: "PATCH", Path: "/api/v1/categories/{id}/reorder", Params: "{sort_order}", Required: []string{"sort_order"}, Description: "Reorder a category"},
			},
		},
		{
			Title:       "Tasks",
			Description: "Create, read, update, and archive tasks.",
			Endpoints: []apiEndpoint{
				{Method: "GET", Path: "/api/v1/tasks", Params: "?account_id=&category_id=&tag=&q=&csm=&ae=&churn=&include_done=1&completed=1&overdue=1&hide_done=1", Description: "List tasks (q searches task title, ticket, details, and account name; repeat category_id, tag, csm, or ae to match any value)"},
				{Method: "POST", Path: "/api/v1/tasks", Params: "{account_id, title, ticket, details, due_date, completed}", Required: []string{"account_id", "title", "due_date"}, Description: "Create a task"},
				{Method: "GET", Path: "/api/v1/tasks/{id}", Description: "Get one task"},
				{Method: "PUT", Path: "/api/v1/tasks/{id}", Params: "{title, ticket, details, due_date, completed}", Description: "Update a task (all fields optional)"},
				{Method: "DELETE", Path: "/api/v1/tasks/{id}", Params: "?permanent=1", Description: "Archive or permanently delete a task"},
				{Method: "POST", Path: "/api/v1/tasks/{id}/restore", Description: "Restore an archived task. Also restores the parent account (and its category if needed) so the task becomes visible again."},
				{Method: "PATCH", Path: "/api/v1/tasks/{id}/reorder", Params: "{due_date, sort_order, before_id, after_id}", Required: []string{"due_date", "sort_order"}, Description: "Reorder a task within the same due date"},
			},
		},
		{
			Title:       "Task notes",
			Description: "Manage notes attached to tasks.",
			Endpoints: []apiEndpoint{
				{Method: "GET", Path: "/api/v1/tasks/{id}/notes", Description: "List notes for a task"},
				{Method: "POST", Path: "/api/v1/tasks/{id}/notes", Params: "{note}", Required: []string{"note"}, Description: "Add a note to a task"},
				{Method: "PUT", Path: "/api/v1/tasks/{id}/notes/{noteId}", Params: "{note}", Required: []string{"note"}, Description: "Edit a task note"},
				{Method: "DELETE", Path: "/api/v1/tasks/{id}/notes/{noteId}", Description: "Delete a task note"},
			},
		},
		{
			Title:       "Tags",
			Description: "Manage tags.",
			Endpoints: []apiEndpoint{
				{Method: "GET", Path: "/api/v1/tags", Description: "List tags"},
				{Method: "POST", Path: "/api/v1/tags", Params: "{name, color}", Required: []string{"name"}, Description: "Create a tag (color defaults to #6b7280)"},
				{Method: "GET", Path: "/api/v1/tags/{id}", Description: "Get one tag"},
				{Method: "PUT", Path: "/api/v1/tags/{id}", Params: "{name, color}", Description: "Update a tag (all fields optional)"},
				{Method: "DELETE", Path: "/api/v1/tags/{id}", Description: "Permanently delete a tag"},
			},
		},
		{
			Title:       "API keys",
			Description: "Create and manage API keys for script access.",
			Endpoints: []apiEndpoint{
				{Method: "GET", Path: "/api/v1/api-keys", Description: "List API keys (prefix only, no full key)"},
				{Method: "GET", Path: "/api/v1/api-keys/{id}", Description: "Get one API key (prefix only, no full key)"},
				{Method: "POST", Path: "/api/v1/api-keys", Params: "{name}", Required: []string{"name"}, Description: "Create an API key (full key returned once)"},
				{Method: "DELETE", Path: "/api/v1/api-keys/{id}", Description: "Revoke an API key"},
			},
		},
		{
			Title:       "Archives",
			Description: "List and restore archived (soft-deleted) records.",
			Endpoints: []apiEndpoint{
				{Method: "GET", Path: "/api/v1/archives", Description: "List archived categories, accounts, and tasks"},
				{Method: "GET", Path: "/api/v1/archives/{id}", Description: "Get one archived category, account, or task by ID"},
				{Method: "POST", Path: "/api/v1/archives/restore", Params: "{\"categories\": [1], \"accounts\": [2], \"tasks\": [3], \"with_children\": false}", Description: "Bulk-restore archived categories, accounts, and tasks via JSON. Restoring a task also restores its parent account (and category if needed)."},
			},
		},
		{
			Title:       "Export / Import",
			Description: "Backup and restore data via CSV or JSON.",
			Endpoints: []apiEndpoint{
				{Method: "GET", Path: "/api/v1/export", Params: "?active_only=1&format=csv|json&sections=categories,tags,accounts,tasks", Description: "Export all data as CSV (default) or JSON"},
				{Method: "POST", Path: "/api/v1/import", Params: "?conflict=duplicate|override&sections=categories,tags,accounts,tasks", Description: "Import CSV file (multipart) or JSON body (application/json)"},
			},
		},
	}
	s.render(w, "api_docs.html", data)
}

func (s *Server) pageError(w http.ResponseWriter, err error) {
	s.logger.Error("render page", "error", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func (s *Server) render(w http.ResponseWriter, page string, data pageData) {
	tpl, ok := s.templates[page]
	if !ok {
		s.logger.Error("missing template", "page", page)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tpl.ExecuteTemplate(w, "layout", data); err != nil {
		s.logger.Error("execute template", "page", page, "error", err)
	}
}
