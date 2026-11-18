package handlers_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"mirador/internal/db"
	"mirador/internal/handlers"
	"mirador/internal/templates"
)

type testAPI struct {
	t      *testing.T
	base   string
	client *http.Client
	token  string
}

func newTestAPI(t *testing.T) (*testAPI, *db.DB) {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "mirador.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	templateSets, err := templates.Parse()
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	server := httptest.NewServer(handlers.New(store, templateSets, logger, ""))
	t.Cleanup(server.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	client := server.Client()
	client.Jar = jar
	api := &testAPI{t: t, base: server.URL, client: client}
	api.token = api.fetchToken()
	return api, store
}

func (a *testAPI) fetchToken() string {
	a.t.Helper()
	resp := a.request(http.MethodGet, "/api/v1/csrf", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		a.t.Fatalf("GET /api/v1/csrf status = %d, want 200", resp.StatusCode)
	}
	var payload struct {
		Token string `json:"csrf_token"`
	}
	decode(a.t, resp.Body, &payload)
	if payload.Token == "" {
		a.t.Fatal("csrf_token is empty")
	}
	return payload.Token
}

func (a *testAPI) request(method, path string, body []byte, headers map[string]string) *http.Response {
	a.t.Helper()
	req, err := http.NewRequest(method, a.base+path, bytes.NewReader(body))
	if err != nil {
		a.t.Fatalf("build %s %s request: %v", method, path, err)
	}
	if body != nil && headers["Content-Type"] == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if a.token != "" {
		req.Header.Set("X-CSRF-Token", a.token)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func (a *testAPI) jsonRequest(method, path string, payload any, wantStatus int) *http.Response {
	a.t.Helper()
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			a.t.Fatalf("encode request body: %v", err)
		}
	}
	resp := a.request(method, path, body, nil)
	if resp.StatusCode != wantStatus {
		defer resp.Body.Close()
		a.t.Fatalf("%s %s status = %d, want %d; body: %s",
			method, path, resp.StatusCode, wantStatus, readBody(a.t, resp.Body))
	}
	return resp
}

func (a *testAPI) page(path string) (int, string) {
	a.t.Helper()
	resp := a.request(http.MethodGet, path, nil, nil)
	defer resp.Body.Close()
	return resp.StatusCode, readBody(a.t, resp.Body)
}

func decode(t *testing.T, reader io.Reader, target any) {
	t.Helper()
	if err := json.NewDecoder(reader).Decode(target); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
}

func readBody(t *testing.T, reader io.Reader) string {
	t.Helper()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(data)
}

func expectStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("status = %d, want %d; body: %s", resp.StatusCode, want, readBody(t, resp.Body))
	}
}

func expectPageContains(t *testing.T, api *testAPI, path, fragment string) {
	t.Helper()
	status, body := api.page(path)
	if status != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", path, status)
	}
	if !strings.Contains(body, fragment) {
		t.Fatalf("GET %s body does not contain %q", path, fragment)
	}
}

func TestSecurityAndCSRF(t *testing.T) {
	api, _ := newTestAPI(t)

	resp := api.request(http.MethodGet, "/health", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /health status = %d, want 200", resp.StatusCode)
	}
	if resp.Header.Get("X-Request-ID") == "" {
		t.Error("X-Request-ID header is empty")
	}
	var health map[string]string
	decode(t, resp.Body, &health)
	resp.Body.Close()
	if health["status"] != "ok" {
		t.Fatalf("health payload = %+v, want status ok", health)
	}

	resp = api.request(http.MethodGet, "/", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := resp.Header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", got)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("Content-Security-Policy = %q, want default-src 'self'", csp)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control for page = %q, want no-store", got)
	}
	pageBody := readBody(t, resp.Body)
	resp.Body.Close()
	if !strings.Contains(pageBody, "Mirador") || !strings.Contains(pageBody, `name="csrf-token"`) {
		t.Fatal("index page is missing branding or the CSRF meta tag")
	}

	resp = api.request(http.MethodGet, "/static/css/style.css", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET stylesheet status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got == "no-store" {
		t.Error("static assets should not be marked no-store")
	}
	if body := readBody(t, resp.Body); !strings.Contains(body, "--tag-color") {
		t.Error("stylesheet does not look like Mirador CSS")
	}
	resp.Body.Close()

	resp = api.request(http.MethodGet, "/static/js/app.js", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET app.js status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	// A state-changing API call without a CSRF header is rejected.
	noToken := *api
	noToken.token = ""
	expectStatus(t, noToken.request(http.MethodPost, "/api/v1/categories",
		[]byte(`{"name":"Blocked"}`), nil), http.StatusForbidden)

	// A matching header without the cookie is also rejected.
	req, err := http.NewRequest(http.MethodPost, api.base+"/api/v1/categories",
		strings.NewReader(`{"name":"NoCookie"}`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", api.token)
	resp, err = (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("send no-cookie request: %v", err)
	}
	expectStatus(t, resp, http.StatusForbidden)

	// Cross-origin requests are blocked even with a valid token.
	expectStatus(t, api.request(http.MethodPost, "/api/v1/categories",
		[]byte(`{"name":"CrossOrigin"}`), map[string]string{"Origin": "http://evil.example"}), http.StatusForbidden)

	// A same-origin request is allowed.
	resp = api.request(http.MethodPost, "/api/v1/categories",
		[]byte(`{"name":"Allowed","color":"#123456"}`), map[string]string{"Origin": api.base})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("same-origin create status = %d, want 201", resp.StatusCode)
	}
	var category db.Category
	decode(t, resp.Body, &category)
	resp.Body.Close()
	if category.Name != "Allowed" || category.Color != "#123456" {
		t.Fatalf("created category = %+v", category)
	}

	// Malformed or incomplete JSON is a client error, not a server error.
	expectStatus(t, api.request(http.MethodPost, "/api/v1/categories", []byte(`{`), nil), http.StatusBadRequest)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/categories", []byte(`{}`), nil), http.StatusBadRequest)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/categories",
		[]byte(`{"name":"Bad color","color":"blue"}`), nil), http.StatusBadRequest)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/categories/99999", nil, nil), http.StatusNotFound)
	expectStatus(t, api.request(http.MethodGet, "/no-such-page", nil, nil), http.StatusNotFound)
}

