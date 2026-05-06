package query

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Parse parses a pcke query DSL string into a Query AST. Returns ErrSyntax
// for malformed input.
func Parse(input string) (*Query, error) {
	tokens, err := Tokenize(input)
	if err != nil {
		return nil, err
	}

	p := &parser{tokens: tokens}
	return p.parse()
}

type parser struct {
	tokens []Token
	pos    int
}

func (p *parser) parse() (*Query, error) {
	q := &Query{}

	coll, err := p.expectIdent()
	if err != nil {
		return nil, fmt.Errorf("%w: expected collection name", ErrSyntax)
	}
	coll = strings.ToLower(coll)
	if !isCollection(coll) {
		return nil, fmt.Errorf("%w: unknown collection %q", ErrUnknownCollection, coll)
	}
	q.Collection = coll

	for !p.atEnd() {
		if err := p.parseClause(q); err != nil {
			return nil, err
		}
	}
	return q, nil
}

// parseClause consumes one optional top-level clause (WHERE, ORDER BY,
// LIMIT, AS OF) and stores it on q. Returns an error if the lookahead
// token is not a recognised clause keyword or if the clause is duplicated.
func (p *parser) parseClause(q *Query) error {
	kw := strings.ToLower(p.current().Literal)
	switch kw {
	case "where":
		if q.Where != nil {
			return p.syntaxErr("duplicate WHERE clause")
		}
		wc, err := p.parseWhere()
		if err != nil {
			return err
		}
		q.Where = wc
	case "order":
		if q.OrderBy != nil {
			return p.syntaxErr("duplicate ORDER BY clause")
		}
		oc, err := p.parseOrderBy()
		if err != nil {
			return err
		}
		q.OrderBy = oc
	case "limit":
		if q.Limit != 0 {
			return p.syntaxErr("duplicate LIMIT clause")
		}
		lim, err := p.parseLimit()
		if err != nil {
			return err
		}
		q.Limit = lim
	case "as":
		if q.AsOf != nil {
			return p.syntaxErr("duplicate AS OF clause")
		}
		at, err := p.parseAsOf()
		if err != nil {
			return err
		}
		q.AsOf = at
	default:
		return p.syntaxErr(fmt.Sprintf("unexpected token %q", p.current().Literal))
	}
	return nil
}

// parseAsOf parses the "AS OF '<rfc3339-timestamp>'" clause. The "AS"
// token has already been peeked by the dispatch loop; this function
// consumes it together with the "OF" keyword and the timestamp literal.
//
// AS OF pins the query to a moment in time: every record-producing read
// returns the version that was active at the supplied timestamp.
//
// Surface-level support only in F12.T4 commit 1 — the executor wires
// the value to event.Store.AsOf in F12.T4 commit 3.
func (p *parser) parseAsOf() (*time.Time, error) {
	p.advance() // consume "as"

	if p.atEnd() {
		return nil, p.syntaxErr("expected OF after AS")
	}
	of := p.current()
	if of.Type != TokenIdent || strings.ToLower(of.Literal) != "of" {
		return nil, p.syntaxErr(fmt.Sprintf("expected OF after AS, got %q", of.Literal))
	}
	p.advance()

	if p.atEnd() {
		return nil, p.syntaxErr("expected timestamp literal after AS OF")
	}
	tok := p.current()
	if tok.Type != TokenString {
		return nil, p.syntaxErr(fmt.Sprintf("expected quoted timestamp after AS OF, got %q", tok.Literal))
	}
	p.advance()

	parsed, err := parseAsOfTimestamp(tok.Literal)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrSyntax, err)
	}
	return &parsed, nil
}

// parseTraverse parses the TRAVERSE(args) FROM '<startkey>' production.
// "TRAVERSE" itself has been peeked but not consumed by parseWhere;
// this function consumes everything from "TRAVERSE" through the
// closing string literal.
//
// Grammar:
//
//	TRAVERSE '(' edge_name (',' named_arg)* ')' FROM STRING
//	named_arg := IDENT '=' (NUMBER | STRING | IDENT)
//
// Recognised named args: depth=N, edge='type' (alias type='type'),
// direction=forward|reverse|both. Unknown keys are rejected; this
// keeps typo'd queries loud rather than silently producing wrong
// results.
func (p *parser) parseTraverse() (*TraverseExpr, error) {
	p.advance() // consume "traverse"
	expr, err := p.parseTraverseArgList()
	if err != nil {
		return nil, err
	}
	if err := p.parseTraverseFrom(expr); err != nil {
		return nil, err
	}
	if expr.Direction == "" {
		expr.Direction = "forward"
	}
	return expr, nil
}

