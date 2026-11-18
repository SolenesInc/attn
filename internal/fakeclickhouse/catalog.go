package fakeclickhouse

import (
	"encoding/json"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"
)

type database struct {
	relations map[string]*stored
}

type stored struct {
	table *table
	view  *view
}

type table struct {
	columns    []column
	replacing  bool
	sortingKey []int
	rows       [][]any
}

type view struct {
	query   *selectQuery
	definer string
}

type user struct {
	name     string
	password string
	admin    bool
	grants   map[granted]bool
}

type granted struct{ privilege, database, table string }

func (u *user) require(privilege, databaseName, tableName string) {
	if u.admin || u.grants[granted{privilege, databaseName, tableName}] || u.grants[granted{privilege, databaseName, "*"}] {
		return
	}
	u.refuse(privilege + " ON " + databaseName + "." + tableName)
}

func (u *user) requireAdmin(privilege string) {
	if !u.admin {
		u.refuse(privilege)
	}
}

func (u *user) refuse(privilege string) {
	panic(failure(accessDenied, "%s: Not enough privileges. To execute this query, it's necessary to have the grant %s", u.name, privilege))
}

func (t *table) insert(block [][]any) {
	slices.SortStableFunc(block, func(a, b []any) int {
		for _, index := range t.sortingKey {
			if order := compareValues(a[index], b[index]); order != 0 {
				return order
			}
		}
		return 0
	})
	t.rows = append(t.rows, block...)
}

func (t *table) merged() [][]any {
	var latest [][]any
	position := map[string]int{}
	for _, row := range t.rows {
		key := make([]any, len(t.sortingKey))
		for i, index := range t.sortingKey {
			key[i] = row[index]
		}
		name := keyOf(key)
		if at, replaced := position[name]; replaced {
			latest[at] = row
			continue
		}
		position[name] = len(latest)
		latest = append(latest, row)
	}
	return latest
}

func (t *table) scope(name string) []scopeColumn {
	columns := make([]scopeColumn, len(t.columns))
	for i, every := range t.columns {
		columns[i] = scopeColumn{qualifier: name, name: every.name, typ: every.typ}
	}
	return columns
}

func (s *session) database() *database {
	return s.server.databases[s.databaseName]
}

func (s *session) writableTable(name, privilege string) *table {
	entry := s.database().relations[name]
	if entry == nil {
		panic(failure(unknownTable, "Table %s.%s does not exist", s.databaseName, name))
	}
	s.user.require(privilege, s.databaseName, name)
	if entry.view != nil {
		panic(unimplemented("%s on the view %s", privilege, name))
	}
	return entry.table
}

func (s *session) run(parsed statement) *result {
	switch parsed := parsed.(type) {
	case *selectQuery:
		return s.selected(parsed)
	case createDatabase:
		s.user.requireAdmin("CREATE DATABASE ON " + parsed.name + ".*")
		if s.server.databases[parsed.name] != nil {
			panic(failure(databaseAlreadyExists, "Database %s already exists", parsed.name))
		}
		s.server.databases[parsed.name] = &database{relations: map[string]*stored{}}
	case dropDatabase:
		s.user.requireAdmin("DROP DATABASE ON " + parsed.name + ".*")
		if s.server.databases[parsed.name] == nil && !parsed.ifExists {
			panic(failure(unknownDatabase, "Database %s does not exist", parsed.name))
		}
		delete(s.server.databases, parsed.name)
	case createUser:
		s.user.requireAdmin("CREATE USER ON " + parsed.name)
		if s.server.users[parsed.name] != nil {
			panic(failure(accessEntityExists, "user `%s`: cannot insert because user `%s` already exists in `local_directory`", parsed.name, parsed.name))
		}
		s.server.users[parsed.name] = &user{name: parsed.name, password: parsed.password, grants: map[granted]bool{}}
	case dropUser:
		s.dropUser(parsed)
	case grant:
		s.grant(parsed)
	case createTable:
		s.createTable(parsed)
	case createView:
		s.user.require("CREATE VIEW", s.databaseName, parsed.name)
		if s.database().relations[parsed.name] != nil {
			panic(failure(tableAlreadyExists, "Table %s.%s already exists", s.databaseName, parsed.name))
		}
		s.plan(parsed.query, s.user)
		s.database().relations[parsed.name] = &stored{view: &view{query: parsed.query, definer: s.user.name}}
	case dropView:
		s.user.require("DROP VIEW", s.databaseName, parsed.name)
		entry := s.database().relations[parsed.name]
		switch {
		case entry == nil && parsed.ifExists:
		case entry == nil:
			panic(failure(unknownTable, "Table %s.%s does not exist", s.databaseName, parsed.name))
		case entry.view == nil:
			panic(failure(incorrectQuery, "Table %s.%s is not a View", s.databaseName, parsed.name))
		default:
			delete(s.database().relations, parsed.name)
		}
	case insert:
		s.insert(parsed)
	case deleteRows:
		target := s.writableTable(parsed.table, "ALTER DELETE")
		condition := s.compiler(s.user, target.scope(parsed.table)).compile(parsed.where)
		requireCondition("WHERE", condition)
		target.rows = slices.DeleteFunc(slices.Clone(target.rows), func(row []any) bool { return isTrue(condition.eval(row)) })
	}
	return nil
}