func TestOptionalCategoryAndTagColor(t *testing.T) {
	api, _ := newTestAPI(t)

	// Color is optional on create and defaults to the neutral color.
	category := createJSON[db.Category](t, api, "/api/v1/categories", map[string]any{"name": "No color"})
	if category.Color != "#6b7280" {
		t.Fatalf("category default color = %q, want #6b7280", category.Color)
	}
	tag := createJSON[db.Tag](t, api, "/api/v1/tags", map[string]any{"name": "No color tag"})
	if tag.Color != "#6b7280" {
		t.Fatalf("tag default color = %q, want #6b7280", tag.Color)
	}

	// An explicitly empty color is treated as omitted.
	emptyCategory := createJSON[db.Category](t, api, "/api/v1/categories",
		map[string]any{"name": "Empty color", "color": ""})
	if emptyCategory.Color != "#6b7280" {
		t.Fatalf("empty category color = %q, want #6b7280", emptyCategory.Color)
	}
	emptyTag := createJSON[db.Tag](t, api, "/api/v1/tags",
		map[string]any{"name": "Empty color tag", "color": ""})
	if emptyTag.Color != "#6b7280" {
		t.Fatalf("empty tag color = %q, want #6b7280", emptyTag.Color)
	}

	// Invalid colors are still rejected.
	expectStatus(t, api.request(http.MethodPost, "/api/v1/categories",
		[]byte(`{"name":"Bad","color":"blue"}`), nil), http.StatusBadRequest)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/tags",
		[]byte(`{"name":"Bad","color":"blue"}`), nil), http.StatusBadRequest)

	// Updates: omitting color keeps the current value; an empty color resets it.
	coloredTag := createJSON[db.Tag](t, api, "/api/v1/tags",
		map[string]any{"name": "Colored tag", "color": "#123456"})
	resp := api.jsonRequest(http.MethodPut, fmt.Sprintf("/api/v1/tags/%d", coloredTag.ID),
		map[string]any{"name": "Colored tag"}, http.StatusOK)
	var kept db.Tag
	decode(t, resp.Body, &kept)
	resp.Body.Close()
	if kept.Color != "#123456" {
		t.Fatalf("omitted color = %q, want #123456", kept.Color)
	}
	resp = api.jsonRequest(http.MethodPut, fmt.Sprintf("/api/v1/tags/%d", coloredTag.ID),
		map[string]any{"color": ""}, http.StatusOK)
	var reset db.Tag
	decode(t, resp.Body, &reset)
	resp.Body.Close()
	if reset.Color != "#6b7280" {
		t.Fatalf("empty color = %q, want #6b7280", reset.Color)
	}
}

type apiSeed struct {
	category db.Category
	tag      db.Tag
	account  db.Account
	first    db.Task
	second   db.Task
}

func createJSON[T any](t *testing.T, api *testAPI, path string, payload any) T {
	t.Helper()
	resp := api.jsonRequest(http.MethodPost, path, payload, http.StatusCreated)
	defer resp.Body.Close()
	var value T
	decode(t, resp.Body, &value)
	return value
}

func seedAPI(t *testing.T, api *testAPI) apiSeed {
	t.Helper()
	var seed apiSeed
	seed.category = createJSON[db.Category](t, api, "/api/v1/categories", map[string]any{
		"name": "Work", "color": "#3b82f6",
	})
	seed.tag = createJSON[db.Tag](t, api, "/api/v1/tags", map[string]any{
		"name": "Urgent", "color": "#ff0000",
	})
	seed.account = createJSON[db.Account](t, api, "/api/v1/accounts", map[string]any{
		"category_id":     seed.category.ID,
		"name":            "Acme",
		"sfdc":            "https://acme.example",
		"subdomain":       "phone",
		"chatapp_channel": "",
		"csm_name":        "Alice",
		"ae_name":         "Bob",
		"churn_risk":      true,
		"notes":           "account notes",
		"tags":            []map[string]any{{"id": seed.tag.ID, "name": "Urgent", "color": "#ff0000"}},
	})
	seed.first = createJSON[db.Task](t, api, "/api/v1/tasks", map[string]any{
		"account_id": seed.account.ID,
		"title":      "First task",
		"details":    "first notes",
		"due_date":   "2030-06-01",
	})
	seed.second = createJSON[db.Task](t, api, "/api/v1/tasks", map[string]any{
		"account_id": seed.account.ID,
		"title":      "Second task",
		"details":    "second notes",
		"due_date":   "2030-06-01",
	})
	return seed
}

func TestAccountDefaultOrder(t *testing.T) {
	api, _ := newTestAPI(t)
	category := createJSON[db.Category](t, api, "/api/v1/categories", map[string]any{"name": "Work"})
	createJSON[db.Account](t, api, "/api/v1/accounts", map[string]any{
		"category_id": category.ID, "name": "Zeta",
	})
	createJSON[db.Account](t, api, "/api/v1/accounts", map[string]any{
		"category_id": category.ID, "name": "Alpha",
	})

	resp := api.jsonRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/accounts?category_id=%d", category.ID), nil, http.StatusOK)
	var accounts []db.Account
	decode(t, resp.Body, &accounts)
	resp.Body.Close()
	if len(accounts) != 2 || accounts[0].Name != "Alpha" || accounts[1].Name != "Zeta" {
		t.Fatalf("default account order = %+v, want Alpha then Zeta", accounts)
	}
}

func TestListAccountsIncludesTasks(t *testing.T) {
	api, _ := newTestAPI(t)
	seed := seedAPI(t, api)
	empty := createJSON[db.Account](t, api, "/api/v1/accounts", map[string]any{
		"category_id": seed.category.ID, "name": "No tasks",
	})

	resp := api.jsonRequest(http.MethodGet, "/api/v1/accounts", nil, http.StatusOK)
	var accounts []db.Account
	decode(t, resp.Body, &accounts)
	resp.Body.Close()
	byID := map[int64]db.Account{}
	for _, account := range accounts {
		byID[account.ID] = account
	}
	if got := byID[seed.account.ID].Tasks; len(got) != 2 {
		t.Fatalf("account tasks = %+v, want the two seeded tasks", got)
	}
	if tasks := byID[empty.ID].Tasks; tasks == nil || len(tasks) != 0 {
		t.Fatalf("taskless account tasks = %+v, want an empty array", tasks)
	}
}

