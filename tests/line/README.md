# Line format

Run `go test ./tests/line`. The [grammar and tests](line_test.go) read a
section header followed by key and value lines. A `#` introduces a comment.
Values can contain spaces. Both line endings and the final end of input can
end an entry.

```text
[user]
name = Ada Lovelace # Display name
[place]
city = Zürich
```

The grammar requests a value token after `=`. Under that request the
lexer reads up to a comment or line ending. The evaluator builds a nested
map, trimming surrounding whitespace from values. The sample gives the
value `Zürich` at `place.city`.

`TestLineGrammar` compares tree shapes and map entries. It checks that a
missing value or equals sign records a diagnostic and leaves the next
entry available.
