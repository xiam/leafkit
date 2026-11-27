package tree

import (
	"fmt"
	"io"
)

// Fprint writes a structural view of a valid tree. It writes one line per
// visited edge, including references to previously printed nodes.
func Fprint(w io.Writer, root Node) error {
	seen := make(map[ID]bool)
	var err error
	walk(root, func(path Path, node Node) (bool, bool) {
		info := node.Info()
		if len(path) != 0 {
			step := path[len(path)-1]
			if _, err = fmt.Fprintf(w, "#%d[%d] -> ", step.Parent.Info().ID.serial, step.Index); err != nil {
				return false, false
			}
		}
		if seen[info.ID] {
			_, err = fmt.Fprintf(w, "#%d (shared)\n", info.ID.serial)
			return false, err == nil
		}
		seen[info.ID] = true
		if info.Span.Source == "" {
			_, err = fmt.Fprintf(w, "#%d %q <unknown>\n", info.ID.serial, info.Kind)
		} else {
			_, err = fmt.Fprintf(w, "#%d %q %q[%d,%d)\n", info.ID.serial, info.Kind,
				info.Span.Source, info.Span.Start, info.Span.End)
		}
		return true, err == nil
	})
	return err
}