func TestDueDateViewSortsDateGroups(t *testing.T) {
	api, _ := newTestAPI(t)
	category := createJSON[db.Category](t, api, "/api/v1/categories", map[string]any{"name": "Work"})
	account := createJSON[db.Account](t, api, "/api/v1/accounts", map[string]any{
		"category_id": category.ID, "name": "Acme",
	})
	for _, due := range []string{"2030-06-03", "2030-06-01", "2030-06-02"} {
		createJSON[db.Task](t, api, "/api/v1/tasks", map[string]any{
			"account_id": account.ID,
			"title":      "Task " + due,
			"due_date":   due,
		})
	}

	status, body := api.page("/tasks?show_done=1")
	if status != http.StatusOK {
		t.Fatalf("GET /tasks status = %d, want 200", status)
	}
	re := regexp.MustCompile(`<section class="date-group[^"]*" data-date="([^"]+)"`)
	matches := re.FindAllStringSubmatch(body, -1)
	got := make([]string, 0, len(matches))
	for _, match := range matches {
		got = append(got, match[1])
	}
	want := []string{"2030-06-03", "2030-06-02", "2030-06-01"}
	if len(got) != len(want) {
		t.Fatalf("date groups = %v, want one group per date %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("date groups = %v, want %v", got, want)
		}
	}
	if !strings.Contains(body, `class="container split"`) {
		t.Fatal("due-date page does not use the split details layout")
	}
	if !strings.Contains(body, "data-open-task-details") {
		t.Fatal("due-date task titles are not clickable")
	}
}

func TestRestoreAccountAfterCategoryDeleted(t *testing.T) {
	api, _ := newTestAPI(t)
	category := createJSON[db.Category](t, api, "/api/v1/categories", map[string]any{"name": "Work"})
	account := createJSON[db.Account](t, api, "/api/v1/accounts", map[string]any{
		"category_id": category.ID, "name": "a11",
	})
	createJSON[db.Task](t, api, "/api/v1/tasks", map[string]any{
		"account_id": account.ID, "title": "Task in a11", "due_date": "2030-06-01",
	})

	// Delete the account, then its category.
	expectStatus(t, api.request(http.MethodDelete,
		fmt.Sprintf("/api/v1/accounts/%d", account.ID), nil, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodDelete,
		fmt.Sprintf("/api/v1/categories/%d", category.ID), nil, nil), http.StatusOK)

	// The account is in the trash even though its category is deleted too.
	resp := api.request(http.MethodGet, "/api/v1/trash", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/trash status = %d, want 200", resp.StatusCode)
	}
	var trash struct {
		Accounts []db.Account `json:"accounts"`
	}
	decode(t, resp.Body, &trash)
	resp.Body.Close()
	found := false
	for _, a := range trash.Accounts {
		if a.ID == account.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("account is missing from the trash after category deletion")
	}

	// Restoring the account via bulk restore must succeed even though its
	// category is still deleted. Previously this returned 404 because the
	// restored account could not be re-fetched while its category remained
	// archived.
	resp = api.jsonRequest(http.MethodPost, "/api/v1/archives/restore", map[string]any{
		"accounts": []int64{account.ID},
	}, http.StatusOK)
	var result handlers.RestoreResponse
	decode(t, resp.Body, &result)
	resp.Body.Close()
	if len(result.Accounts) != 1 || result.Accounts[0].Status != "restored" {
		t.Fatalf("bulk restore account = %+v, want restored", result.Accounts)
	}

	// The account is moved to the uncategorized category because its original
	// category is still archived.
	resp = api.request(http.MethodGet,
		fmt.Sprintf("/api/v1/accounts/%d?include_deleted=1", account.ID), nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET restored account status = %d, want 200", resp.StatusCode)
	}
	var restored db.Account
	decode(t, resp.Body, &restored)
	resp.Body.Close()
	if restored.ID != account.ID {
		t.Fatalf("restored account id = %d, want %d", restored.ID, account.ID)
	}
	if restored.CategoryID == category.ID {
		t.Fatalf("account still references deleted category %d", category.ID)
	}
	if restored.CategoryName != "Uncategorized" {
		t.Fatalf("account category_name = %q, want Uncategorized", restored.CategoryName)
	}

	// The account is no longer in the trash (it was restored).
	resp = api.request(http.MethodGet, "/api/v1/trash", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/trash status = %d, want 200", resp.StatusCode)
	}
	decode(t, resp.Body, &trash)
	resp.Body.Close()
	for _, a := range trash.Accounts {
		if a.ID == account.ID {
			t.Fatal("account is still in the trash after restore")
		}
	}
}

func TestAPIKeyAuthentication(t *testing.T) {
	api, _ := newTestAPI(t)

	// Create an API key via the API (requires CSRF token for POST).
	apiKeyResp := api.jsonRequest(http.MethodPost,
		"/api/v1/api-keys", map[string]any{"name": "test-key"}, http.StatusCreated)
	defer apiKeyResp.Body.Close()
	var createdKey struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Key  string `json:"key"`
	}
	decode(t, apiKeyResp.Body, &createdKey)
	if createdKey.Key == "" {
		t.Fatal("API key response missing full key")
	}

	// Invalid API key should return 401 with "invalid API key".
	resp := api.request(http.MethodGet, "/api/v1/accounts", nil,
		map[string]string{"Authorization": "Bearer invalid_key_12345"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid API key status = %d, want 401; body: %s",
			resp.StatusCode, readBody(t, resp.Body))
	}
	var errResp struct {
		Error string `json:"error"`
	}
	decode(t, resp.Body, &errResp)
	if errResp.Error != "invalid API key" {
		t.Fatalf("invalid API key error = %q, want %q", errResp.Error, "invalid API key")
	}

	// Valid API key should return 200.
	resp = api.request(http.MethodGet, "/api/v1/accounts", nil,
		map[string]string{"Authorization": "Bearer " + createdKey.Key})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid API key status = %d, want 200; body: %s",
			resp.StatusCode, readBody(t, resp.Body))
	}

	// No API key but with CSRF token should return 200 (CSRF protection works).
	resp = api.request(http.MethodGet, "/api/v1/accounts", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("no key with CSRF token status = %d, want 200; body: %s",
			resp.StatusCode, readBody(t, resp.Body))
	}

	// List API keys with valid key should work.
	resp = api.request(http.MethodGet, "/api/v1/api-keys", nil,
		map[string]string{"Authorization": "Bearer " + createdKey.Key})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list API keys status = %d, want 200; body: %s",
			resp.StatusCode, readBody(t, resp.Body))
	}
}

