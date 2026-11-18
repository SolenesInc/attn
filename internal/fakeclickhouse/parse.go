package fakeclickhouse

import (
	"strconv"
	"strings"
)

type statement any

type selectQuery struct {
	distinct  bool
	items     []selectItem
	from      *source
	arrayJoin *arrayJoin
	join      *leftJoin
	where     expr
	groupBy   []expr
	orderBy   []expr
	limit     *uint64
	format    string
}

type selectItem struct {
	expr  expr
	alias string
	text  string
}

type source struct {
	table    string
	subquery *selectQuery
	alias    string
	final    bool
}

type arrayJoin struct {
	array expr
	alias string
}

type leftJoin struct {
	right source
	on    expr
}

type createDatabase struct{ name string }

type dropDatabase struct {
	name     string
	ifExists bool
}

type createTable struct {
	name        string
	ifNotExists bool
	columns     []column
	engine      string
	partitionBy expr
	orderBy     []string
}

type createView struct {
	name  string
	query *selectQuery
}

type dropView struct {
	name     string
	ifExists bool
}

type createUser struct{ name, password string }

type dropUser struct {
	name     string
	ifExists bool
}

type grant struct {
	privileges []string
	table      string
	user       string
}

type insert struct {
	table   string
	columns []string
	values  [][]expr
	format  string
	data    string
}

type deleteRows struct {
	table string
	where expr
}

type expr any

type literal struct {
	typ   dataType
	value any
}

type identifier struct{ qualifier, name string }

type call struct {
	name      string
	arguments []expr
}

type comparison struct {
	operator    string
	left, right expr
}

type logical struct {
	operator    string
	left, right expr
}

type tuple struct{ items []expr }

type membership struct {
	left  expr
	table string
}

type parameter struct {
	name string
	typ  dataType
}

var grantable = map[string]bool{
	"SELECT": true, "INSERT": true, "CREATE TABLE": true, "ALTER DELETE": true,
	"ALTER UPDATE": true, "CREATE VIEW": true, "DROP VIEW": true,
}

var clauseWords = map[string]bool{
	"SELECT": true, "FROM": true, "WHERE": true, "GROUP": true, "ORDER": true, "BY": true, "LIMIT": true,
	"FORMAT": true, "AS": true, "AND": true, "OR": true, "ON": true, "LEFT": true, "ARRAY": true, "JOIN": true, "IN": true,
}

type parser struct {
	source   string
	offset   int
	peeked   *token
	consumed int
}

func parse(source string) statement {
	p := &parser{source: source}
	var parsed statement
	switch {
	case p.peekKeyword("SELECT"):
		parsed = p.selectQuery(true)
	case p.keyword("CREATE"):
		parsed = p.create()
	case p.keyword("DROP"):
		parsed = p.drop()
	case p.keyword("GRANT"):
		parsed = p.grant()
	case p.keyword("INSERT"):
		return p.insert()
	case p.keyword("DELETE"):
		p.expectKeywords("FROM")
		table := p.word("a table name")
		p.expectKeywords("WHERE")
		parsed = deleteRows{table: table, where: p.expression()}
	default:
		panic(p.unexpected("SELECT, CREATE, DROP, GRANT, INSERT or DELETE"))
	}
	p.end()
	return parsed
}

func parseDataType(text string) dataType {
	p := &parser{source: text}
	parsed := p.dataType()
	p.end()
	return parsed
}

func parseStructure(text string) []column {
	p := &parser{source: text}
	columns := p.columns()
	p.end()
	return columns
}

func (p *parser) peek() token {
	if p.peeked == nil {
		next := scan(p.source, p.offset)
		p.peeked = &next
	}
	return *p.peeked
}

func (p *parser) take() token {
	next := p.peek()
	p.peeked = nil
	p.offset = next.end
	p.consumed = next.end
	return next
}

func (p *parser) unexpected(expected string) *queryError {
	return syntaxFailure(p.source, p.peek().start, expected)
}

func (p *parser) end() {
	if p.peek().kind != tokenEnd {
		panic(p.unexpected("the end of the query"))
	}
}

func (p *parser) peekKeyword(word string) bool {
	next := p.peek()
	return next.kind == tokenWord && strings.EqualFold(next.text, word)
}

func (p *parser) keyword(word string) bool {
	if !p.peekKeyword(word) {
		return false
	}
	p.take()
	return true
}

func (p *parser) expectKeywords(words ...string) {
	for _, word := range words {
		if !p.keyword(word) {
			panic(p.unexpected(word))
		}
	}
}

func (p *parser) peekSymbol(text string) bool {
	next := p.peek()
	return next.kind == tokenSymbol && next.text == text
}

func (p *parser) symbol(text string) bool {
	if !p.peekSymbol(text) {
		return false
	}
	p.take()
	return true
}

func (p *parser) expectSymbol(text string) {
	if !p.symbol(text) {
		panic(p.unexpected(text))
	}
}

func (p *parser) word(what string) string {
	if p.peek().kind != tokenWord {
		panic(p.unexpected(what))
	}
	return p.take().text
}

