package fakeclickhouse

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
)

const (
	AdminUser     = "attn"
	AdminPassword = "attn"
)

type Server struct {
	mu        sync.Mutex
	databases map[string]*database
	users     map[string]*user
}

func New() *Server {
	return &Server{
		databases: map[string]*database{"default": {relations: map[string]*stored{}}},
		users:     map[string]*user{AdminUser: {name: AdminUser, password: AdminPassword, admin: true}},
	}
}

type session struct {
	server          *Server
	user            *user
	databaseName    string
	parameters      map[string]string
	external        map[string]*table
	bestEffortDates bool
	isoDates        bool
}

type request struct {
	query    string
	settings url.Values
	uploads  map[string]string
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body strings.Builder
	contentType := "text/tab-separated-values; charset=UTF-8"
	failed := failureOf(func() {
		received := receive(r)
		s.mu.Lock()
		defer s.mu.Unlock()
		opened := s.open(r, received)
		if answer := opened.run(parse(received.query)); answer != nil {
			answer.write(&body, opened.isoDates)
			if answer.jsonEachRow {
				contentType = "application/x-ndjson; charset=UTF-8"
			}
		}
	})
	if failed != nil {
		w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
		w.Header()["X-ClickHouse-Exception-Code"] = []string{strconv.Itoa(failed.code)}
		w.WriteHeader(errorKinds[failed.code].status)
		fmt.Fprintln(w, failed.Error())
		return
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = io.WriteString(w, body.String())
}

func receive(r *http.Request) request {
	if r.Method != http.MethodPost {
		panic(unimplemented("the HTTP method %s, only POST", r.Method))
	}
	received := request{settings: r.URL.Query(), uploads: map[string]string{}}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			panic(failure(cannotParseInput, "Cannot read the request body: %v", err))
		}
		received.query = strings.TrimSpace(received.settings.Get("query") + "\n" + string(body))
		return received
	}
	parts, err := r.MultipartReader()
	if err != nil {
		panic(failure(cannotParseInput, "Cannot read the multipart request: %v", err))
	}
	for {
		part, err := parts.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(failure(cannotParseInput, "Cannot read the multipart request: %v", err))
		}
		data, err := io.ReadAll(part)
		if err != nil {
			panic(failure(cannotParseInput, "Cannot read the external table %s: %v", part.FormName(), err))
		}
		received.uploads[part.FormName()] = string(data)
	}
	received.query = received.settings.Get("query")
	return received
}

func (s *Server) open(r *http.Request, received request) *session {
	name, password, sent := r.BasicAuth()
	if !sent {
		name = "default"
	}
	account := s.users[name]
	if account == nil || account.password != password {
		code := authenticationFailed
		if !sent {
			code = requiredPassword
		}
		panic(failure(code, "%s: Authentication failed: password is incorrect, or there is no user with such name", name))
	}
	opened := &session{
		server:       s,
		user:         account,
		databaseName: "default",
		parameters:   map[string]string{},
		external:     map[string]*table{},
	}
	for setting, values := range received.settings {
		value := values[len(values)-1]
		parameterName, isParameter := strings.CutPrefix(setting, "param_")
		switch {
		case isParameter:
			opened.parameters[parameterName] = value
		case setting == "query":
		case setting == "database":
			opened.databaseName = value
		case setting == "date_time_input_format":
			opened.bestEffortDates = oneOf(setting, value, "basic", "best_effort") == "best_effort"
		case setting == "date_time_output_format":
			opened.isoDates = oneOf(setting, value, "simple", "iso") == "iso"
		case setting == "output_format_json_quote_64bit_integers", setting == "send_progress_in_http_headers":
			oneOf(setting, value, "0")
		case setting == "wait_end_of_query":
			oneOf(setting, value, "1")
		case describesUpload(setting, received.uploads):
		default:
			panic(failure(unknownSetting, "Setting %s is neither a builtin setting nor started with the prefix 'SQL_' registered for user-defined settings. %s", setting, teach))
		}
	}
	if s.databases[opened.databaseName] == nil {
		panic(failure(unknownDatabase, "Database %s does not exist", opened.databaseName))
	}
	for name, data := range received.uploads {
		if format := received.settings.Get(name + "_format"); format != formatJSONEachRow {
			panic(unimplemented("an external table in the format %q, only JSONEachRow", format))
		}
		columns := parseStructure(received.settings.Get(name + "_structure"))
		opened.external[name] = &table{columns: columns, rows: opened.readJSONEachRow(columns, data)}
	}
	return opened
}

func oneOf(setting, value string, accepted ...string) string {
	if !slices.Contains(accepted, value) {
		panic(unimplemented("the value %q of the setting %s, only %s", value, setting, strings.Join(accepted, " and ")))
	}
	return value
}

func describesUpload(setting string, uploads map[string]string) bool {
	for _, suffix := range []string{"_format", "_structure"} {
		if name, ok := strings.CutSuffix(setting, suffix); ok {
			if _, uploaded := uploads[name]; uploaded {
				return true
			}
		}
	}
	return false
}