func TestAPICRUDAndPages(t *testing.T) {
	api, _ := newTestAPI(t)
	seed := seedAPI(t, api)

	if seed.account.CategoryID != seed.category.ID || seed.account.CategoryName != "Work" {
		t.Fatalf("account category = %+v", seed.account)
	}
	if len(seed.account.Tags) != 1 || seed.account.Tags[0].Name != "Urgent" {
		t.Fatalf("account tags = %+v, want Urgent", seed.account.Tags)
	}
	if seed.first.AccountName != "Acme" || seed.first.CategoryName != "Work" {
		t.Fatalf("task account/category names = %+v", seed.first)
	}
	if seed.second.SortOrder <= seed.first.SortOrder {
		t.Fatalf("second task sort = %v, want after %v", seed.second.SortOrder, seed.first.SortOrder)
	}

	resp := api.jsonRequest(http.MethodGet, "/api/v1/categories", nil, http.StatusOK)
	var categories []db.Category
	decode(t, resp.Body, &categories)
	resp.Body.Close()
	if len(categories) != 1 || categories[0].Name != "Work" {
		t.Fatalf("categories = %+v", categories)
	}

	resp = api.jsonRequest(http.MethodGet, "/api/v1/accounts?tag="+fmt.Sprint(seed.tag.ID), nil, http.StatusOK)
	var accounts []db.Account
	decode(t, resp.Body, &accounts)
	resp.Body.Close()
	if len(accounts) != 1 || accounts[0].Name != "Acme" {
		t.Fatalf("accounts by tag = %+v", accounts)
	}

	resp = api.jsonRequest(http.MethodGet, "/api/v1/tasks?tag="+fmt.Sprint(seed.tag.ID), nil, http.StatusOK)
	var tasks []db.Task
	decode(t, resp.Body, &tasks)
	resp.Body.Close()
	if len(tasks) != 2 {
		t.Fatalf("tasks by tag = %d, want 2", len(tasks))
	}

	resp = api.jsonRequest(http.MethodGet, "/api/v1/accounts?q=acm&csm=Alice&ae=Bob&churn=true", nil, http.StatusOK)
	var filteredAccounts []db.Account
	decode(t, resp.Body, &filteredAccounts)
	resp.Body.Close()
	if len(filteredAccounts) != 1 || filteredAccounts[0].ID != seed.account.ID {
		t.Fatalf("account field filter = %+v", filteredAccounts)
	}

	resp = api.jsonRequest(http.MethodGet, "/api/v1/tasks?q=first+notes&csm=Alice&ae=Bob&churn=true", nil, http.StatusOK)
	var filteredTasks []db.Task
	decode(t, resp.Body, &filteredTasks)
	resp.Body.Close()
	if len(filteredTasks) != 1 || filteredTasks[0].ID != seed.first.ID {
		t.Fatalf("task field filter = %+v", filteredTasks)
	}

	status, page := api.page("/?q=acm&csm=Alice&ae=Bob&churn=true")
	if status != http.StatusOK {
		t.Fatalf("GET / filter status = %d, want 200", status)
	}
	if !strings.Contains(page, "data-filter-form") || !strings.Contains(page, ">Acme<") {
		t.Fatal("accounts page does not render the filter form or the matching account")
	}
	status, page = api.page("/?q=missing")
	if status != http.StatusOK {
		t.Fatalf("GET / no-match status = %d, want 200", status)
	}
	if strings.Contains(page, ">Acme<") {
		t.Fatal("accounts page shows a non-matching account")
	}
	// The account create/edit dialogs must keep every category even when the
	// page body is filtered. The filter bar renders chips, not options, so this
	// exact form matches only the two dialog selects.
	categoryOption := fmt.Sprintf(`<option value="%d">Work</option>`, seed.category.ID)
	if got := strings.Count(page, categoryOption); got != 2 {
		t.Fatalf("filtered page renders %d category dialog options, want 2", got)
	}

	status, page = api.page(fmt.Sprintf("/?category_id=%d", seed.category.ID))
	if status != http.StatusOK || !strings.Contains(page, ">Acme<") {
		t.Fatalf("category page filter failed (status %d)", status)
	}
	status, page = api.page("/?csm=alice")
	if status != http.StatusOK || !strings.Contains(page, `name="csm" value="Alice" checked`) {
		t.Fatalf("case-insensitive CSM filter is not reflected in the chips (status %d)", status)
	}
	status, page = api.page("/?csm=Alice&csm=alice&category_id=" + fmt.Sprint(seed.category.ID))
	if status != http.StatusOK || !strings.Contains(page, ">Acme<") {
		t.Fatalf("repeated filter params failed (status %d)", status)
	}

	status, page = api.page("/tasks?q=second")
	if status != http.StatusOK {
		t.Fatalf("GET /tasks filter status = %d, want 200", status)
	}
	if !strings.Contains(page, "Second task") || strings.Contains(page, "First task") {
		t.Fatal("task page search did not narrow the task list")
	}

	// Cross-date drag moves are rejected; same-date reorders are accepted.
	crossDate := fmt.Sprintf(`{"due_date":"2030-06-02","sort_order":0}`)
	expectStatus(t, api.request(http.MethodPatch,
		fmt.Sprintf("/api/v1/tasks/%d/reorder", seed.second.ID), []byte(crossDate), nil), http.StatusConflict)

	reorder := fmt.Sprintf(`{"due_date":"2030-06-01","sort_order":%v}`, seed.first.SortOrder-1)
	resp = api.request(http.MethodPatch, fmt.Sprintf("/api/v1/tasks/%d/reorder", seed.second.ID), []byte(reorder), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("same-date reorder status = %d, want 200; body: %s", resp.StatusCode, readBody(t, resp.Body))
	}
	var reordered db.Task
	decode(t, resp.Body, &reordered)
	resp.Body.Close()
	if reordered.SortOrder >= seed.first.SortOrder {
		t.Fatalf("reordered sort = %v, want before %v", reordered.SortOrder, seed.first.SortOrder)
	}

	// Completion sets completed_at and feeds the completed view.
	resp = api.request(http.MethodPut, fmt.Sprintf("/api/v1/tasks/%d", seed.first.ID),
		[]byte(`{"completed":true}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("complete task status = %d, want 200; body: %s", resp.StatusCode, readBody(t, resp.Body))
	}
	var completed db.Task
	decode(t, resp.Body, &completed)
	resp.Body.Close()
	if !completed.Completed || completed.CompletedAt == nil {
		t.Fatalf("completed task = %+v", completed)
	}
	if completed.Title != "First task" || completed.Details != "first notes" {
		t.Fatalf("partial update overwrote fields: %+v", completed)
	}

	status, page = api.page("/tasks?status=completed&q=first")
	if status != http.StatusOK {
		t.Fatalf("GET /tasks?status=completed status = %d, want 200", status)
	}
	if !strings.Contains(page, "First task") {
		t.Fatal("completed tasks page does not show the completed task")
	}

	resp = api.jsonRequest(http.MethodGet, "/api/v1/tasks?completed=1", nil, http.StatusOK)
	var completedTasks []db.Task
	decode(t, resp.Body, &completedTasks)
	resp.Body.Close()
	if len(completedTasks) != 1 || completedTasks[0].ID != seed.first.ID {
		t.Fatalf("completed filter = %+v", completedTasks)
	}
	resp = api.jsonRequest(http.MethodGet, "/api/v1/tasks?hide_done=1", nil, http.StatusOK)
	var openTasks []db.Task
	decode(t, resp.Body, &openTasks)
	resp.Body.Close()
	if len(openTasks) != 1 || openTasks[0].ID != seed.second.ID {
		t.Fatalf("hide_done filter = %+v", openTasks)
	}
	resp = api.jsonRequest(http.MethodGet, "/api/v1/tasks", nil, http.StatusOK)
	var defaultTasks []db.Task
	decode(t, resp.Body, &defaultTasks)
	resp.Body.Close()
	if len(defaultTasks) != 1 || defaultTasks[0].ID != seed.second.ID {
		t.Fatalf("default task list = %+v, want only open tasks", defaultTasks)
	}
	resp = api.jsonRequest(http.MethodGet, "/api/v1/tasks?include_done=1", nil, http.StatusOK)
	var allTasks []db.Task
	decode(t, resp.Body, &allTasks)
	resp.Body.Close()
	if len(allTasks) != 2 {
		t.Fatalf("include_done task list = %d, want 2", len(allTasks))
	}

	// Tags can update both name and color.
	resp = api.request(http.MethodPut, fmt.Sprintf("/api/v1/tags/%d", seed.tag.ID),
		[]byte(`{"name":"Renamed","color":"#00ff00"}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("edit tag status = %d, want 200; body: %s", resp.StatusCode, readBody(t, resp.Body))
	}
	var renamed db.Tag
	decode(t, resp.Body, &renamed)
	resp.Body.Close()
	if renamed.Name != "Renamed" || renamed.Color != "#00ff00" {
		t.Fatalf("edited tag = %+v", renamed)
	}

	// Account partial update preserves unspecified fields.
	resp = api.request(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d", seed.account.ID),
		[]byte(`{"details":"updated notes"}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update account status = %d, want 200; body: %s", resp.StatusCode, readBody(t, resp.Body))
	}
	var updatedAccount db.Account
	decode(t, resp.Body, &updatedAccount)
	resp.Body.Close()
	if updatedAccount.Notes != "updated notes" || updatedAccount.SFDC != "https://acme.example" {
		t.Fatalf("updated account = %+v", updatedAccount)
	}

	// Category collapse state is editable.
	resp = api.request(http.MethodPut, fmt.Sprintf("/api/v1/categories/%d", seed.category.ID),
		[]byte(`{"expanded":false}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update category status = %d, want 200; body: %s", resp.StatusCode, readBody(t, resp.Body))
	}
	var updatedCategory db.Category
	decode(t, resp.Body, &updatedCategory)
	resp.Body.Close()
	if updatedCategory.Expanded || updatedCategory.Name != "Work" {
		t.Fatalf("updated category = %+v", updatedCategory)
	}

	expectPageContains(t, api, "/", "Work")
	status, indexBody := api.page("/")
	if status != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", status)
	}
	if strings.Contains(indexBody, "Acme") {
		t.Fatal("collapsed category still renders its accounts")
	}
	expectPageContains(t, api, "/tasks?status=completed", "First task")
	status, tasksBody := api.page("/tasks")
	if status != http.StatusOK {
		t.Fatalf("GET /tasks status = %d, want 200", status)
	}
	if strings.Contains(tasksBody, "First task") {
		t.Fatal("due-date view shows a completed task without status=completed")
	}
	expectPageContains(t, api, "/tasks?status=completed", "First task")
	expectPageContains(t, api, "/tags", "Renamed")
	expectPageContains(t, api, "/archive", "Archive")
	expectPageContains(t, api, "/archive", "Archive")

	// Validation failures map to 400/404, never 500.
	expectStatus(t, api.request(http.MethodPost, "/api/v1/tasks",
		[]byte(`{"account_id":`+fmt.Sprint(seed.account.ID)+`,"title":"Bad","due_date":"06/01/2030"}`), nil),
		http.StatusBadRequest)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/tasks",
		[]byte(`{"account_id":99999,"title":"Missing account","due_date":"2030-06-01"}`), nil),
		http.StatusNotFound)
	expectStatus(t, api.request(http.MethodPatch,
		fmt.Sprintf("/api/v1/tasks/%d/reorder", seed.first.ID),
		[]byte(`{"due_date":"not-a-date","sort_order":1}`), nil), http.StatusBadRequest)
}

