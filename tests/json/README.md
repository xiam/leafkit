# JSON

Run `go test ./tests/json`. The [grammar and tests](json_test.go) recognize
objects, arrays, strings, numbers, booleans and null. Object members are
ordered children, so the printer can preserve the input member order.
The scalar leaves keep the original number and string spellings.

For example, `{ "n": [ -0.25e+2, true, null ], "e": {} }` prints as
`{"n":[-0.25e+2,true,null],"e":{}}`. The printer is an `eval.Fold` over the
tree. `TestJSONGrammar` checks the input and output with the standard
library's JSON validator alongside its exact tree and diagnostic assertions.

A missing object value leaves a diagnostic where the value belongs and
keeps the next member. A trailing comma and a leading zero produce file
diagnostics.
