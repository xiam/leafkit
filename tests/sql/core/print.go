package core

import (
	"fmt"
	"strings"

	"github.com/xiam/leafkit/tree"
)

// ExtensionPrinter prints a dialect-owned node using recur for shared nodes.
// It must reject unsupported constructs rather than silently dropping them.
type ExtensionPrinter func(node tree.Node, recur func(tree.Node) (string, error)) (string, error)

// Print emits portable SQL for the core subset. It rejects diagnostics and
// dialect-only constructs. It emits ANSI quotes for decoded delimited values.
func Print(root tree.Node) (string, error) { return PrintWith(root, nil) }

// PrintWith keeps core printing shared while allowing dialect-owned nodes.
func PrintWith(root tree.Node, extension ExtensionPrinter) (string, error) {
	var render func(tree.Node) (string, error)
	render = func(n tree.Node) (string, error) {
		if n == nil {
			return "", fmt.Errorf("sql: missing printer operand")
		}
		list := func(nodes []tree.Node, separator string) (string, error) {
			parts := make([]string, len(nodes))
			for i, child := range nodes {
				var err error
				parts[i], err = render(child)
				if err != nil {
					return "", err
				}
			}
			return strings.Join(parts, separator), nil
		}
		fields := func(s *structure, names ...string) (string, error) {
			var nodes []tree.Node
			for _, name := range names {
				child := s.One(name)
				if child == nil {
					return "", fmt.Errorf("sql: missing %s in %s", name, n.Info().Kind)
				}
				nodes = append(nodes, child)
			}
			return list(nodes, " ")
		}
		alias := func(s *structure, expr string) (string, error) {
			if child := s.One("alias"); child != nil {
				text, err := render(child)
				return expr + " AS " + text, err
			}
			return expr, nil
		}
		switch node := n.(type) {
		case *Lexeme:
			if node.Info().Kind == NameKind && node.Quoted() {
				return "\"" + strings.ReplaceAll(node.Value(), "\"", "\"\"") + "\"", nil
			}
			if node.Info().Kind == StringKind {
				return "'" + strings.ReplaceAll(node.Value(), "'", "''") + "'", nil
			}
			if node.decode != nil || node.Info().Kind == OperatorKind || node.Info().Kind == DirectionKind {
				return node.Value(), nil
			}
			return node.Raw(), nil
		case *File:
			return list(node.children, " ")
		case *Identifier:
			return list(node.children, ".")
		case *Star:
			return "*", nil
		case *Distinct:
			return "DISTINCT", nil
		case *Projection:
			text, err := render(node.One("expression"))
			if err != nil {
				return "", err
			}
			return alias(node.structure, text)
		case *Table:
			text, err := render(node.One("name"))
			if err != nil {
				return "", err
			}
			return alias(node.structure, text)
		case *Comparison:
			return fields(node.structure, "left", "operator", "right")
		case *Join:
			table, err := render(node.One("table"))
			if err != nil {
				return "", err
			}
			cond, err := render(node.One("condition"))
			return "INNER JOIN " + table + " ON " + cond, err
		case *Order:
			text, err := render(node.One("expression"))
			if err != nil {
				return "", err
			}
			if direction := node.One("direction"); direction != nil {
				d, err := render(direction)
				return text + " " + d, err
			}
			return text, nil
		case *Select:
			modifiers, err := list(node.Field("modifiers"), " ")
			if err != nil {
				return "", err
			}
			projections, err := list(node.Projections(), ", ")
			if err != nil {
				return "", err
			}
			from, err := render(node.One("from"))
			if err != nil {
				return "", err
			}
			if projections == "" {
				return "", fmt.Errorf("sql: empty projections")
			}
			text := "SELECT "
			if modifiers != "" {
				text += modifiers + " "
			}
			text += projections + " FROM " + from
			joins, err := list(node.Field("joins"), " ")
			if err != nil {
				return "", err
			}
			if joins != "" {
				text += " " + joins
			}
			if where := node.One("where"); where != nil {
				condition, err := render(where)
				if err != nil {
					return "", err
				}
				text += " WHERE " + condition
			}
			order, err := list(node.Field("order"), ", ")
			if err != nil {
				return "", err
			}
			if order != "" {
				text += " ORDER BY " + order
			}
			suffix, err := list(node.Field("suffix"), " ")
			if err != nil {
				return "", err
			}
			if suffix != "" {
				text += " " + suffix
			}
			return text, nil
		}
		if extension != nil {
			return extension(n, render)
		}
		return "", fmt.Errorf("sql: cannot print %q", n.Info().Kind)
	}
	return render(root)
}