func TestAPITrashAndCSV(t *testing.T) {
	api, _ := newTestAPI(t)
	seed := seedAPI(t, api)

	// Active-only export omits a soft-deleted task.
	expectStatus(t, api.request(http.MethodDelete,
		fmt.Sprintf("/api/v1/tasks/%d", seed.second.ID), nil, nil), http.StatusOK)

	resp := api.request(http.MethodGet, "/api/v1/export?active_only=1&format=csv", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("active-only export status = %d, want 200", resp.StatusCode)
	}
	activeCSV := readBody(t, resp.Body)
	resp.Body.Close()
	if !strings.HasPrefix(activeCSV, "format_version,record_type,") {
		t.Fatalf("active-only export has unexpected header: %.60s", activeCSV)
	}
	if strings.Contains(activeCSV, "Second task") {
		t.Fatal("active-only export contains a soft-deleted task")
	}

	resp = api.request(http.MethodGet, "/api/v1/export?format=csv", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("full export status = %d, want 200", resp.StatusCode)
	}
	fullCSV := readBody(t, resp.Body)
	resp.Body.Close()
	if !strings.Contains(fullCSV, "Second task") {
		t.Fatal("full export is missing the soft-deleted task")
	}
	resp = api.jsonRequest(http.MethodPost, "/api/v1/archives/restore", map[string]any{
		"tasks": []int64{seed.second.ID},
	}, http.StatusOK)
	var result handlers.RestoreResponse
	decode(t, resp.Body, &result)
	resp.Body.Close()
	if len(result.Tasks) != 1 || result.Tasks[0].Status != "restored" {
		t.Fatalf("bulk restore task = %+v, want restored", result.Tasks)
	}

	// Deleting a category hides its whole subtree and shows it in the trash.
	expectStatus(t, api.request(http.MethodDelete,
		fmt.Sprintf("/api/v1/categories/%d", seed.category.ID), nil, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodGet,
		fmt.Sprintf("/api/v1/tasks/%d", seed.first.ID), nil, nil), http.StatusNotFound)

	resp = api.request(http.MethodGet, "/api/v1/trash", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/trash status = %d, want 200", resp.StatusCode)
	}
	var trash struct {
		Categories []db.Category `json:"categories"`
		Accounts   []db.Account  `json:"accounts"`
		Tasks      []db.Task     `json:"tasks"`
	}
	decode(t, resp.Body, &trash)
	resp.Body.Close()
	if len(trash.Categories) != 1 || len(trash.Accounts) != 1 || len(trash.Tasks) != 2 {
		t.Fatalf("trash counts = %d/%d/%d, want 1/1/2",
			len(trash.Categories), len(trash.Accounts), len(trash.Tasks))
	}

	expectPageContains(t, api, "/archive", "Acme")
	status, indexBody := api.page("/")
	if status != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", status)
	}
	if strings.Contains(indexBody, "Acme") {
		t.Fatal("index page still shows an account from a deleted category")
	}

	// Restore the category together with its children via bulk restore.
	resp = api.jsonRequest(http.MethodPost, "/api/v1/archives/restore", map[string]any{
		"categories":    []int64{seed.category.ID},
		"with_children": true,
	}, http.StatusOK)
	decode(t, resp.Body, &result)
	resp.Body.Close()
	if len(result.Categories) != 1 || result.Categories[0].Status != "restored" {
		t.Fatalf("bulk restore category = %+v, want restored", result.Categories)
	}
	expectStatus(t, api.request(http.MethodGet,
		fmt.Sprintf("/api/v1/tasks/%d", seed.first.ID), nil, nil), http.StatusOK)

	// Export headers and body are usable for a round trip.
	resp = api.request(http.MethodGet, "/api/v1/export?format=json", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export status = %d, want 200", resp.StatusCode)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Fatalf("export Content-Type = %q, want application/json", contentType)
	}
	if disposition := resp.Header.Get("Content-Disposition"); !strings.Contains(disposition, "attachment") {
		t.Fatalf("export Content-Disposition = %q, want attachment", disposition)
	}
	exported := readBody(t, resp.Body)
	resp.Body.Close()

	// CSV export for multipart import test.
	csvResp := api.request(http.MethodGet, "/api/v1/export?format=csv", nil, nil)
	if csvResp.StatusCode != http.StatusOK {
		t.Fatalf("csv export status = %d, want 200", csvResp.StatusCode)
	}
	csvBody := readBody(t, csvResp.Body)
	csvResp.Body.Close()

	// Multipart file upload uses the same import path.
	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	part, err := writer.CreateFormFile("file", "mirador.csv")
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write([]byte(csvBody)); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	resp = api.request(http.MethodPost, "/api/v1/import?conflict=duplicate", upload.Bytes(),
		map[string]string{"Content-Type": writer.FormDataContentType()})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("multipart import status = %d, want 200; body: %s", resp.StatusCode, readBody(t, resp.Body))
	}
	var importResult2 struct {
		Categories int `json:"categories"`
		Tags       int `json:"tags"`
		Accounts   int `json:"accounts"`
		Tasks      int `json:"tasks"`
	}
	decode(t, resp.Body, &importResult2)
	resp.Body.Close()
	if importResult2.Categories != 1 || importResult2.Tags != 1 || importResult2.Accounts != 1 || importResult2.Tasks != 2 {
		t.Fatalf("multipart import result = %+v, want 1/1/1/2", importResult2)
	}

	expectStatus(t, api.request(http.MethodPost, "/api/v1/import?conflict=merge",
		[]byte(exported), map[string]string{"Content-Type": "text/csv"}), http.StatusBadRequest)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/import?conflict=duplicate",
		[]byte("not,a,valid,export\n"), map[string]string{"Content-Type": "text/csv"}), http.StatusBadRequest)

	// Permanent deletes bypass the trash.
	extra := createJSON[db.Task](t, api, "/api/v1/tasks", map[string]any{
		"account_id": seed.account.ID,
		"title":      "Delete forever",
		"due_date":   "2030-06-03",
	})
	expectStatus(t, api.request(http.MethodDelete,
		fmt.Sprintf("/api/v1/tasks/%d?permanent=1", extra.ID), nil, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodGet,
		fmt.Sprintf("/api/v1/tasks/%d", extra.ID), nil, nil), http.StatusNotFound)

	extraCategory := createJSON[db.Category](t, api, "/api/v1/categories", map[string]any{
		"name": "Temporary",
	})
	expectStatus(t, api.request(http.MethodDelete,
		fmt.Sprintf("/api/v1/categories/%d?permanent=1", extraCategory.ID), nil, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodGet,
		fmt.Sprintf("/api/v1/categories/%d", extraCategory.ID), nil, nil), http.StatusNotFound)

	// Restoring an active category via bulk restore is skipped, not an error.
	resp = api.jsonRequest(http.MethodPost, "/api/v1/archives/restore", map[string]any{
		"categories": []int64{seed.category.ID},
	}, http.StatusOK)
	decode(t, resp.Body, &result)
	resp.Body.Close()
	if len(result.Categories) != 1 || result.Categories[0].Status != "skipped" || result.Categories[0].Reason != "not in trash" {
		t.Fatalf("bulk restore active category = %+v, want skipped", result.Categories)
	}
}

