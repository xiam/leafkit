// Package postgres composes a bounded PostgreSQL SELECT example over core.
package postgres

import (
	"fmt"
	"strconv"
	"strings"

	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tests/sql/core"
	"github.com/xiam/leafkit/tree"
)

const DistinctOnKind tree.Kind = "postgres:distinct-on"

const LimitKind tree.Kind = "postgres:limit"

// Limit retains a nonnegative bigint row count. Its span refers to the input,
// including when a translation constructs it from a source TOP count.
type Limit struct {
	info  tree.Envelope
	count *core.Lexeme
}

func (n *Limit) Info() tree.Envelope { return n.info }
func (n *Limit) Count() *core.Lexeme { return n.count }
func (n *Limit) Child(i int) (tree.Node, bool) {
	if i == 0 {
		return n.count, true
	}
	return nil, false
}

// NewLimit shares an immutable count lexeme without copying source text.
func NewLimit(count *core.Lexeme) (*Limit, error) {
	if count == nil || count.Info().Kind != core.NumberKind {
		return nil, fmt.Errorf("postgres: LIMIT requires an integer count")
	}
	if _, err := strconv.ParseUint(count.Value(), 10, 63); err != nil {
		return nil, fmt.Errorf("postgres: invalid LIMIT count")
	}
	return &Limit{info: tree.NewEnvelope(LimitKind, count.Info().Span), count: count}, nil
}

// DistinctOn retains shared expression children in source order. Its storage is
// private; Expressions returns a copy of the child references.
type DistinctOn struct {
	info        tree.Envelope
	expressions []tree.Node
}

func (n *DistinctOn) Info() tree.Envelope { return n.info }
func (n *DistinctOn) Child(i int) (tree.Node, bool) {
	if i < 0 || i >= len(n.expressions) {
		return nil, false
	}
	return n.expressions[i], true
}
func (n *DistinctOn) Expressions() []tree.Node { return append([]tree.Node(nil), n.expressions...) }

// Grammar extends the modifier, operand, name and suffix composition points. Ordinary
// DISTINCT and all common names, expressions, joins and tables remain shared.
func Grammar() p.Grammar {
	g := core.Grammar()
	// Two-token lookahead distinguishes DISTINCT ON from ordinary DISTINCT.
	g.Rules["modifiers"] = p.Choose(p.ChoiceSpec{Lookahead: 2},
		p.Make("distinct-on", DistinctOnKind, p.Seq(
			core.Skip(core.Keyword("DISTINCT")), core.Skip(core.Keyword("ON")), core.Skip(core.Symbol("(")),
			p.Bind("expressions", p.Seq(p.Ref("expression"), p.Many(p.Seq(core.Skip(core.Symbol(",")), p.Ref("expression"))))),
			core.Skip(core.Symbol(")")))), g.Rules["modifiers"])
	g.Rules["operand"] = p.Choose(p.ChoiceSpec{},
		p.Make("postgres-string", core.StringKind, p.Choose(p.ChoiceSpec{}, core.Token(DollarStringToken), core.Token(QuotedStringToken))), g.Rules["operand"])
	g.Rules["name"] = p.Choose(p.ChoiceSpec{}, p.Make("dollar-name", core.NameKind, core.Token(DollarNameToken)), g.Rules["name"])
	count := p.Guard(p.Make("limit-count", core.NumberKind, core.Token(core.NumberToken)), func(v p.View) bool {
		tokens := v.Parts.Tokens("")
		if len(tokens) != 1 {
			return false
		}
		_, err := strconv.ParseUint(tokens[0].Text, 10, 63)
		return err == nil
	})
	g.Rules["suffix"] = p.Opt(p.Make("limit", LimitKind,
		p.Seq(core.Skip(LimitToken), p.Bind("count", count))))
	return g
}

// Engine binds immutable source storage and dialect construction to a fresh
// public parser engine. SQL syntax errors are diagnostics, not Go errors.
func Engine(snapshot *core.Snapshot) (*p.Engine[tree.Node], error) {
	build := func(b p.Build[tree.Node]) (tree.Node, error) {
		if b.Kind == LimitKind {
			for _, field := range b.Fields {
				if field.Name == "count" && len(field.Nodes) == 1 {
					count, _ := field.Nodes[0].(*core.Lexeme)
					n, err := NewLimit(count)
					if err != nil {
						return nil, err
					}
					n.info = tree.NewEnvelope(b.Kind, b.Span)
					return n, nil
				}
			}
			return nil, fmt.Errorf("postgres: missing LIMIT count")
		}
		if b.Kind != DistinctOnKind {
			return nil, fmt.Errorf("postgres: unknown node %q", b.Kind)
		}
		var expressions []tree.Node
		for _, f := range b.Fields {
			expressions = append(expressions, f.Nodes...)
		}
		return &DistinctOn{info: tree.NewEnvelope(b.Kind, b.Span), expressions: expressions}, nil
	}
	decoder := core.TokenDecoder{Kind: DollarStringToken, Quoted: true, Decode: func(raw string) string {
		width := strings.IndexByte(raw[1:], '$') + 2
		return raw[width : len(raw)-width]
	}}
	return p.Configure(Grammar(), core.Builders(snapshot, build, decoder,
		core.TokenDecoder{Kind: QuotedStringToken, Quoted: true, Decode: func(raw string) string { return strings.ReplaceAll(raw[1:len(raw)-1], "''", "'") }},
		core.TokenDecoder{Kind: DollarNameToken, Decode: strings.ToLower}), p.Limits{})
}

// Print shares core rendering and adds DISTINCT ON and LIMIT. Dollar strings print as
// standard single-quoted values, with doubled apostrophes and literal newlines.
// Parse/print/parse preserves the canonical tree, not the original spelling.
func Print(root tree.Node) (string, error) {
	return core.PrintWith(root, func(n tree.Node, recur func(tree.Node) (string, error)) (string, error) {
		if limit, ok := n.(*Limit); ok {
			count, err := recur(limit.Count())
			return "LIMIT " + count, err
		}
		modifier, ok := n.(*DistinctOn)
		if !ok {
			return "", fmt.Errorf("postgres: unsupported node %q", n.Info().Kind)
		}
		parts := make([]string, len(modifier.expressions))
		for i, child := range modifier.expressions {
			var err error
			parts[i], err = recur(child)
			if err != nil {
				return "", err
			}
		}
		return "DISTINCT ON (" + strings.Join(parts, ", ") + ")", nil
	})
}
