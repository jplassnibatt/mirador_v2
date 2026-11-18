// Package templates embeds and parses the server-rendered HTML templates.
package templates

import (
	"embed"
	"html"
	"html/template"
	"regexp"
	"strconv"
	"strings"
	"time"

	"mirador/internal/db"
)

//go:embed *.html
var FS embed.FS

var pages = []string{
	"index.html",
	"tasks_due_date.html",
	"tags.html",
	"archive.html",
	"api.html",
	"api_docs.html",
}

var shared = []string{"layout.html", "partials.html"}

// Parse returns one template set per page. Each set contains the shared
// layout, partials, and the page's content block.
func Parse() (map[string]*template.Template, error) {
	sets := make(map[string]*template.Template, len(pages))
	for _, page := range pages {
		files := append(append([]string{}, shared...), page)
		t, err := template.New("layout").Funcs(FuncMap()).ParseFS(FS, files...)
		if err != nil {
			return nil, err
		}
		sets[page] = t
	}
	return sets, nil
}

// FuncMap returns the template helper functions.
func FuncMap() template.FuncMap {
	return template.FuncMap{
		"fmtDate":     fmtDate,
		"shortDate":   shortDate,
		"dueLabel":    dueLabel,
		"dueWeekday":  dueWeekday,
		"overdueDays": overdueDays,
		"linkify":     linkify,
		"add":         func(a, b int) int { return a + b },
		"sub":         func(a, b int) int { return a - b },
		"dict": func(values ...any) map[string]any {
			result := make(map[string]any, len(values)/2)
			for i := 0; i+1 < len(values); i += 2 {
				key, ok := values[i].(string)
				if ok {
					result[key] = values[i+1]
				}
			}
			return result
		},
		"join":  strings.Join,
		"today": func() string { return time.Now().Format("2006-01-02") },
		"tagIDs": func(tags []db.Tag) string {
			ids := make([]string, 0, len(tags))
			for _, tag := range tags {
				ids = append(ids, strconv.FormatInt(tag.ID, 10))
			}
			return strings.Join(ids, ",")
		},
		"fmtDeletedAt": fmtDeletedAt,
	}
}

func parseDate(value string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", value, time.Local)
}

func fmtDate(value string) string {
	t, err := parseDate(value)
	if err != nil {
		return value
	}
	return t.Format("Mon, Jan 2, 2006")
}

func shortDate(value string) string {
	t, err := parseDate(value)
	if err != nil {
		return value
	}
	return t.Format("Jan 2")
}

func dueLabel(value string) string {
	t, err := parseDate(value)
	if err != nil {
		return value
	}
	today := time.Now().In(time.Local)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.Local)
	switch {
	case t.Equal(today):
		return "Today"
	case t.Equal(today.AddDate(0, 0, 1)):
		return "Tomorrow"
	case t.Equal(today.AddDate(0, 0, -1)):
		return "Yesterday"
	}

	// Dates in the current Monday-Sunday week show the weekday name.
	weekday := (int(today.Weekday()) + 6) % 7 // Monday = 0
	weekStart := today.AddDate(0, 0, -weekday)
	weekEnd := weekStart.AddDate(0, 0, 6)
	if !t.Before(weekStart) && !t.After(weekEnd) {
		return t.Weekday().String()
	}

	// Everything else is "day month"; include the year whenever the due date
	// is outside the current calendar year.
	label := strconv.Itoa(t.Day()) + " " + t.Format("Jan")
	if t.Year() != today.Year() {
		label += " " + strconv.Itoa(t.Year())
	}
	return label
}

// dueWeekday returns the short weekday shown after a day/month due label.
// It is empty for Today/Yesterday/Tomorrow and for labels that already are
// weekday names, so those headings do not repeat the weekday.
func dueWeekday(value string) string {
	t, err := parseDate(value)
	if err != nil {
		return ""
	}
	label := dueLabel(value)
	switch label {
	case "Today", "Tomorrow", "Yesterday", t.Weekday().String():
		return ""
	}
	return t.Format("Mon")
}

func overdueDays(value string) int {
	t, err := parseDate(value)
	if err != nil {
		return 0
	}
	today := time.Now().In(time.Local)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.Local)
	days := int(today.Sub(t).Hours() / 24)
	if days < 0 {
		return 0
	}
	return days
}

// fmtDeletedAt formats an archived-at timestamp as "2006-01-02 15:04:05",
// dropping the sub-second precision that SQLite stores.
func fmtDeletedAt(value string) string {
	if len(value) >= 19 {
		return value[:19]
	}
	return value
}

var linkPattern = regexp.MustCompile(`^\[([^\]]*)\]\(([^)]*)\)$`)

// linkify turns http(s) URLs into safe external links. It supports the
// markdown-style [text](url) pattern so users can store a display name with
// the link, for example "[Google](https://www.google.com/?hl=es)". A plain URL
// renders as <a href="url">url</a>. Non-URL text is escaped and returned as
// plain text.
func linkify(value string) template.HTML {
	s := strings.TrimSpace(value)
	if s == "" {
		return ""
	}
	if matches := linkPattern.FindStringSubmatch(s); matches != nil {
		href := html.EscapeString(matches[2])
		label := html.EscapeString(matches[1])
		return template.HTML(`<a href="` + href + `" target="_blank" rel="noopener noreferrer">` + label + `</a>`)
	}
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		return template.HTML(html.EscapeString(s))
	}
	href := html.EscapeString(s)
	return template.HTML(`<a href="` + href + `" target="_blank" rel="noopener noreferrer">` + href + `</a>`)
}