func (p *parser) stringLiteral(what string) string {
	if p.peek().kind != tokenString {
		panic(p.unexpected(what))
	}
	return p.take().text
}

func (p *parser) ifExists() bool {
	if !p.keyword("IF") {
		return false
	}
	p.expectKeywords("EXISTS")
	return true
}

func (p *parser) create() statement {
	switch {
	case p.keyword("DATABASE"):
		return createDatabase{name: p.word("a database name")}
	case p.keyword("USER"):
		name := p.word("a user name")
		p.expectKeywords("IDENTIFIED", "BY")
		return createUser{name: name, password: p.stringLiteral("a password")}
	case p.keyword("VIEW"):
		name := p.word("a view name")
		p.expectKeywords("DEFINER")
		p.expectSymbol("=")
		p.expectKeywords("CURRENT_USER", "SQL", "SECURITY", "DEFINER", "AS")
		return createView{name: name, query: p.selectQuery(false)}
	case p.keyword("TABLE"):
		return p.createTable()
	default:
		panic(p.unexpected("DATABASE, USER, VIEW or TABLE"))
	}
}

func (p *parser) createTable() statement {
	var table createTable
	if p.keyword("IF") {
		p.expectKeywords("NOT", "EXISTS")
		table.ifNotExists = true
	}
	table.name = p.word("a table name")
	p.expectSymbol("(")
	table.columns = p.columns()
	p.expectSymbol(")")
	p.expectKeywords("ENGINE")
	p.expectSymbol("=")
	table.engine = p.word("a table engine")
	if p.keyword("PARTITION") {
		p.expectKeywords("BY")
		table.partitionBy = p.expression()
	}
	p.expectKeywords("ORDER", "BY")
	if !p.symbol("(") {
		table.orderBy = []string{p.word("a column name")}
		return table
	}
	for {
		table.orderBy = append(table.orderBy, p.word("a column name"))
		if !p.symbol(",") {
			break
		}
	}
	p.expectSymbol(")")
	return table
}

func (p *parser) columns() []column {
	var columns []column
	for {
		name := p.word("a column name")
		columns = append(columns, column{name: name, typ: p.dataType()})
		if !p.symbol(",") {
			return columns
		}
	}
}

func (p *parser) dataType() dataType {
	name := p.word("a type")
	switch name {
	case "Nullable":
		p.expectSymbol("(")
		inner := p.dataType()
		p.expectSymbol(")")
		if inner.nullable {
			panic(unimplemented("the type Nullable(%s)", inner))
		}
		inner.nullable = true
		return inner
	case "LowCardinality":
		p.expectSymbol("(")
		inner := p.dataType()
		p.expectSymbol(")")
		if inner != (dataType{kind: kindString}) {
			panic(unimplemented("the type LowCardinality(%s)", inner))
		}
		return inner
	case "DateTime64":
		p.expectSymbol("(")
		precision := p.take()
		p.expectSymbol(",")
		zone := p.stringLiteral("a time zone")
		p.expectSymbol(")")
		if precision.text != "3" || zone != "UTC" {
			panic(unimplemented("the type DateTime64(%s, '%s'), only DateTime64(3, 'UTC')", precision.text, zone))
		}
		return dataType{kind: kindDateTime64}
	}
	scalar, known := scalarKinds[name]
	if !known {
		panic(unimplemented("the type %s", name))
	}
	return dataType{kind: scalar}
}

func (p *parser) drop() statement {
	switch {
	case p.keyword("DATABASE"):
		ifExists := p.ifExists()
		return dropDatabase{name: p.word("a database name"), ifExists: ifExists}
	case p.keyword("USER"):
		ifExists := p.ifExists()
		return dropUser{name: p.word("a user name"), ifExists: ifExists}
	case p.keyword("VIEW"):
		ifExists := p.ifExists()
		return dropView{name: p.word("a view name"), ifExists: ifExists}
	default:
		panic(p.unexpected("DATABASE, USER or VIEW"))
	}
}

func (p *parser) grant() statement {
	var granted grant
	for {
		privilege := strings.ToUpper(p.word("a privilege"))
		if privilege == "CREATE" || privilege == "ALTER" || privilege == "DROP" {
			privilege += " " + strings.ToUpper(p.word("a privilege"))
		}
		if !grantable[privilege] {
			panic(p.unexpected("one of the privileges SELECT, INSERT, CREATE TABLE, ALTER DELETE, ALTER UPDATE, CREATE VIEW, DROP VIEW"))
		}
		granted.privileges = append(granted.privileges, privilege)
		if !p.symbol(",") {
			break
		}
	}
	p.expectKeywords("ON")
	if p.symbol("*") {
		granted.table = "*"
	} else {
		granted.table = p.word("a table name")
	}
	p.expectKeywords("TO")
	granted.user = p.word("a user name")
	return granted
}

