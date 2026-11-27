# SQL composition benchmarks

Run the four SQL example parsers on the same frozen queries. The benchmark
checks normalized trees and table visits before measuring parsing. Start at
the module root with Go 1.23 or later:

```sh
GOTOOLCHAIN=go1.23.12 go test ./tests/sql/bench -count=1
GOTOOLCHAIN=go1.23.12 go test ./tests/sql/bench -run '^$' -bench . -benchmem -count=5 -cpu=1
```

On a systemd runner, put each command inside its own memory-capped scope:
`systemd-run --user --scope -p MemoryMax=8G -p MemorySwapMax=0 -q --`.
Keep measurements private. See [the method](method.md) for timed work,
input schedules, corpus provenance and reproducibility records.
