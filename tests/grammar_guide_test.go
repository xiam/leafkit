package tests

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGrammarGuideMatchesExampleSources(t *testing.T) {
	guide, err := os.ReadFile("../docs/writing-a-grammar.md")
	require.NoError(t, err)
	link := regexp.MustCompile(`^<!-- example: (tests/[a-z-]+/[a-z_]+\.go)#([a-z-]+) -->$`)
	lines := strings.Split(string(guide), "\n")
	blocks := 0
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "```") && !strings.HasPrefix(line, "~~~") {
			continue
		}
		require.NotEqualf(t, 0, i, "guide line %d: expected a Go block with an example marker", i+1)
		require.Equalf(t, "```go", lines[i], "guide line %d: expected a Go block with an example marker", i+1)
		match := link.FindStringSubmatch(lines[i-1])
		require.Truef(t, match != nil, "guide line %d: missing or invalid example marker", i+1)
		start := i + 1
		for i = start; i < len(lines) && lines[i] != "```"; i++ {
		}
		require.NotEqualf(t, len(lines), i, "guide line %d: unclosed block", start)
		source, err := os.ReadFile(filepath.Join("..", match[1]))
		require.NoError(t, err)
		open := "\n// guide:" + match[2] + "\n"
		require.Equalf(t, 1, strings.Count(string(source), open), "expected one snippet %s#%s", match[1], match[2])
		_, rest, _ := strings.Cut(string(source), open)
		excerpt, _, found := strings.Cut(rest, "\n// guide:end\n")
		require.Truef(t, found, "missing excerpt or end marker in %s#%s", match[1], match[2])
		require.NotEqualf(t, "", strings.TrimSpace(excerpt), "missing excerpt or end marker in %s#%s", match[1], match[2])
		{
			got, want := strings.Join(lines[start:i], "\n"), strings.TrimSuffix(excerpt, "\n")
			assert.Equalf(t, want, got, "guide block at line %d differs from %s#%s", start, match[1], match[2])
		}
		blocks++
	}
	require.NotEqualf(t, 0, blocks, "guide has no source-checked blocks")
}
