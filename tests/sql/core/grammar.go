package core

import (
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tree"
)

const (
	FileKind       tree.Kind = "sql:file"
	SelectKind     tree.Kind = "sql:select"
	IdentifierKind tree.Kind = "sql:identifier"
	NameKind       tree.Kind = "sql:name"
	StringKind     tree.Kind = "sql:string"
	NumberKind     tree.Kind = "sql:number"
	StarKind       tree.Kind = "sql:star"
	DistinctKind   tree.Kind = "sql:distinct"
	ProjectionKind tree.Kind = "sql:projection"
	TableKind      tree.Kind = "sql:table"
	JoinKind       tree.Kind = "sql:inner-join"
	ComparisonKind tree.Kind = "sql:comparison"
	OperatorKind   tree.Kind = "sql:operator"
	OrderKind      tree.Kind = "sql:order"
	DirectionKind  tree.Kind = "sql:direction"
)

func Token(kind p.TokenKind) p.Rule               { return p.Tok(p.Kind(kind)) }
func Skip(kind p.TokenKind) p.Rule                { return p.Drop(Token(kind)) }
func kw(word string) p.Rule                       { return Skip(Keyword(word)) }
func sym(text string) p.Rule                      { return Skip(Symbol(text)) }
func makeNode(kind tree.Kind, body p.Rule) p.Rule { return p.Make(string(kind), kind, body) }
func choose(rules ...p.Rule) p.Rule               { return p.Choose(p.ChoiceSpec{}, rules...) }

// Grammar returns independent rule maps. Replace named rules before Configure.
// All composition points retain parser.Ref calls: name, identifier, operand,
// expression, comparison, modifiers, projection, table, join, order, select,
// and suffix. The empty suffix is the place for dialect LIMIT tails.
func Grammar() p.Grammar {
	ref, bind := p.Ref, p.Bind
	list := func(item p.Rule) p.Rule { return p.Seq(item, p.Many(p.Seq(sym(","), item))) }
	alias := p.Opt(p.Seq(kw("AS"), bind("alias", ref("name"))))
	operators := []p.Rule{}
	for _, op := range []string{"=", "<>", "!=", "<=", ">=", "<", ">"} {
		operators = append(operators, Token(Symbol(op)))
	}
	return p.Grammar{Start: "file", Rules: map[string]p.Rule{
		"file":       p.Seq(ref("select"), p.Opt(sym(";"))),
		"name":       makeNode(NameKind, choose(Token(NameToken), Token(QuotedNameToken))),
		"identifier": makeNode(IdentifierKind, p.Seq(ref("name"), p.Many(p.Seq(sym("."), ref("name"))))),
		"operand":    choose(ref("identifier"), makeNode(StringKind, Token(StringToken)), makeNode(NumberKind, Token(NumberToken))),
		"expression": ref("operand"),
		"modifiers":  p.Opt(makeNode(DistinctKind, kw("DISTINCT"))),
		"projection": makeNode(ProjectionKind, choose(bind("expression", makeNode(StarKind, sym("*"))), p.Seq(bind("expression", ref("expression")), alias))),
		"table":      makeNode(TableKind, p.Seq(bind("name", ref("identifier")), alias)),
		"comparison": makeNode(ComparisonKind, p.Seq(
			bind("left", ref("expression")),
			bind("operator", makeNode(OperatorKind, choose(operators...))),
			bind("right", ref("expression")))),
		"join": makeNode(JoinKind, p.Seq(
			kw("INNER"), kw("JOIN"), bind("table", ref("table")),
			kw("ON"), bind("condition", ref("comparison")))),
		"order": makeNode(OrderKind, p.Seq(
			bind("expression", ref("expression")),
			p.Opt(bind("direction", makeNode(DirectionKind,
				choose(Token(Keyword("ASC")), Token(Keyword("DESC")))))))),
		"suffix": p.Seq(),
		"select": makeNode(SelectKind, p.Seq(
			kw("SELECT"), bind("modifiers", ref("modifiers")),
			bind("projections", list(ref("projection"))),
			kw("FROM"), bind("from", ref("table")), bind("joins", p.Many(ref("join"))),
			p.Opt(p.Seq(kw("WHERE"), bind("where", ref("comparison")))),
			p.Opt(p.Seq(kw("ORDER"), kw("BY"), bind("order", list(ref("order"))))),
			bind("suffix", ref("suffix")))),
	}}
}
