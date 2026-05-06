package query

import "fmt"

// Lexer tokenizes a pcke query DSL string into a sequence of tokens.
type Lexer struct {
	input  string
	pos    int
	tokens []Token
}

// Tokenize scans the input string and returns all tokens, ending with TokenEOF.
// Returns ErrSyntax (wrapped) for invalid characters or unterminated strings.
func Tokenize(input string) ([]Token, error) {
	l := &Lexer{input: input}
	return l.tokenize()
}

func (l *Lexer) tokenize() ([]Token, error) {
	for l.pos < len(l.input) {
		l.skipWhitespace()
		if l.pos >= len(l.input) {
			break
		}

		if err := l.scanToken(); err != nil {
			return nil, err
		}
	}

	l.tokens = append(l.tokens, Token{Type: TokenEOF, Pos: l.pos})
	return l.tokens, nil
}

func (l *Lexer) scanToken() error {
	ch := l.input[l.pos]

	switch {
	case ch == '\'':
		return l.readString()
	case isDigit(ch):
		l.readNumber()
	case ch == '=':
		l.emit(TokenEq, "=")
	case ch == '!':
		return l.readBang()
	case ch == '>':
		l.readAngleBracket(TokenGt, ">", TokenGte, ">=")
	case ch == '<':
		l.readAngleBracket(TokenLt, "<", TokenLte, "<=")
	case ch == '.':
		l.emit(TokenDot, ".")
	case ch == '(':
		l.emit(TokenLeftParen, "(")
	case ch == ')':
		l.emit(TokenRightParen, ")")
	case ch == ',':
		l.emit(TokenComma, ",")
	case isLetter(ch) || ch == '_':
		l.readIdent()
	default:
		return l.syntaxErr(fmt.Sprintf("unexpected character %q", ch))
	}
	return nil
}

func (l *Lexer) readBang() error {
	start := l.pos
	if l.peek() == '=' {
		l.tokens = append(l.tokens, Token{Type: TokenNeq, Literal: "!=", Pos: start})
		l.pos += 2
		return nil
	}
	return l.syntaxErr("unexpected character '!'")
}

func (l *Lexer) readAngleBracket(singleType TokenType, singleLit string, doubleType TokenType, doubleLit string) {
	start := l.pos
	if l.peek() == '=' {
		l.tokens = append(l.tokens, Token{Type: doubleType, Literal: doubleLit, Pos: start})
		l.pos += 2
	} else {
		l.emit(singleType, singleLit)
	}
}

func (l *Lexer) emit(typ TokenType, lit string) {
	l.tokens = append(l.tokens, Token{Type: typ, Literal: lit, Pos: l.pos})
	l.pos++
}

func (l *Lexer) peek() byte {
	if l.pos+1 < len(l.input) {
		return l.input[l.pos+1]
	}
	return 0
}

func (l *Lexer) skipWhitespace() {
	for l.pos < len(l.input) && isWhitespace(l.input[l.pos]) {
		l.pos++
	}
}

func (l *Lexer) readString() error {
	start := l.pos
	l.pos++ // skip opening quote

	for l.pos < len(l.input) {
		if l.input[l.pos] == '\'' {
			// The literal does not include the quotes.
			lit := l.input[start+1 : l.pos]
			l.tokens = append(l.tokens, Token{Type: TokenString, Literal: lit, Pos: start})
			l.pos++ // skip closing quote
			return nil
		}
		l.pos++
	}

	return l.syntaxErr("unterminated string literal")
}

func (l *Lexer) readNumber() {
	start := l.pos
	for l.pos < len(l.input) && (isDigit(l.input[l.pos]) || l.input[l.pos] == '.') {
		l.pos++
	}
	l.tokens = append(l.tokens, Token{Type: TokenNumber, Literal: l.input[start:l.pos], Pos: start})
}

func (l *Lexer) readIdent() {
	start := l.pos
	for l.pos < len(l.input) && (isLetter(l.input[l.pos]) || isDigit(l.input[l.pos]) || l.input[l.pos] == '_') {
		l.pos++
	}
	l.tokens = append(l.tokens, Token{Type: TokenIdent, Literal: l.input[start:l.pos], Pos: start})
}

func (l *Lexer) syntaxErr(msg string) error {
	return fmt.Errorf("%w: %s at position %d", ErrSyntax, msg, l.pos)
}

func isWhitespace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
func isDigit(c byte) bool      { return c >= '0' && c <= '9' }
func isLetter(c byte) bool     { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