func (p *parser) insert() statement {
	p.expectKeywords("INTO")
	inserted := insert{table: p.word("a table name")}
	if p.symbol("(") {
		for {
			inserted.columns = append(inserted.columns, p.word("a column name"))
			if !p.symbol(",") {
				break
			}
		}
		p.expectSymbol(")")
	}
	if p.keyword("FORMAT") {
		inserted.format = p.word("a format")
		inserted.data = p.source[p.consumed:]
		return inserted
	}
	p.expectKeywords("VALUES")
	for {
		p.expectSymbol("(")
		inserted.values = append(inserted.values, p.expressions())
		p.expectSymbol(")")
		if !p.symbol(",") {
			break
		}
	}
	p.end()
	return inserted
}

func (p *parser) selectQuery(topLevel bool) *selectQuery {
	p.expectKeywords("SELECT")
	query := &selectQuery{distinct: p.keyword("DISTINCT")}
	for {
		query.items = append(query.items, p.selectItem())
		if !p.symbol(",") {
			break
		}
	}
	if p.keyword("FROM") {
		from := p.tableExpression()
		query.from = &from
		if p.keyword("ARRAY") {
			p.expectKeywords("JOIN")
			array := p.expression()
			p.expectKeywords("AS")
			query.arrayJoin = &arrayJoin{array: array, alias: p.word("an alias")}
		}
		if p.keyword("LEFT") {
			p.expectKeywords("JOIN")
			right := p.tableExpression()
			p.expectKeywords("ON")
			query.join = &leftJoin{right: right, on: p.expression()}
		}
	}
	if p.keyword("WHERE") {
		query.where = p.expression()
	}
	if p.keyword("GROUP") {
		p.expectKeywords("BY")
		query.groupBy = p.expressions()
	}
	if p.keyword("ORDER") {
		p.expectKeywords("BY")
		query.orderBy = p.expressions()
	}
	if p.keyword("LIMIT") {
		if p.peek().kind != tokenNumber {
			panic(p.unexpected("a row count"))
		}
		limit, err := strconv.ParseUint(p.take().text, 10, 64)
		if err != nil {
			panic(p.unexpected("a row count"))
		}
		query.limit = &limit
	}
	if topLevel && p.keyword("FORMAT") {
		query.format = p.word("a format")
	}
	return query
}

func (p *parser) selectItem() selectItem {
	start := p.peek().start
	item := selectItem{expr: p.expression()}
	item.text = p.source[start:p.consumed]
	if p.keyword("AS") {
		item.alias = p.word("an alias")
	}
	return item
}

func (p *parser) tableExpression() source {
	var from source
	if p.symbol("(") {
		from.subquery = p.selectQuery(false)
		p.expectSymbol(")")
	} else {
		from.table = p.word("a table name")
	}
	if p.keyword("AS") {
		from.alias = p.word("an alias")
	}
	from.final = p.keyword("FINAL")
	return from
}

func (p *parser) expressions() []expr {
	var list []expr
	for {
		list = append(list, p.expression())
		if !p.symbol(",") {
			return list
		}
	}
}

func (p *parser) expression() expr {
	left := p.conjunction()
	for p.keyword("OR") {
		left = logical{operator: "OR", left: left, right: p.conjunction()}
	}
	return left
}

func (p *parser) conjunction() expr {
	left := p.predicate()
	for p.keyword("AND") {
		left = logical{operator: "AND", left: left, right: p.predicate()}
	}
	return left
}

func (p *parser) predicate() expr {
	left := p.primary()
	if p.keyword("IN") {
		return membership{left: left, table: p.word("a table name")}
	}
	for _, operator := range []string{"=", "!=", "<=", ">=", "<", ">"} {
		if p.symbol(operator) {
			return comparison{operator: operator, left: left, right: p.primary()}
		}
	}
	return left
}

func (p *parser) primary() expr {
	next := p.peek()
	switch {
	case next.kind == tokenString:
		return literal{typ: dataType{kind: kindString}, value: p.take().text}
	case next.kind == tokenNumber:
		whole, err := strconv.ParseUint(p.take().text, 10, 64)
		if err != nil {
			panic(failure(cannotParseNumber, "Cannot parse number: %v", err))
		}
		return literal{typ: dataType{kind: kindUInt64}, value: whole}
	case p.symbol("("):
		items := p.expressions()
		p.expectSymbol(")")
		if len(items) == 1 {
			return items[0]
		}
		return tuple{items: items}
	case p.symbol("{"):
		name := p.word("a parameter name")
		p.expectSymbol(":")
		typ := p.dataType()
		p.expectSymbol("}")
		return parameter{name: name, typ: typ}
	case next.kind == tokenWord && !clauseWords[strings.ToUpper(next.text)]:
		name := p.take().text
		if p.symbol("(") {
			called := call{name: name}
			if !p.symbol(")") {
				called.arguments = p.expressions()
				p.expectSymbol(")")
			}
			return called
		}
		if p.symbol(".") {
			return identifier{qualifier: name, name: p.word("a column name")}
		}
		return identifier{name: name}
	default:
		panic(p.unexpected("an expression"))
	}
}
