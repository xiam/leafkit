// Package translate demonstrates a bounded, read-only SQL AST translation.
package translate

import (
	"fmt"
	"slices"
	"strings"

	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tests/sql/core"
	"github.com/xiam/leafkit/tests/sql/postgres"
	"github.com/xiam/leafkit/tests/sql/tsql"
	"github.com/xiam/leafkit/tree"
)

// UniqueIntegerKey is a caller assertion about the source relation: Column is
// unique, non-null and integer-valued. Table and Column use decoded AST names.
// The pass cannot infer schema constraints or verify the assertion from SQL.
type UniqueIntegerKey struct {
	Table  []string
	Column string
}

// Diagnostic refers to the original source, never to generated SQL offsets.
type Diagnostic struct {
	Code    string
	Span    tree.Span
	Message string
}

// ToPostgres preserves portable subtrees by reference and replaces ordinary
// SQL Server TOP with LIMIT. TOP requires one table, no joins or DISTINCT, and
// exactly one ORDER BY identifier matching key (ASC or DESC). Unsupported input
// returns no partial target. The source tree and its snapshots remain unchanged.
func ToPostgres(root tree.Node, key *UniqueIntegerKey) (tree.Node, []Diagnostic) {
	fail := func(code string, n tree.Node, message string) (tree.Node, []Diagnostic) {
		var span tree.Span
		if n != nil {
			span = n.Info().Span
		}
		return nil, []Diagnostic{{Code: code, Span: span, Message: message}}
	}
	if err := tree.Validate(root); err != nil {
		return fail("invalid-tree", nil, err.Error())
	}
	if ds := core.Diagnostics(root); len(ds) != 0 {
		return fail("invalid-sql", ds[0], "source contains a parser diagnostic")
	}
	file, ok := root.(*core.File)
	if !ok {
		return fail("unsupported-root", root, "expected one parsed SQL file")
	}
	child, ok := file.Child(0)
	sel, isSelect := child.(*core.Select)
	if _, more := file.Child(1); !ok || !isSelect || more {
		return fail("unsupported-root", root, "expected exactly one SELECT")
	}
	// A custom public grammar can produce a valid generic tree without the
	// shared SQL projection wrapper. Reject that shape before reading aliases.
	for _, node := range sel.Projections() {
		if _, ok := node.(*core.Projection); !ok {
			return fail("unsupported-projection", node, "expected a shared SQL projection with an expression field")
		}
	}
	var top *tsql.Top
	for n := range tree.Walk(root) {
		switch node := n.(type) {
		case *tsql.Top:
			if node.WithTies() {
				ties, _ := node.Child(1)
				return fail("with-ties", ties, "WITH TIES cannot be translated to LIMIT without losing boundary rows")
			}
			top = node
		case *core.File, *core.Select, *core.Identifier, *core.Lexeme, *core.Projection, *core.Table, *core.Join, *core.Comparison, *core.Order, *core.Distinct, *core.Star:
		default:
			return fail("unsupported-node", n, "unsupported source construct: "+string(n.Info().Kind))
		}
	}
	if top == nil {
		return root, nil
	}
	if len(sel.Field("modifiers")) != 1 || sel.One("modifiers") != top || len(sel.Field("joins")) != 0 || len(sel.Field("suffix")) != 0 {
		return fail("unsupported-top", top, "TOP translation requires a single table without joins, DISTINCT or another row limit")
	}
	if key == nil || key.Column == "" || len(key.Table) == 0 || !slices.Equal(names(sel.From().Name()), key.Table) {
		return fail("ordering-key", top, "TOP translation requires an asserted unique, non-null integer key for this table")
	}
	orders := sel.Field("order")
	if len(orders) != 1 {
		return fail("ordering-key", top, "TOP translation requires exactly one ORDER BY key")
	}
	order, ok := orders[0].(*core.Order)
	if !ok {
		return fail("ordering-key", top, "unsupported ORDER BY")
	}
	ident, ok := order.One("expression").(*core.Identifier)
	parts := names(ident)
	qualifier := key.Table
	if alias := sel.From().Alias(); alias != nil {
		qualifier = []string{alias.Value()}
	}
	bindsKey := func(parts []string) bool {
		return slices.Equal(parts, []string{key.Column}) ||
			slices.Equal(parts, append(slices.Clone(qualifier), key.Column))
	}
	if !ok || !bindsKey(parts) {
		return fail("ordering-key", order, "ORDER BY must reference the asserted unique integer key")
	}
	// A bare ORDER BY name can bind to a projection alias before the input
	// column. Prove that alias's expression is the key; reject duplicate or
	// possibly case-insensitive collisions rather than changing source meaning.
	if len(parts) == 1 {
		aliases := 0
		for _, node := range sel.Projections() {
			projection := node.(*core.Projection)
			alias, _ := projection.One("alias").(*core.Lexeme)
			if alias == nil || !strings.EqualFold(alias.Value(), parts[0]) {
				continue
			}
			aliases++
			expression, _ := projection.One("expression").(*core.Identifier)
			if aliases > 1 || !bindsKey(names(expression)) {
				return fail("ordering-key", order, "ORDER BY alias does not unambiguously name the asserted input key")
			}
		}
	}
	limit, err := postgres.NewLimit(top.Count())
	if err != nil {
		return fail("invalid-count", top, err.Error())
	}
	fields := []p.Field[tree.Node]{}
	for _, name := range []string{"projections", "from", "joins", "where", "order"} {
		fields = append(fields, p.Field[tree.Node]{Name: name, Nodes: sel.Field(name)})
	}
	fields = append(fields, p.Field[tree.Node]{Name: "suffix", Nodes: []tree.Node{limit}})
	builders := core.Builders(nil, nil)
	target, err := builders.Node(p.Build[tree.Node]{Kind: core.SelectKind, Span: sel.Info().Span, Fields: fields})
	if err != nil {
		return fail("build", sel, err.Error())
	}
	result, err := builders.File(root.Info().Span, []tree.Node{target}, nil)
	if err != nil {
		return fail("build", root, err.Error())
	}
	return result, nil
}

