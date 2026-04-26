package query

import (
	"fmt"
	"strconv"
	"strings"
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

	// collection
	coll, err := p.expectIdent()
	if err != nil {
		return nil, fmt.Errorf("%w: expected collection name", ErrSyntax)
	}

	coll = strings.ToLower(coll)
	if !isCollection(coll) {
		return nil, fmt.Errorf("%w: unknown collection %q", ErrUnknownCollection, coll)
	}
	q.Collection = coll

	// optional clauses
	for !p.atEnd() {
		kw := strings.ToLower(p.current().Literal)
		switch kw {
		case "where":
			if q.Where != nil {
				return nil, p.syntaxErr("duplicate WHERE clause")
			}
			wc, err := p.parseWhere()
			if err != nil {
				return nil, err
			}
			q.Where = wc
		case "order":
			if q.OrderBy != nil {
				return nil, p.syntaxErr("duplicate ORDER BY clause")
			}
			oc, err := p.parseOrderBy()
			if err != nil {
				return nil, err
			}
			q.OrderBy = oc
		case "limit":
			if q.Limit != 0 {
				return nil, p.syntaxErr("duplicate LIMIT clause")
			}
			lim, err := p.parseLimit()
			if err != nil {
				return nil, err
			}
			q.Limit = lim
		default:
			return nil, p.syntaxErr(fmt.Sprintf("unexpected token %q", p.current().Literal))
		}
	}

	return q, nil
}

func (p *parser) parseWhere() (*WhereClause, error) {
	p.advance() // skip "where"

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