// parseTraverseArgList consumes the "(edge_name [, named_arg]...)" arg
// block of a TRAVERSE expression and returns a partially-populated
// TraverseExpr (StartKey/Direction set later by parseTraverseFrom).
func (p *parser) parseTraverseArgList() (*TraverseExpr, error) {
	if p.atEnd() || p.current().Type != TokenLeftParen {
		return nil, p.syntaxErr("expected '(' after TRAVERSE")
	}
	p.advance() // consume '('

	if p.atEnd() || p.current().Type != TokenIdent {
		return nil, p.syntaxErr("expected edge collection name as first TRAVERSE argument")
	}
	expr := &TraverseExpr{EdgeName: p.current().Literal}
	p.advance()

	for !p.atEnd() && p.current().Type == TokenComma {
		p.advance()
		if err := p.parseTraverseNamedArg(expr); err != nil {
			return nil, err
		}
	}

	if p.atEnd() || p.current().Type != TokenRightParen {
		return nil, p.syntaxErr("expected ')' or ',' inside TRAVERSE argument list")
	}
	p.advance()
	return expr, nil
}

// parseTraverseFrom consumes "FROM '<startkey>'" and stores the start
// key on expr.
func (p *parser) parseTraverseFrom(expr *TraverseExpr) error {
	if p.atEnd() || p.current().Type != TokenIdent ||
		strings.ToLower(p.current().Literal) != "from" {
		return p.syntaxErr("expected FROM after TRAVERSE(...)")
	}
	p.advance()
	if p.atEnd() || p.current().Type != TokenString {
		return p.syntaxErr("expected quoted start-key after TRAVERSE(...) FROM")
	}
	if p.current().Literal == "" {
		return p.syntaxErr("TRAVERSE FROM requires a non-empty start key")
	}
	expr.StartKey = p.current().Literal
	p.advance()
	return nil
}

// parseTraverseNamedArg parses one IDENT '=' VALUE pair inside the
// TRAVERSE arg list and applies it to expr.
func (p *parser) parseTraverseNamedArg(expr *TraverseExpr) error {
	if p.atEnd() || p.current().Type != TokenIdent {
		return p.syntaxErr("expected named TRAVERSE argument")
	}
	key := strings.ToLower(p.current().Literal)
	p.advance()
	if p.atEnd() || p.current().Type != TokenEq {
		return p.syntaxErr(fmt.Sprintf("expected '=' after %q in TRAVERSE", key))
	}
	p.advance()
	if p.atEnd() {
		return p.syntaxErr(fmt.Sprintf("expected value after %q= in TRAVERSE", key))
	}
	val := p.current()
	p.advance()

	switch key {
	case "depth":
		if val.Type != TokenNumber {
			return p.syntaxErr("TRAVERSE depth= expects a number")
		}
		n, err := strconv.Atoi(val.Literal)
		if err != nil || n < 0 {
			return p.syntaxErr(fmt.Sprintf("TRAVERSE depth= must be a non-negative integer, got %q", val.Literal))
		}
		expr.Depth = n
	case "edge", "type":
		if val.Type != TokenString {
			return p.syntaxErr(fmt.Sprintf("TRAVERSE %s= expects a quoted string", key))
		}
		expr.EdgeType = val.Literal
	case "direction":
		var lit string
		switch val.Type {
		case TokenIdent, TokenString:
			lit = strings.ToLower(val.Literal)
		default:
			return p.syntaxErr("TRAVERSE direction= expects an identifier or string")
		}
		switch lit {
		case "forward", "reverse", "both":
			expr.Direction = lit
		default:
			return p.syntaxErr(fmt.Sprintf("TRAVERSE direction= must be forward|reverse|both, got %q", lit))
		}
	default:
		return p.syntaxErr(fmt.Sprintf("unknown TRAVERSE argument %q", key))
	}
	return nil
}

// parseAsOfTimestamp accepts RFC3339, RFC3339 with nanosecond
// precision, or a plain "YYYY-MM-DD" date. Other formats are rejected.
func parseAsOfTimestamp(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02",
	}
	for _, layout := range formats {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid AS OF timestamp %q (want RFC3339 or YYYY-MM-DD)", s)
}

func (p *parser) parseWhere() (*WhereClause, error) {
	p.advance() // skip "where"

	// TRAVERSE special form: WHERE TRAVERSE(args) FROM '<startkey>'.
	if !p.atEnd() && p.current().Type == TokenIdent &&
		strings.ToLower(p.current().Literal) == "traverse" {
		traverse, err := p.parseTraverse()
		if err != nil {
			return nil, err
		}
		return &WhereClause{Traverse: traverse}, nil
	}

	wc := &WhereClause{}
	cond, err := p.parseCondition()
	if err != nil {
		return nil, err
	}
	wc.Conditions = append(wc.Conditions, cond)

	for !p.atEnd() {
		kw := strings.ToLower(p.current().Literal)
		if kw == "and" || kw == "or" {
			var op LogicalOp
			if kw == "or" {
				op = LogicalOr
			}
			wc.Operators = append(wc.Operators, op)
			p.advance()

			cond, err := p.parseCondition()
			if err != nil {
				return nil, err
			}
			wc.Conditions = append(wc.Conditions, cond)
		} else {
			break // next clause (order, limit)
		}
	}

	return wc, nil
}

