// Package core is a composable SQL SELECT example built with public APIs.
package core

import (
	scan "github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/rules/base"
	lex "github.com/xiam/bytescan/rules/core"
	adapter "github.com/xiam/leafkit/adapter/bytescan"
	p "github.com/xiam/leafkit/parser"
)

const (
	NameToken       p.TokenKind = "sql:name"
	QuotedNameToken p.TokenKind = "sql:quoted-name"
	StringToken     p.TokenKind = "sql:string"
	NumberToken     p.TokenKind = "sql:number"
)

// Keyword and Symbol produce the shared token namespace. Dialect-only kinds
// must instead use their own prefix (for example "postgres:ilike").
func Keyword(upper string) p.TokenKind { return p.TokenKind("sql:" + upper) }
func Symbol(text string) p.TokenKind   { return p.TokenKind("sql:" + text) }

// LexRule prepends a dialect rule. Bytescan resolves equal-length matches in
// registration order; a dialect keyword can therefore precede the core name.
type LexRule struct {
	Kind scan.TokenType
	Rule scan.Rule
}

// Scanner fixes ANSI-style quotes, doubled-quote escapes and ASCII bare names.
// Extra rules are registered first. Each parse needs its own scanner/cursor.
func Scanner(cur *cursor.Reader, extra ...LexRule) *scan.Scanner {
	s := scan.NewWithCursor(cur)
	for _, r := range extra {
		s.MustAddRule(r.Kind, r.Rule)
	}
	s.MustAddRule("sql:space", lex.RuneRun(lex.RuneSet(" \t\r\n")))
	s.MustAddRule("sql:comment", lex.Choice(base.LineComment("--"), base.RawMultiline("/*", "*/")))
	for _, word := range []string{"SELECT", "DISTINCT", "FROM", "AS", "INNER", "JOIN", "ON", "WHERE", "ORDER", "BY", "ASC", "DESC"} {
		s.MustAddRule(scan.TokenType(Keyword(word)), lex.KeywordsIgnoreCaseWithGuard(lex.ASCIIWord, word))
	}
	s.MustAddRule(scan.TokenType(NameToken), base.Identifier(lex.ASCIIIdentifierStart, lex.ASCIIWord))
	s.MustAddRule(scan.TokenType(QuotedNameToken), lex.Sequence(lex.Literal(`"`), lex.OneOrMore(lex.Choice(lex.Literal(`""`), lex.Rune(lex.RuneSet("\"\r\n").Not()))), lex.Literal(`"`)))
	s.MustAddRule(scan.TokenType(StringToken), base.Quoted('\'', '\''))
	s.MustAddRule(scan.TokenType(NumberToken), lex.Sequence(lex.RuneRun(lex.ASCIIDigit), lex.Optional(lex.Sequence(lex.Literal("."), lex.RuneRun(lex.ASCIIDigit)))))
	for _, sym := range []string{"<=", ">=", "<>", "!=", "=", "<", ">", ".", ",", "*", ";", "(", ")"} {
		s.MustAddRule(scan.TokenType(Symbol(sym)), lex.Literal(sym))
	}
	return s
}

// Trivia returns fresh adapter options, so callers cannot mutate shared state.
func Trivia() adapter.Options {
	return adapter.Options{Trivia: []scan.TokenType{"sql:space", "sql:comment"}}
}
