package fakeclickhouse

import (
	"fmt"
	"net/http"
)

const version = "25.8.33.6 (fakeclickhouse)"

const teach = "fakeclickhouse answers only the SQL that attn-ingest and its tests send: teach it more in internal/fakeclickhouse, and check the new behavior against a real ClickHouse"

const (
	cannotParseInput      = 27
	cannotParseDateTime   = 41
	wrongArgumentCount    = 42
	illegalArgumentType   = 43
	unknownFunction       = 46
	unknownIdentifier     = 47
	notImplemented        = 48
	tableAlreadyExists    = 57
	unknownTable          = 60
	syntaxError           = 62
	cannotParseNumber     = 72
	unknownFormat         = 73
	incorrectQuery        = 80
	unknownDatabase       = 81
	databaseAlreadyExists = 82
	unknownSetting        = 115
	illegalFinal          = 181
	illegalAggregation    = 184
	unknownUser           = 192
	requiredPassword      = 194
	notAnAggregate        = 215
	noCommonType          = 386
	cannotCompileRegexp   = 427
	unknownQueryParameter = 456
	accessEntityExists    = 493
	accessDenied          = 497
	unknownRole           = 511
	authenticationFailed  = 516
	haveDependentObjects  = 630
)

type errorKind struct {
	name   string
	status int
}

var errorKinds = map[int]errorKind{
	cannotParseInput:      {"CANNOT_PARSE_INPUT_ASSERTION_FAILED", http.StatusBadRequest},
	cannotParseDateTime:   {"CANNOT_PARSE_DATETIME", http.StatusBadRequest},
	wrongArgumentCount:    {"NUMBER_OF_ARGUMENTS_DOESNT_MATCH", http.StatusBadRequest},
	illegalArgumentType:   {"ILLEGAL_TYPE_OF_ARGUMENT", http.StatusInternalServerError},
	unknownFunction:       {"UNKNOWN_FUNCTION", http.StatusNotFound},
	unknownIdentifier:     {"UNKNOWN_IDENTIFIER", http.StatusNotFound},
	notImplemented:        {"NOT_IMPLEMENTED", http.StatusNotImplemented},
	tableAlreadyExists:    {"TABLE_ALREADY_EXISTS", http.StatusInternalServerError},
	unknownTable:          {"UNKNOWN_TABLE", http.StatusNotFound},
	syntaxError:           {"SYNTAX_ERROR", http.StatusBadRequest},
	cannotParseNumber:     {"CANNOT_PARSE_NUMBER", http.StatusBadRequest},
	unknownFormat:         {"UNKNOWN_FORMAT", http.StatusNotFound},
	incorrectQuery:        {"INCORRECT_QUERY", http.StatusInternalServerError},
	unknownDatabase:       {"UNKNOWN_DATABASE", http.StatusNotFound},
	databaseAlreadyExists: {"DATABASE_ALREADY_EXISTS", http.StatusInternalServerError},
	unknownSetting:        {"UNKNOWN_SETTING", http.StatusNotFound},
	illegalFinal:          {"ILLEGAL_FINAL", http.StatusInternalServerError},
	illegalAggregation:    {"ILLEGAL_AGGREGATION", http.StatusInternalServerError},
	unknownUser:           {"UNKNOWN_USER", http.StatusForbidden},
	requiredPassword:      {"REQUIRED_PASSWORD", http.StatusUnauthorized},
	notAnAggregate:        {"NOT_AN_AGGREGATE", http.StatusInternalServerError},
	noCommonType:          {"NO_COMMON_TYPE", http.StatusInternalServerError},
	cannotCompileRegexp:   {"CANNOT_COMPILE_REGEXP", http.StatusBadRequest},
	unknownQueryParameter: {"UNKNOWN_QUERY_PARAMETER", http.StatusInternalServerError},
	accessEntityExists:    {"ACCESS_ENTITY_ALREADY_EXISTS", http.StatusInternalServerError},
	accessDenied:          {"ACCESS_DENIED", http.StatusInternalServerError},
	unknownRole:           {"UNKNOWN_ROLE", http.StatusNotFound},
	authenticationFailed:  {"AUTHENTICATION_FAILED", http.StatusForbidden},
	haveDependentObjects:  {"HAVE_DEPENDENT_OBJECTS", http.StatusInternalServerError},
}

type queryError struct {
	code    int
	message string
}

func (e *queryError) Error() string {
	return fmt.Sprintf("Code: %d. DB::Exception: %s. (%s) (version %s)", e.code, e.message, errorKinds[e.code].name, version)
}

func failure(code int, format string, args ...any) *queryError {
	return &queryError{code: code, message: fmt.Sprintf(format, args...)}
}

func unimplemented(format string, args ...any) *queryError {
	return failure(notImplemented, "fakeclickhouse does not implement "+format+". "+teach, args...)
}

func failureOf(run func()) (failed *queryError) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		raised, ok := recovered.(*queryError)
		if !ok {
			panic(recovered)
		}
		failed = raised
	}()
	run()
	return nil
}