func (p *parser) parseCondition() (Condition, error) {
	var c Condition

	// field (may be dotted: identifier.identifier...)
	field, err := p.parseField()
	if err != nil {
		return c, err
	}
	c.Field = field

	// operator
	op, err := p.parseOperator()
	if err != nil {
		return c, err
	}
	c.Operator = op

	// value
	val, err := p.parseValue()
	if err != nil {
		return c, err
	}
	c.Value = val

	return c, nil
}

func (p *parser) parseField() (string, error) {
	name, err := p.expectIdent()
	if err != nil {
		return "", fmt.Errorf("%w: expected field name", ErrSyntax)
	}

	// Handle dotted fields: field.subfield.subsubfield
	for !p.atEnd() && p.current().Type == TokenDot {
		p.advance() // skip "."
		sub, err := p.expectIdent()
		if err != nil {
			return "", fmt.Errorf("%w: expected field name after '.'", ErrSyntax)
		}
		name += "." + sub
	}

	return name, nil
}

func (p *parser) parseOperator() (Operator, error) {
	if p.atEnd() {
		return 0, p.syntaxErr("expected operator")
	}

	tok := p.current()

	switch tok.Type {
	case TokenEq:
		p.advance()
		return OpEq, nil
	case TokenNeq:
		p.advance()
		return OpNeq, nil
	case TokenGt:
		p.advance()
		return OpGt, nil
	case TokenLt:
		p.advance()
		return OpLt, nil
	case TokenGte:
		p.advance()
		return OpGte, nil
	case TokenLte:
		p.advance()
		return OpLte, nil
	case TokenIdent:
		switch strings.ToLower(tok.Literal) {
		case "contains":
			p.advance()
			return OpContains, nil
		case "matches":
			p.advance()
			return OpMatches, nil
		}
	}

	return 0, p.syntaxErr(fmt.Sprintf("expected operator, got %q", tok.Literal))
}

func (p *parser) parseValue() (any, error) {
	if p.atEnd() {
		return nil, p.syntaxErr("expected value")
	}

	tok := p.current()
	p.advance()

	switch tok.Type {
	case TokenString:
		return tok.Literal, nil
	case TokenNumber:
		f, err := strconv.ParseFloat(tok.Literal, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid number %q", ErrSyntax, tok.Literal)
		}
		return f, nil
	case TokenIdent:
		switch strings.ToLower(tok.Literal) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return nil, p.syntaxErrAt(fmt.Sprintf("expected value, got identifier %q", tok.Literal), tok.Pos)
	default:
		return nil, p.syntaxErrAt(fmt.Sprintf("expected value, got %s", tok.Type), tok.Pos)
	}
}

func (p *parser) parseOrderBy() (*OrderClause, error) {
	p.advance() // skip "order"

	// expect "by"
	kw, err := p.expectIdent()
	if err != nil || strings.ToLower(kw) != "by" {
		return nil, p.syntaxErr("expected 'by' after 'order'")
	}

	field, err := p.parseField()
	if err != nil {
		return nil, err
	}

	oc := &OrderClause{Field: field, Direction: SortAsc}

	// optional asc/desc
	if !p.atEnd() && p.current().Type == TokenIdent {
		switch strings.ToLower(p.current().Literal) {
		case "asc":
			oc.Direction = SortAsc
			p.advance()
		case "desc":
			oc.Direction = SortDesc
			p.advance()
		}
	}

	return oc, nil
}

func (p *parser) parseLimit() (int, error) {
	p.advance() // skip "limit"

	if p.atEnd() || p.current().Type != TokenNumber {
		return 0, p.syntaxErr("expected integer after 'limit'")
	}

	n, err := strconv.Atoi(p.current().Literal)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid limit %q", ErrSyntax, p.current().Literal)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%w: limit must be positive", ErrSyntax)
	}
	p.advance()

	return n, nil
}

// ── helpers ──

func (p *parser) current() Token {
	if p.pos >= len(p.tokens) {
		return Token{Type: TokenEOF, Pos: -1}
	}
	return p.tokens[p.pos]
}

func (p *parser) atEnd() bool {
	return p.pos >= len(p.tokens) || p.tokens[p.pos].Type == TokenEOF
}

func (p *parser) advance() {
	if p.pos < len(p.tokens) {
		p.pos++
	}
}

func (p *parser) expectIdent() (string, error) {
	if p.atEnd() || p.current().Type != TokenIdent {
		return "", p.syntaxErr("expected identifier")
	}
	lit := p.current().Literal
	p.advance()
	return lit, nil
}

func (p *parser) syntaxErr(msg string) error {
	pos := -1
	if p.pos < len(p.tokens) {
		pos = p.tokens[p.pos].Pos
	}
	return fmt.Errorf("%w: %s at position %d", ErrSyntax, msg, pos)
}

func (p *parser) syntaxErrAt(msg string, pos int) error {
	return fmt.Errorf("%w: %s at position %d", ErrSyntax, msg, pos)
}

func isCollection(name string) bool {
	switch name {
	case "nodes", "evolution", "constraints", "notes", "relations":
		return true
	}
	return false
}
