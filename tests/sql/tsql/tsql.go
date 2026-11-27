// Package tsql composes a bounded SQL Server SELECT example over the SQL core.
package tsql

import (
	"fmt"
	"strconv"
	"strings"

	scan "github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	lex "github.com/xiam/bytescan/rules/core"
	adapter "github.com/xiam/leafkit/adapter/bytescan"
	p "github.com/xiam/leafkit/parser"
	sql "github.com/xiam/leafkit/tests/sql/core"
	"github.com/xiam/leafkit/tree"
)

const (
	BracketNameToken p.TokenKind = "tsql:bracket-name"
	TopToken         p.TokenKind = "tsql:TOP"
	WithToken        p.TokenKind = "tsql:WITH"
	TiesToken        p.TokenKind = "tsql:TIES"
	PercentToken     p.TokenKind = "tsql:PERCENT"
	TopKind          tree.Kind   = "tsql:top"
	WithTiesKind     tree.Kind   = "tsql:with-ties"
)

// Top retains a row count and whether rows tied at the cutoff are included.
// WITH TIES uses the complete shared ORDER BY list, not projection equality.
// The grammar requires ORDER BY when ties are requested.
type Top struct {
	info  tree.Envelope
	count *sql.Lexeme
	ties  tree.Node
}

func (n *Top) Info() tree.Envelope { return n.info }
func (n *Top) Count() *sql.Lexeme  { return n.count }
func (n *Top) WithTies() bool      { return n.ties != nil }
func (n *Top) Child(i int) (tree.Node, bool) {
	if i == 0 {
		return n.count, true
	}
	if i == 1 && n.ties != nil {
		return n.ties, true
	}
	return nil, false
}

// Grammar changes only core composition slots. Shared syntax keeps its types.
func Grammar() p.Grammar {
	g := sql.Grammar()
	g.Rules["name"] = p.Make("name", sql.NameKind, p.Choose(p.ChoiceSpec{},
		sql.Token(BracketNameToken), sql.Token(sql.NameToken), sql.Token(sql.QuotedNameToken)))
	count := p.Guard(p.Make("count", sql.NumberKind, sql.Token(sql.NumberToken)), func(v p.View) bool {
		tokens := v.Parts.Tokens("")
		if len(tokens) != 1 {
			return false
		}
		// TOP's bounded form is a nonnegative bigint literal, not a decimal,
		// expression, sign, percentage or parameter.
		_, err := strconv.ParseUint(tokens[0].Text, 10, 63)
		return err == nil
	})
	g.Rules["modifiers"] = p.Seq(g.Rules["modifiers"], p.Opt(p.Make("top", TopKind,
		p.Seq(sql.Skip(TopToken), sql.Skip(sql.Symbol("(")), p.Bind("count", count),
			sql.Skip(sql.Symbol(")")), p.Opt(p.Bind("ties", p.Make("ties", WithTiesKind,
				p.Seq(sql.Skip(WithToken), sql.Skip(TiesToken)))))))))
	// Inspect token kinds of the retained SELECT production. A keyword inside
	// a quoted name, string or comment cannot satisfy the ORDER requirement.
	g.Rules["select"] = p.Guard(g.Rules["select"], func(v p.View) bool {
		var ties, order bool
		for _, token := range v.Parts.Tokens("") {
			ties = ties || token.Kind == TiesToken
			order = order || token.Kind == sql.Keyword("ORDER")
		}
		return !ties || order
	})
	return g
}

// Scanner adds bracket names and bounded modifier keywords before core rules.
// ANSI double-quoted identifiers assume QUOTED_IDENTIFIER ON.
func Scanner(cur *cursor.Reader) *scan.Scanner {
	brackets := lex.Sequence(lex.Literal("["),
		lex.OneOrMore(lex.Choice(lex.Literal("]]"), lex.Rune(lex.RuneSet("]\r\n").Not()))), lex.Literal("]"))
	rules := []sql.LexRule{{Kind: scan.TokenType(BracketNameToken), Rule: brackets}}
	for _, token := range []p.TokenKind{TopToken, WithToken, TiesToken, PercentToken} {
		word := strings.TrimPrefix(string(token), "tsql:")
		rules = append(rules, sql.LexRule{Kind: scan.TokenType(token), Rule: lex.KeywordsIgnoreCaseWithGuard(lex.ASCIIWord, word)})
	}
	return sql.Scanner(cur, rules...)
}

func Builders(snapshot *sql.Snapshot) p.Builders[tree.Node] {
	return sql.Builders(snapshot, func(b p.Build[tree.Node]) (tree.Node, error) {
		switch b.Kind {
		case WithTiesKind:
			return tree.NewContainer(b.Kind, b.Span), nil
		case TopKind:
			n := &Top{info: tree.NewEnvelope(b.Kind, b.Span)}
			for _, field := range b.Fields {
				if len(field.Nodes) != 1 {
					return nil, fmt.Errorf("tsql: invalid TOP field %q", field.Name)
				}
				switch field.Name {
				case "count":
					n.count, _ = field.Nodes[0].(*sql.Lexeme)
				case "ties":
					n.ties = field.Nodes[0]
				}
			}
			if n.count == nil {
				return nil, fmt.Errorf("tsql: TOP missing count")
			}
			return n, nil
		}
		return nil, fmt.Errorf("tsql: unknown node %q", b.Kind)
	}, sql.TokenDecoder{Kind: BracketNameToken, Quoted: true, Decode: func(raw string) string {
		return strings.ReplaceAll(raw[1:len(raw)-1], "]]", "]")
	}})
}

// Engine binds a fresh composed grammar to one immutable source snapshot.
func Engine(snapshot *sql.Snapshot) (*p.Engine[tree.Node], error) {
	return p.Configure(Grammar(), Builders(snapshot), p.Limits{})
}

// Parse returns a diagnostic tree for malformed or unsupported SQL.
// Check core.Diagnostics before using the tree; Go errors report operational failures.
func Parse(snapshot *sql.Snapshot) (tree.Node, error) {
	engine, err := Engine(snapshot)
	if err != nil {
		return nil, err
	}
	cur := cursor.NewReader(strings.NewReader(snapshot.Text()))
	source, err := adapter.New(snapshot.ID(), Scanner(cur), cur, p.Request{}, sql.Trivia())
	if err != nil {
		return nil, err
	}
	root, status, err := engine.Start(source, "").Run()
	if err == nil && status != p.Done {
		return nil, fmt.Errorf("tsql: complete input paused")
	}
	return root, err
}

// Print retains TOP and WITH TIES. Shared delimited names print as ANSI quotes,
// preserving decoded values under the documented QUOTED_IDENTIFIER ON mode.
func Print(root tree.Node) (string, error) {
	return sql.PrintWith(root, func(n tree.Node, recur func(tree.Node) (string, error)) (string, error) {
		if top, ok := n.(*Top); ok {
			count, err := recur(top.Count())
			if err != nil {
				return "", err
			}
			text := "TOP (" + count + ")"
			if top.WithTies() {
				text += " WITH TIES"
			}
			return text, nil
		}
		return "", fmt.Errorf("tsql: cannot print %q", n.Info().Kind)
	})
}