func names(n *core.Identifier) []string {
	if n == nil {
		return nil
	}
	parts := n.Parts()
	result := make([]string, len(parts))
	for i, part := range parts {
		result[i] = part.Value()
	}
	return result
}

// PrintPostgres quotes every decoded identifier component, including names
// that are reserved only in the target dialect. It reads shared fields without
// copying or mutating their nodes. Source ranges still refer to source SQL.
func PrintPostgres(root tree.Node) (string, error) {
	var render func(tree.Node) (string, error)
	render = func(n tree.Node) (string, error) {
		if n == nil {
			return "", fmt.Errorf("translate: missing printer operand")
		}
		list := func(nodes []tree.Node, sep string) (string, error) {
			parts := make([]string, len(nodes))
			for i, node := range nodes {
				var err error
				parts[i], err = render(node)
				if err != nil {
					return "", err
				}
			}
			return strings.Join(parts, sep), nil
		}
		alias := func(text string, a *core.Lexeme) (string, error) {
			if a == nil {
				return text, nil
			}
			name, err := render(a)
			return text + " AS " + name, err
		}
		switch node := n.(type) {
		case *core.File:
			var children []tree.Node
			for i := 0; ; i++ {
				child, ok := node.Child(i)
				if !ok {
					break
				}
				children = append(children, child)
			}
			return list(children, " ")
		case *core.Lexeme:
			if node.Info().Kind == core.NameKind {
				return `"` + strings.ReplaceAll(node.Value(), `"`, `""`) + `"`, nil
			}
		case *core.Identifier:
			var parts []tree.Node
			for _, part := range node.Parts() {
				parts = append(parts, part)
			}
			return list(parts, ".")
		case *core.Table:
			text, err := render(node.Name())
			if err != nil {
				return "", err
			}
			return alias(text, node.Alias())
		case *core.Projection:
			text, err := render(node.One("expression"))
			if err != nil {
				return "", err
			}
			a, _ := node.One("alias").(*core.Lexeme)
			return alias(text, a)
		case *core.Comparison:
			return list([]tree.Node{node.One("left"), node.One("operator"), node.One("right")}, " ")
		case *core.Join:
			text, err := list([]tree.Node{node.One("table"), node.One("condition")}, " ON ")
			return "INNER JOIN " + text, err
		case *core.Order:
			children := []tree.Node{node.One("expression")}
			if d := node.One("direction"); d != nil {
				children = append(children, d)
			}
			return list(children, " ")
		case *core.Select:
			text := "SELECT "
			if mods := node.Field("modifiers"); len(mods) != 0 {
				m, err := list(mods, " ")
				if err != nil {
					return "", err
				}
				text += m + " "
			}
			proj, err := list(node.Projections(), ", ")
			if err != nil {
				return "", err
			}
			from, err := render(node.From())
			if err != nil {
				return "", err
			}
			text += proj + " FROM " + from
			for _, clause := range []struct{ field, prefix, sep string }{{"joins", " ", " "}, {"where", " WHERE ", " "}, {"order", " ORDER BY ", ", "}, {"suffix", " ", " "}} {
				nodes := node.Field(clause.field)
				if len(nodes) == 0 {
					continue
				}
				part, err := list(nodes, clause.sep)
				if err != nil {
					return "", err
				}
				text += clause.prefix + part
			}
			return text, nil
		}
		return postgres.Print(n)
	}
	return render(root)
}
