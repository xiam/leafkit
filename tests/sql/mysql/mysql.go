// Package mysql composes a bounded MySQL SELECT example over the shared core.
package mysql

import (
	"fmt"
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
	BacktickNameToken p.TokenKind = "mysql:backtick-name"
	StraightJoinToken p.TokenKind = "mysql:STRAIGHT_JOIN"
	StraightJoinKind  tree.Kind   = "mysql:straight-join"
)

// StraightJoin retains the ordered-join instruction. Its left input is the
// preceding FROM/join sequence; its right input and optional ON remain children.
type StraightJoin struct {
	info      tree.Envelope
	table     *sql.Table
	condition *sql.Comparison
}

func (n *StraightJoin) Info() tree.Envelope        { return n.info }
func (n *StraightJoin) Table() *sql.Table          { return n.table }
func (n *StraightJoin) Condition() *sql.Comparison { return n.condition }
func (n *StraightJoin) Child(i int) (tree.Node, bool) {
	switch {
	case i == 0:
		return n.table, true
	case i == 1 && n.condition != nil:
		return n.condition, true
	default:
		return nil, false
	}
}

// Grammar changes only the shared name and join slots. Each call is independent.
func Grammar() p.Grammar {
	g := sql.Grammar()
	g.Rules["name"] = p.Make("name", sql.NameKind, p.Choose(p.ChoiceSpec{},
		sql.Token(sql.NameToken), sql.Token(sql.QuotedNameToken), sql.Token(BacktickNameToken)))
	g.Rules["join"] = p.Choose(p.ChoiceSpec{},
		p.Make("straight-join", StraightJoinKind, p.Seq(
			sql.Skip(StraightJoinToken), p.Bind("table", p.Ref("table")),
			p.Opt(p.Seq(sql.Skip(sql.Keyword("ON")), p.Bind("condition", p.Ref("comparison")))))),
		g.Rules["join"])
	return g
}

// Scanner fixes ANSI_QUOTES and NO_BACKSLASH_ESCAPES, as well as the core's
// bounded lexical subset. Backticks double to escape; keyword guards are ASCII.
func Scanner(cur *cursor.Reader) *scan.Scanner {
	backtick := lex.Sequence(lex.Literal("`"),
		lex.OneOrMore(lex.Choice(lex.Literal("``"), lex.Rune(lex.RuneSet("`\r\n").Not()))), lex.Literal("`"))
	return sql.Scanner(cur,
		sql.LexRule{Kind: scan.TokenType(BacktickNameToken), Rule: backtick},
		sql.LexRule{Kind: scan.TokenType(StraightJoinToken), Rule: lex.KeywordsIgnoreCaseWithGuard(lex.ASCIIWord, "STRAIGHT_JOIN")})
}

// Builders retains shared nodes and snapshot-backed names, including raw quotes.
func Builders(snapshot *sql.Snapshot) p.Builders[tree.Node] {
	return sql.Builders(snapshot, func(b p.Build[tree.Node]) (tree.Node, error) {
		if b.Kind != StraightJoinKind {
			return nil, fmt.Errorf("mysql: unknown node %q", b.Kind)
		}
		n := &StraightJoin{info: tree.NewEnvelope(b.Kind, b.Span)}
		for _, f := range b.Fields {
			if len(f.Nodes) != 1 {
				return nil, fmt.Errorf("mysql: invalid %s field", f.Name)
			}
			switch f.Name {
			case "table":
				n.table, _ = f.Nodes[0].(*sql.Table)
			case "condition":
				n.condition, _ = f.Nodes[0].(*sql.Comparison)
			default:
				return nil, fmt.Errorf("mysql: unknown field %q", f.Name)
			}
		}
		if n.table == nil {
			return nil, fmt.Errorf("mysql: missing table")
		}
		return n, nil
	}, sql.TokenDecoder{Kind: BacktickNameToken, Quoted: true,
		Decode: func(raw string) string { return strings.ReplaceAll(raw[1:len(raw)-1], "``", "`") }})
}

func Engine(snapshot *sql.Snapshot) (*p.Engine[tree.Node], error) {
	return p.Configure(Grammar(), Builders(snapshot), p.Limits{})
}

// Parse returns operational errors separately from syntax diagnostic trees.
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
		return nil, fmt.Errorf("mysql: complete input paused")
	}
	return root, err
}

// Print uses ANSI identifier quotes in the fixed mode, retaining STRAIGHT_JOIN.
// The portable core printer rejects this join instead of erasing its semantics.
func Print(root tree.Node) (string, error) {
	return sql.PrintWith(root, func(n tree.Node, recur func(tree.Node) (string, error)) (string, error) {
		join, ok := n.(*StraightJoin)
		if !ok {
			return "", fmt.Errorf("mysql: cannot print %q", n.Info().Kind)
		}
		table, err := recur(join.Table())
		if err != nil {
			return "", err
		}
		text := "STRAIGHT_JOIN " + table
		if join.Condition() != nil {
			condition, err := recur(join.Condition())
			if err != nil {
				return "", err
			}
			text += " ON " + condition
		}
		return text, nil
	})
}