func TestAPIBulkRestore(t *testing.T) {
	api, _ := newTestAPI(t)
	seed := seedAPI(t, api)

	// Archive the account (cascades to its tasks) and the category.
	expectStatus(t, api.request(http.MethodDelete,
		fmt.Sprintf("/api/v1/accounts/%d", seed.account.ID), nil, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodDelete,
		fmt.Sprintf("/api/v1/categories/%d", seed.category.ID), nil, nil), http.StatusOK)

	// Bulk restore all archived records by ID.
	resp := api.jsonRequest(http.MethodPost, "/api/v1/archives/restore", map[string]any{
		"categories": []int64{seed.category.ID},
		"accounts":   []int64{seed.account.ID},
		"tasks":      []int64{seed.first.ID, seed.second.ID},
	}, http.StatusOK)
	var result handlers.RestoreResponse
	decode(t, resp.Body, &result)
	resp.Body.Close()
	if len(result.Categories) != 1 || result.Categories[0].Status != "restored" || result.Categories[0].ID != seed.category.ID {
		t.Fatalf("categories restore = %+v", result.Categories)
	}
	if len(result.Accounts) != 1 || result.Accounts[0].Status != "restored" || result.Accounts[0].ID != seed.account.ID {
		t.Fatalf("accounts restore = %+v", result.Accounts)
	}
	if len(result.Tasks) != 2 || result.Tasks[0].Status != "restored" || result.Tasks[1].Status != "restored" {
		t.Fatalf("tasks restore = %+v", result.Tasks)
	}

	// Restoring already-active records is skipped, not an error.
	resp = api.jsonRequest(http.MethodPost, "/api/v1/archives/restore", map[string]any{
		"categories": []int64{seed.category.ID},
		"accounts":   []int64{seed.account.ID},
		"tasks":      []int64{seed.first.ID, seed.second.ID},
	}, http.StatusOK)
	decode(t, resp.Body, &result)
	resp.Body.Close()
	if len(result.Categories) != 1 || result.Categories[0].Status != "skipped" || result.Categories[0].Reason != "not in trash" {
		t.Fatalf("categories skip = %+v", result.Categories)
	}
	if len(result.Accounts) != 1 || result.Accounts[0].Status != "skipped" || result.Accounts[0].Reason != "not in trash" {
		t.Fatalf("accounts skip = %+v", result.Accounts)
	}
	if len(result.Tasks) != 2 || result.Tasks[0].Status != "skipped" || result.Tasks[1].Status != "skipped" {
		t.Fatalf("tasks skip = %+v", result.Tasks)
	}

	// Non-existent IDs are skipped as "not found".
	resp = api.jsonRequest(http.MethodPost, "/api/v1/archives/restore", map[string]any{
		"categories": []int64{999999},
		"accounts":   []int64{999998},
		"tasks":      []int64{999997},
	}, http.StatusOK)
	decode(t, resp.Body, &result)
	resp.Body.Close()
	if len(result.Categories) != 1 || result.Categories[0].Status != "skipped" || result.Categories[0].Reason != "not found" {
		t.Fatalf("categories not found = %+v", result.Categories)
	}
	if len(result.Accounts) != 1 || result.Accounts[0].Status != "skipped" || result.Accounts[0].Reason != "not found" {
		t.Fatalf("accounts not found = %+v", result.Accounts)
	}
	if len(result.Tasks) != 1 || result.Tasks[0].Status != "skipped" || result.Tasks[0].Reason != "not found" {
		t.Fatalf("tasks not found = %+v", result.Tasks)
	}

	// Empty body restores nothing and returns empty lists.
	resp = api.jsonRequest(http.MethodPost, "/api/v1/archives/restore", map[string]any{}, http.StatusOK)
	decode(t, resp.Body, &result)
	resp.Body.Close()
	if len(result.Categories) != 0 || len(result.Accounts) != 0 || len(result.Tasks) != 0 {
		t.Fatalf("empty body = %+v", result)
	}
}