func (s *session) selected(query *selectQuery) *result {
	if query.format != "" && query.format != formatJSONEachRow {
		panic(failure(unknownFormat, "Unknown format %s. %s", query.format, teach))
	}
	planned := s.plan(query, s.user)
	answer := &result{jsonEachRow: query.format == formatJSONEachRow, rows: planned.rows()}
	for _, every := range planned.columns {
		answer.columns = append(answer.columns, column{name: every.name, typ: every.typ})
	}
	return answer
}

func (s *session) dropUser(parsed dropUser) {
	s.user.requireAdmin("DROP USER ON " + parsed.name)
	if s.server.users[parsed.name] == nil {
		if parsed.ifExists {
			return
		}
		panic(failure(unknownUser, "There is no user `%s` in `user directories`", parsed.name))
	}
	var defined []string
	for databaseName, every := range s.server.databases {
		for name, entry := range every.relations {
			if entry.view != nil && entry.view.definer == parsed.name {
				defined = append(defined, "'"+databaseName+"."+name+"'")
			}
		}
	}
	if len(defined) > 0 {
		sort.Strings(defined)
		panic(failure(haveDependentObjects, "User `%s` is used as a definer in views [%s]", parsed.name, strings.Join(defined, ", ")))
	}
	delete(s.server.users, parsed.name)
}

func (s *session) grant(parsed grant) {
	on := " ON " + s.databaseName + "." + parsed.table + " WITH GRANT OPTION"
	s.user.requireAdmin(strings.Join(parsed.privileges, ", ") + on)
	grantee := s.server.users[parsed.user]
	if grantee == nil {
		panic(failure(unknownRole, "There is no role `%s` in `user directories`", parsed.user))
	}
	for _, privilege := range parsed.privileges {
		grantee.grants[granted{privilege, s.databaseName, parsed.table}] = true
	}
}

func (s *session) createTable(parsed createTable) {
	s.user.require("CREATE TABLE", s.databaseName, parsed.name)
	if s.database().relations[parsed.name] != nil {
		if parsed.ifNotExists {
			return
		}
		panic(failure(tableAlreadyExists, "Table %s.%s already exists", s.databaseName, parsed.name))
	}
	created := &table{columns: parsed.columns}
	switch parsed.engine {
	case "MergeTree":
	case "ReplacingMergeTree":
		created.replacing = true
	default:
		panic(unimplemented("the table engine %s", parsed.engine))
	}
	for _, name := range parsed.orderBy {
		index := slices.IndexFunc(created.columns, func(every column) bool { return every.name == name })
		if index < 0 {
			panic(failure(unknownIdentifier, "Missing columns: '%s' while processing the ORDER BY of %s", name, parsed.name))
		}
		if created.columns[index].typ.nullable {
			panic(unimplemented("the nullable sorting key column %s", name))
		}
		created.sortingKey = append(created.sortingKey, index)
	}
	if parsed.partitionBy != nil {
		s.compiler(s.user, created.scope(parsed.name)).compile(parsed.partitionBy)
	}
	s.database().relations[parsed.name] = &stored{table: created}
}

