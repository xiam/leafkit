package postgres

import (
	"fmt"
	"strings"

	scan "github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/rules/base"
	lex "github.com/xiam/bytescan/rules/core"
	adapter "github.com/xiam/leafkit/adapter/bytescan"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tests/sql/core"
	"github.com/xiam/leafkit/tree"
)

const (
	DollarStringToken p.TokenKind = "postgres:dollar-string"
	QuotedStringToken p.TokenKind = "postgres:quoted-string"
	DollarNameToken   p.TokenKind = "postgres:dollar-name"
	LimitToken        p.TokenKind = "postgres:LIMIT"
)

// Scanner adds case-sensitive dollar quoting and PostgreSQL multiline strings.
// Bare names retain the core ASCII mode but permit dollars after their first
// character, so SELECT$tag$ is a name, not SELECT followed by a dollar string.
func Scanner(cur *cursor.Reader) *scan.Scanner {
	word := func(r rune) bool { return lex.ASCIIWord(r) || r == '$' }
	return core.Scanner(cur,
		core.LexRule{Kind: scan.TokenType(LimitToken), Rule: lex.KeywordsIgnoreCaseWithGuard(word, "LIMIT")},
		core.LexRule{Kind: scan.TokenType(DollarStringToken), Rule: dollarString()},
		core.LexRule{Kind: scan.TokenType(DollarNameToken), Rule: lex.Sequence(base.Identifier(lex.ASCIIIdentifierStart, lex.ASCIIWord), lex.Literal("$"), lex.ZeroOrMore(lex.Rune(word)))},
		core.LexRule{Kind: scan.TokenType(QuotedStringToken), Rule: base.QuotedMultiline('\'', '\'')})
}

// Each returned Rule captures only immutable progress. Neither partial input
// nor a failed token mutates a shared closure. Payload text stays in Snapshot.
func dollarString() scan.Rule {
	var tag func(string) scan.Rule
	var body func(string, int) scan.Rule
	body = func(delimiter string, matched int) scan.Rule {
		return func(s scan.Symbol) (scan.Rule, scan.State) {
			if s.IsEOF() || s.Rune() == 0 {
				return nil, scan.StateReject
			}
			next := 0
			if s.Rune() == rune(delimiter[matched]) {
				next = matched + 1
			} else if s.Rune() == '$' {
				next = 1
			}
			if next == len(delimiter) {
				return nil, scan.StateAccept
			}
			return body(delimiter, next), scan.StateContinue
		}
	}
	tag = func(text string) scan.Rule {
		return func(s scan.Symbol) (scan.Rule, scan.State) {
			if s.IsEOF() {
				return nil, scan.StateReject
			}
			r := s.Rune()
			if r == '$' {
				return body("$"+text+"$", 0), scan.StateContinue
			}
			if (text == "" && lex.ASCIIIdentifierStart(r)) || (text != "" && lex.ASCIIWord(r)) {
				return tag(text + string(r)), scan.StateContinue
			}
			return nil, scan.StateReject
		}
	}
	return func(s scan.Symbol) (scan.Rule, scan.State) {
		if s.IsEOF() || s.Rune() != '$' {
			return nil, scan.StateReject
		}
		return tag(""), scan.StateContinue
	}
}

// Parse accepts the bounded grammar in one complete snapshot. Check
// core.Diagnostics before consuming or printing a returned tree.
func Parse(snapshot *core.Snapshot) (tree.Node, error) {
	engine, err := Engine(snapshot)
	if err != nil {
		return nil, err
	}
	cur := cursor.NewReader(strings.NewReader(snapshot.Text()))
	source, err := adapter.New(snapshot.ID(), Scanner(cur), cur, p.Request{}, core.Trivia())
	if err != nil {
		return nil, err
	}
	root, status, err := engine.Start(source, "").Run()
	if err == nil && status != p.Done {
		return nil, fmt.Errorf("postgres: complete input paused")
	}
	return root, err
}