func TestAPIJSONExportImport(t *testing.T) {
	api, _ := newTestAPI(t)
	seedAPI(t, api)

	// JSON export.
	resp := api.request(http.MethodGet, "/api/v1/export?format=json", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("JSON export status = %d, want 200", resp.StatusCode)
	}
	exported := readBody(t, resp.Body)
	resp.Body.Close()
	if contentType := resp.Header.Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Fatalf("JSON export Content-Type = %q, want application/json", contentType)
	}
	var jsonData struct {
		FormatVersion int           `json:"format_version"`
		Categories    []db.Category `json:"categories"`
		Tags          []db.Tag      `json:"tags"`
		Accounts      []db.Account  `json:"accounts"`
		Tasks         []db.Task     `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(exported), &jsonData); err != nil {
		t.Fatalf("parse JSON export: %v", err)
	}
	if jsonData.FormatVersion != 2 {
		t.Fatalf("format_version = %d, want 2", jsonData.FormatVersion)
	}
	if len(jsonData.Categories) != 1 || len(jsonData.Tags) != 1 || len(jsonData.Accounts) != 1 || len(jsonData.Tasks) != 2 {
		t.Fatalf("JSON export counts = %d/%d/%d/%d, want 1/1/1/2",
			len(jsonData.Categories), len(jsonData.Tags), len(jsonData.Accounts), len(jsonData.Tasks))
	}
	// Tags are embedded on accounts.
	acme := findAccountByNameSlice(jsonData.Accounts, "Acme")
	if acme == nil {
		t.Fatal("acme account not found in JSON export")
	}
	if len(acme.Tags) != 1 || acme.Tags[0].Name != "Urgent" {
		t.Fatalf("acme tags = %+v", acme.Tags)
	}

	// JSON import into a fresh API.
	targetAPI, _ := newTestAPI(t)
	jsonBody, err := json.Marshal(jsonData)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	resp = targetAPI.request(http.MethodPost, "/api/v1/import?conflict=duplicate", jsonBody,
		map[string]string{"Content-Type": "application/json"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("JSON import status = %d, want 200; body: %s", resp.StatusCode, readBody(t, resp.Body))
	}
	var result struct {
		Categories int `json:"categories"`
		Tags       int `json:"tags"`
		Accounts   int `json:"accounts"`
		Tasks      int `json:"tasks"`
	}
	decode(t, resp.Body, &result)
	resp.Body.Close()
	if result.Categories != 1 || result.Tags != 1 || result.Accounts != 1 || result.Tasks != 2 {
		t.Fatalf("JSON import result = %+v, want 1/1/1/2", result)
	}

	// Verify imported data via API.
	resp = targetAPI.request(http.MethodGet, "/api/v1/accounts", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list accounts status = %d, want 200", resp.StatusCode)
	}
	var importedAccounts []db.Account
	decode(t, resp.Body, &importedAccounts)
	resp.Body.Close()
	if len(importedAccounts) != 1 || importedAccounts[0].Name != "Acme" {
		t.Fatalf("imported accounts = %+v, want one Acme", importedAccounts)
	}
	if len(importedAccounts[0].Tags) != 1 || importedAccounts[0].Tags[0].Name != "Urgent" {
		t.Fatalf("imported acme tags = %+v", importedAccounts[0].Tags)
	}
}

func findAccountByNameSlice(accounts []db.Account, name string) *db.Account {
	for i := range accounts {
		if accounts[i].Name == name {
			return &accounts[i]
		}
	}
	return nil
}

func TestRestorePerResourceEndpoints(t *testing.T) {
	api, _ := newTestAPI(t)
	category := createJSON[db.Category](t, api, "/api/v1/categories", map[string]any{"name": "Work"})
	account := createJSON[db.Account](t, api, "/api/v1/accounts", map[string]any{
		"category_id": category.ID, "name": "Acme",
	})
	task := createJSON[db.Task](t, api, "/api/v1/tasks", map[string]any{
		"account_id": account.ID, "title": "Task", "due_date": "2030-06-01",
	})

	// Soft delete account (cascades to its task).
	expectStatus(t, api.request(http.MethodDelete,
		fmt.Sprintf("/api/v1/accounts/%d", account.ID), nil, nil), http.StatusOK)

	// Restore task via per-resource endpoint (task was cascaded into trash).
	// Restoring a task also restores its parent account if it's archived,
	// otherwise the task would be invisible (task queries join accounts).
	resp := api.jsonRequest(http.MethodPost,
		fmt.Sprintf("/api/v1/tasks/%d/restore", task.ID), nil, http.StatusOK)
	var restoredTask db.Task
	decode(t, resp.Body, &restoredTask)
	resp.Body.Close()
	if restoredTask.ID != task.ID || restoredTask.DeletedAt != nil {
		t.Fatalf("task not restored via /tasks/{id}/restore: %+v", restoredTask)
	}

	// The account should also have been restored as a side effect.
	var restoredAccount db.Account
	resp = api.jsonRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/accounts/%d", account.ID), nil, http.StatusOK)
	decode(t, resp.Body, &restoredAccount)
	resp.Body.Close()
	if restoredAccount.ID != account.ID || restoredAccount.DeletedAt != nil {
		t.Fatalf("account not restored via task restore: %+v", restoredAccount)
	}
}

func TestAccountTagsSerializeAndUpdate(t *testing.T) {
	api, _ := newTestAPI(t)
	category := createJSON[db.Category](t, api, "/api/v1/categories", map[string]any{"name": "Work"})
	tag := createJSON[db.Tag](t, api, "/api/v1/tags", map[string]any{"name": "Urgent"})

	// Account created without tags serializes tags as empty array, not null.
	account := createJSON[db.Account](t, api, "/api/v1/accounts", map[string]any{
		"category_id": category.ID, "name": "Acme",
	})
	if account.Tags == nil {
		t.Fatal("account tags is null, want empty array")
	}

	// Add a tag via PUT.
	resp := api.jsonRequest(http.MethodPut,
		fmt.Sprintf("/api/v1/accounts/%d", account.ID),
		map[string]any{"tags": []map[string]any{{"id": tag.ID, "name": "Urgent"}}}, http.StatusOK)
	var updated db.Account
	decode(t, resp.Body, &updated)
	resp.Body.Close()
	if len(updated.Tags) != 1 || updated.Tags[0].Name != "Urgent" {
		t.Fatalf("tags not updated on PUT: %+v", updated.Tags)
	}

	// Clear tags via PUT with empty array.
	resp = api.jsonRequest(http.MethodPut,
		fmt.Sprintf("/api/v1/accounts/%d", account.ID),
		map[string]any{"tags": []map[string]any{}}, http.StatusOK)
	decode(t, resp.Body, &updated)
	resp.Body.Close()
	if len(updated.Tags) != 0 {
		t.Fatalf("tags not cleared on PUT: %+v", updated.Tags)
	}
}

func TestAccountRenewalDate(t *testing.T) {
	api, _ := newTestAPI(t)
	category := createJSON[db.Category](t, api, "/api/v1/categories", map[string]any{"name": "Work"})

	expectStatus(t, api.request(http.MethodPost, "/api/v1/accounts",
		[]byte(`{"category_id":`+fmt.Sprint(category.ID)+`,"name":"Bad","renewal_date":"03/15/2031"}`), nil),
		http.StatusBadRequest)

	account := createJSON[db.Account](t, api, "/api/v1/accounts", map[string]any{
		"category_id": category.ID, "name": "Acme", "renewal_date": "2031-03-15",
	})
	if account.RenewalDate != "2031-03-15" {
		t.Fatalf("created renewal date = %q, want 2031-03-15", account.RenewalDate)
	}

	expectStatus(t, api.request(http.MethodPut,
		fmt.Sprintf("/api/v1/accounts/%d", account.ID),
		[]byte(`{"renewal_date":"not-a-date"}`), nil), http.StatusBadRequest)

	resp := api.jsonRequest(http.MethodPut,
		fmt.Sprintf("/api/v1/accounts/%d", account.ID),
		map[string]any{"renewal_date": "2032-01-01"}, http.StatusOK)
	var updated db.Account
	decode(t, resp.Body, &updated)
	resp.Body.Close()
	if updated.RenewalDate != "2032-01-01" {
		t.Fatalf("updated renewal date = %q, want 2032-01-01", updated.RenewalDate)
	}

	// Clearing the date is allowed and keeps the rest of the account intact.
	resp = api.jsonRequest(http.MethodPut,
		fmt.Sprintf("/api/v1/accounts/%d", account.ID),
		map[string]any{"renewal_date": ""}, http.StatusOK)
	decode(t, resp.Body, &updated)
	resp.Body.Close()
	if updated.RenewalDate != "" || updated.Name != "Acme" {
		t.Fatalf("cleared account = %+v, want empty renewal date and name Acme", updated)
	}
}

func TestJSONImportWithoutFormatVersion(t *testing.T) {
	api, _ := newTestAPI(t)
	category := createJSON[db.Category](t, api, "/api/v1/categories", map[string]any{"name": "Work"})
	account := createJSON[db.Account](t, api, "/api/v1/accounts", map[string]any{
		"category_id": category.ID, "name": "Acme",
	})
	createJSON[db.Task](t, api, "/api/v1/tasks", map[string]any{
		"account_id": account.ID, "title": "Task", "due_date": "2030-06-01",
	})

	// Export JSON, strip format_version, re-import into a fresh API.
	resp := api.jsonRequest(http.MethodGet, "/api/v1/export?format=json", nil, http.StatusOK)
	exported := readBody(t, resp.Body)
	resp.Body.Close()

	var raw map[string]any
	if err := json.Unmarshal([]byte(exported), &raw); err != nil {
		t.Fatalf("parse export: %v", err)
	}
	delete(raw, "format_version")
	body, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal import body: %v", err)
	}

	targetAPI, _ := newTestAPI(t)
	resp = targetAPI.request(http.MethodPost, "/api/v1/import?conflict=duplicate", body,
		map[string]string{"Content-Type": "application/json"})
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Verify imported data.
	resp = targetAPI.jsonRequest(http.MethodGet, "/api/v1/accounts", nil, http.StatusOK)
	var importedAccounts []db.Account
	decode(t, resp.Body, &importedAccounts)
	resp.Body.Close()
	if len(importedAccounts) != 1 || importedAccounts[0].Name != "Acme" {
		t.Fatalf("imported accounts = %+v, want one Acme", importedAccounts)
	}
}