func (s *session) insert(parsed insert) {
	target := s.writableTable(parsed.table, "INSERT")
	receiving := target.columns
	positions := make([]int, len(target.columns))
	for i := range positions {
		positions[i] = i
	}
	if parsed.columns != nil {
		receiving, positions = nil, nil
		for _, name := range parsed.columns {
			index := slices.IndexFunc(target.columns, func(every column) bool { return every.name == name })
			if index < 0 {
				panic(failure(unknownIdentifier, "No such column %s in table %s.%s", name, s.databaseName, parsed.table))
			}
			receiving, positions = append(receiving, target.columns[index]), append(positions, index)
		}
	}
	var received [][]any
	switch parsed.format {
	case formatJSONEachRow:
		received = s.readJSONEachRow(receiving, parsed.data)
	case "":
		received = s.constantRows(receiving, parsed.values)
	default:
		panic(unimplemented("INSERT in the format %s, only JSONEachRow and VALUES", parsed.format))
	}
	block := make([][]any, len(received))
	for i, values := range received {
		block[i] = make([]any, len(target.columns))
		for index, every := range target.columns {
			block[i][index] = every.typ.zero()
		}
		for at, value := range values {
			block[i][positions[at]] = value
		}
	}
	target.insert(block)
}

func (s *session) constantRows(columns []column, written [][]expr) [][]any {
	constants := s.compiler(s.user, nil)
	rows := make([][]any, len(written))
	for i, values := range written {
		if len(values) != len(columns) {
			panic(failure(cannotParseInput, "Cannot parse input: row %d has %d values for %d columns", i+1, len(values), len(columns)))
		}
		rows[i] = make([]any, len(columns))
		for at, value := range values {
			made := constants.compile(value)
			rows[i][at] = coerce(made.eval(nil), made.typ, columns[at].typ, s.bestEffortDates)
		}
	}
	return rows
}

func (s *session) readJSONEachRow(columns []column, data string) [][]any {
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.UseNumber()
	var rows [][]any
	for {
		var fields map[string]any
		err := decoder.Decode(&fields)
		if err == io.EOF {
			return rows
		}
		if err != nil {
			panic(failure(cannotParseInput, "Cannot parse input: %v: (at row %d)", err, len(rows)+1))
		}
		row := make([]any, len(columns))
		for i, every := range columns {
			row[i] = every.typ.zero()
			if field := fields[every.name]; field != nil {
				value, ok := s.fromJSON(every.typ.kind, field)
				if !ok {
					panic(failure(cannotParseInput, "Cannot parse input: %v is not a %s: (while reading the value of key %s): (at row %d)", field, every.typ, every.name, len(rows)+1))
				}
				row[i] = value
			}
		}
		rows = append(rows, row)
	}
}

var unsignedMasks = map[kind]uint64{kindUInt32: 1<<32 - 1, kindUInt64: 1<<64 - 1}

func (s *session) fromJSON(target kind, field any) (any, bool) {
	text, isText := field.(string)
	number, isNumber := field.(json.Number)
	if isNumber {
		text = number.String()
	}
	switch target {
	case kindString:
		if boolean, isBool := field.(bool); isBool {
			return strconv.FormatBool(boolean), true
		}
		return text, isText || isNumber
	case kindInt64:
		whole, err := strconv.ParseInt(text, 10, 64)
		return whole, err == nil && (isText || isNumber)
	case kindUInt32, kindUInt64:
		if strings.HasPrefix(text, "-") {
			panic(failure(cannotParseNumber, "Unsigned type must not contain '-' symbol"))
		}
		whole, err := strconv.ParseUint(text, 10, 64)
		return whole & unsignedMasks[target], err == nil && (isText || isNumber)
	case kindFloat64:
		fraction, err := strconv.ParseFloat(text, 64)
		return fraction, err == nil && (isText || isNumber)
	case kindBool:
		if boolean, isBool := field.(bool); isBool {
			return boolean, true
		}
		return text == "1", isNumber && (text == "0" || text == "1")
	case kindDateTime64:
		if isNumber {
			milliseconds, err := strconv.ParseInt(text, 10, 64)
			return milliseconds, err == nil
		}
		milliseconds, ok := parseDateTime(text, s.bestEffortDates)
		return milliseconds, ok && isText
	default:
		return nil, false
	}
}
