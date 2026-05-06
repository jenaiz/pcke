package query

import "fmt"

// TokenType identifies the kind of a lexical token.
type TokenType int

// Token type constants.
const (
	TokenEOF    TokenType = iota // end of input
	TokenIdent                   // identifier (field names, collection names, keywords)
	TokenString                  // 'single-quoted string'
	TokenNumber                  // integer or float literal

	TokenEq  // =
	TokenNeq // !=
	TokenGt  // >
	TokenLt  // <
	TokenGte // >=
	TokenLte // <=
	TokenDot // .

	// Punctuation introduced by F12.T4.2 for TRAVERSE(...) argument lists.
	TokenLeftParen  // (
	TokenRightParen // )
	TokenComma      // ,
)

// Token represents a single lexical token produced by the lexer.
type Token struct {
	Type    TokenType
	Literal string
	Pos     int // byte offset in input
}

// String returns a human-readable representation for debugging.
func (t Token) String() string {
	return fmt.Sprintf("{%s %q @%d}", t.Type, t.Literal, t.Pos)
}

// String returns the name of the token type.
func (tt TokenType) String() string {
	switch tt {
	case TokenEOF:
		return "EOF"
	case TokenIdent:
		return "IDENT"
	case TokenString:
		return "STRING"
	case TokenNumber:
		return "NUMBER"
	case TokenEq:
		return "EQ"
	case TokenNeq:
		return "NEQ"
	case TokenGt:
		return "GT"
	case TokenLt:
		return "LT"
	case TokenGte:
		return "GTE"
	case TokenLte:
		return "LTE"
	case TokenDot:
		return "DOT"
	case TokenLeftParen:
		return "LPAREN"
	case TokenRightParen:
		return "RPAREN"
	case TokenComma:
		return "COMMA"
	default:
		return fmt.Sprintf("TokenType(%d)", int(tt))
	}
}
