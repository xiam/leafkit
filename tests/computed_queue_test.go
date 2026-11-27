package tests

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	scan "github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/rules/core"
	adapter "github.com/xiam/leafkit/adapter/bytescan"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tree"
)

const queueLead = "x;let x;{let x;{let y;y;};x;y;};x;"
const queueHeader = "<A<~'B'\n"
const queueInterpolated = "hello${x+x}\n"
const queueLiteral = "  raw${x}\n  B\n"

var strictBody = p.Request{Goal: "interpolated", Argument: "A", Layout: p.Lines}
var quotedBody = p.Request{Goal: "quoted", Argument: "B", Layout: p.Indentation}
var embeddedBody = p.Request{Goal: "embedded", Argument: "${:x;", Layout: p.Joined}

func queueNames(state p.State) p.State {
	names, _, _ := strings.Cut(string(state), "#")
	return p.State(names)
}

func queueRequest(view p.View) p.Request {
	_, queue, _ := strings.Cut(string(view.State), "#")
	head, _, _ := strings.Cut(queue, ",")
	if delimiter, quoted := strings.CutPrefix(head, "~'"); quoted {
		return p.Request{Goal: "quoted", Argument: strings.TrimSuffix(delimiter, "'"), Layout: p.Indentation}
	}
	return p.Request{Goal: "interpolated", Argument: head, Layout: p.Lines}
}

func requestFromHeader(view p.View, capture string) p.Request {
	req := queueRequest(view)
	opener := view.Parts.Tokens(capture)[0]
	if req.Argument != strings.TrimPrefix(opener.Text, "<") || queueNames(view.State) != "x;" {
		return p.Request{Goal: "invalid-header"}
	}
	return req
}

func dequeue(view p.View) p.State {
	names, queue, _ := strings.Cut(string(view.State), "#")
	_, rest, more := strings.Cut(queue, ",")
	if more {
		return p.State(names + "#" + rest)
	}
	return p.State(names)
}

func queuedUse(name, class string) p.Rule {
	predicate := p.Guard(p.Tok(p.Word(name)), func(view p.View) bool {
		view.State = queueNames(view.State)
		return useName(view, name) == class
	})
	return p.Make("name", tree.Kind(class+"-"+name), predicate)
}

func queuedNameStatement(name, class string) p.Rule {
	return p.Seq(queuedUse(name, class), skip(";"))
}

func queuedScope(body p.Rule) p.Rule {
	enter := p.Update(p.Seq(), func(view p.View) p.State { return view.State + "/" })
	return p.Scope(p.Seq(skip("{"), enter, body, skip("}"), skip(";")))
}

func queuedProgram() p.Rule {
	return p.Program("header", map[string]p.ProgramState{
		"header": {Calls: []string{"header"}, ReturnsTo: []string{"first"}, Control: func(p.View) p.Move {
			return p.Call("header", "first", "header")
		}},
		"first": {Calls: []string{"body-a"}, ReturnsTo: []string{"pop-a"}, Control: func(view p.View) p.Move {
			return p.CallWithLex("body-a", "pop-a", "first", requestFromHeader(view, "header"))
		}},
		"pop-a": {Jumps: []string{"second"}, Control: func(view p.View) p.Move {
			return p.SetState("second", dequeue(view))
		}},
		"second": {Calls: []string{"body-b"}, ReturnsTo: []string{"pop-b"}, Control: func(view p.View) p.Move {
			return p.CallWithLex("body-b", "pop-b", "second", queueRequest(view))
		}},
		"pop-b": {Jumps: []string{"done"}, Control: func(view p.View) p.Move {
			return p.SetState("done", dequeue(view))
		}},
		"done": {Control: func(p.View) p.Move { return p.Return() }},
	})
}

func queuedGrammar(hand, chart bool) p.Grammar {
	decl := p.Update(p.Seq(p.Tok(p.Word("let")), p.Bind("name", tagged("word"))), declareName)
	if hand {
		decl = programDeclaration()
	}
	header := p.Update(p.Seq(p.Bind("first", tagged("opener")), p.Bind("second", tagged("opener")), skip("line")), func(view p.View) p.State {
		first := strings.TrimPrefix(view.Parts.Tokens("first")[0].Text, "<")
		second := strings.TrimPrefix(view.Parts.Tokens("second")[0].Text, "<")
		return view.State + p.State("#"+first+","+second)
	})
	queue := p.Seq(
		p.Update(p.WithLex(p.Ref("header"), func(view p.View) p.Request {
			return requestFromHeader(view, "first")
		}, p.Ref("body-a")), dequeue),
		p.Update(p.WithLex(p.Seq(), queueRequest, p.Ref("body-b")), dequeue),
	)
	if hand {
		queue = queuedProgram()
	}
	name := queuedUse("x", "local")
	if chart {
		name = p.Choose(p.ChoiceSpec{Policy: p.Longest}, name)
	}
	expression := p.Expr("expression", name, p.Operator{Form: p.Infix, Match: p.Kind("+"), Power: 1, Kind: "add"})
	interpolation := p.WithLex(p.Bind("start", tagged("interpolate")), func(view p.View) p.Request {
		return p.Request{Goal: "embedded", Argument: view.Parts.Tokens("start")[0].Text + ":" + string(queueNames(view.State)), Layout: p.Joined}
	}, p.Seq(expression, skip("}")))
	indent := p.Insert(p.Kind("body-indent"), func(look p.Look, view p.View) bool {
		return view.State == "x;#~'B'" && p.Matches(look, p.Kind("end")) && strings.HasPrefix(look.Token.Text, "  ")
	})
	return p.Grammar{Start: "file", Rules: map[string]p.Rule{
		"file": p.Seq(queuedNameStatement("x", "call"), p.Ref("declare"),
			queuedScope(p.Seq(p.Ref("declare"),
				queuedScope(p.Seq(p.Ref("declare"), queuedNameStatement("y", "local"))),
				queuedNameStatement("x", "local"), queuedNameStatement("y", "call"))),
			queuedNameStatement("x", "local"), queue, queuedNameStatement("x", "local"), stateCheckpoint("x;")),
		"declare": p.Make("declare", "declare", p.Seq(decl, skip(";"))),
		"header":  header,
		"body-a": p.Make("body", "unquoted", p.Seq(p.Make("text", "body-text", tagged("text")), interpolation, skip("line"),
			p.Recover("closer", skip("end"), tokenBoundaries(true, "sync")))),
		"body-b": p.Make("body", "quoted", p.Seq(p.Make("text", "quoted-text", tagged("text")), indent, skip("end"))),
	}}
}

func queuedBytescan(t *testing.T, input *adapterInput) p.Source {
	t.Helper()
	source, err := adapter.NewReplay(snapshot, input.open, struct{}{}, func(cur *cursor.Reader, _ struct{}, req p.Request) (scan.Source, func() struct{}, error) {
		lexer := scan.NewWithCursor(cur)
		switch {
		case req == (p.Request{}):
			lexer.MustAddRule("word", core.AnyLiteral("let", "x", "y"))
			lexer.MustAddRule("space", core.Literal(" "))
			lexer.MustAddRule("line", core.Literal("\n"))
			letters := core.RuneRun(core.RuneRange('A', 'Z'))
			lexer.MustAddRule("opener", core.Sequence(core.Literal("<"), core.Choice(letters, core.Sequence(core.Literal("~'"), letters, core.Literal("'")))))
			for _, char := range []string{";", "{", "}"} {
				lexer.MustAddRule(scan.TokenType(char), core.Literal(char))
			}
		case req.Goal == "interpolated" && req.Layout == p.Lines && req.Argument != "":
			lexer.MustAddRule("text", core.Literal("hello"))
			lexer.MustAddRule("interpolate", core.Literal("${"))
			lexer.MustAddRule("line", core.Literal("\n"))
			lexer.MustAddRule("end", core.AtLineStart(core.Literal(req.Argument+"\n")))
			lexer.MustAddRule("wrong-end", core.AtLineStart(core.Literal("Z\n")))
			lexer.MustAddRule("sync", core.AtLineStart(core.Literal("!\n")))
		case req == embeddedBody:
			lexer.MustAddRule("word", core.Literal("x"))
			for _, char := range []string{"+", "}"} {
				lexer.MustAddRule(scan.TokenType(char), core.Literal(char))
			}
		case req.Goal == "quoted" && req.Layout == p.Indentation && req.Argument != "":
			lexer.MustAddRule("text", core.Literal("  raw${x}\n"))
			lexer.MustAddRule("end", core.AtLineStart(core.Literal("  "+req.Argument+"\n")))
		default:
			return nil, nil, fmt.Errorf("unsupported queued-body request %+v", req)
		}
		return lexer, nil, nil
	}, adapter.Options{Trivia: []scan.TokenType{"space"}})
	require.NoError(t, err)
	return source
}

type lexicalSample struct {
	kind p.TokenKind
	text string
}

func queuedHandmade(_ *testing.T, input *adapterInput) p.Source {
	return newOpaqueRequestSource(func(mark p.Mark, req p.Request) (p.Read, error) {
		start := mark.Byte
		if start == len(input.text) && input.sealed {
			return eof(start), nil
		}
		if start >= input.available {
			return shortage(), nil
		}
		var choices []lexicalSample
		var leading []p.Trivia
		switch req {
		case p.Request{}:
			if input.text[start] == ' ' {
				leading = []p.Trivia{{Kind: "space", Span: span(start, start+1)}}
				start++
			}
			choices = []lexicalSample{{"word", "let"}, {"word", "x"}, {"word", "y"}, {"opener", "<A"}, {"opener", "<~'B'"}, {"line", "\n"}, {";", ";"}, {"{", "{"}, {"}", "}"}}
		case strictBody:
			choices = []lexicalSample{{"text", "hello"}, {"interpolate", "${"}, {"line", "\n"}, {"end", "A\n"}, {"wrong-end", "Z\n"}, {"sync", "!\n"}}
		case quotedBody:
			choices = []lexicalSample{{"text", "  raw${x}\n"}, {"end", "  B\n"}}
		case embeddedBody:
			choices = []lexicalSample{{"word", "x"}, {"+", "+"}, {"}", "}"}}
		default:
			return p.Read{}, fmt.Errorf("unexpected queue request %+v", req)
		}
		for _, choice := range choices {
			if strings.HasPrefix(input.text[start:], choice.text) {
				end := start + len(choice.text)
				if end > input.available {
					return shortage(), nil
				}
				token := fixtureToken(choice.kind, choice.text, start, end)
				token.Leading = leading
				return ready(token, p.Mark{Byte: end}), nil
			}
		}
		return p.Read{}, fmt.Errorf("unexpected queued-body input at %v under %+v", mark, req)
	})
}

func queuedPlan(broken bool) readingPlan {
	var uses []grammarUse
	read := func(site string, state p.State, req p.Request, advance bool) {
		uses = append(uses, grammarUse{site, state, req, advance})
	}
	take := func(site string, state p.State) { read(site, state, p.Request{}, true) }
	name := func(site string, state p.State) { take(site+"/name", state); take(site+"/term", state) }
	declare := func(site string, before, after p.State) {
		take(site+"/keyword", before)
		take(site+"/declared-name", before)
		take(site+"/term", after)
	}
	name("first-use", "")
	declare("declare-global", "", "x;")
	take("scope/open", "x;")
	declare("shadow-x", "x;/", "x;/x;")
	take("nested/open", "x;/x;")
	declare("declare-y", "x;/x;/", "x;/x;/y;")
	name("nested-local-y", "x;/x;/y;")
	take("nested/close", "x;/x;/y;")
	take("nested/term", "x;/x;/y;")
	name("scoped-local-x", "x;/x;")
	name("scoped-call-y", "x;/x;")
	take("scope/close", "x;/x;")
	take("scope/term", "x;/x;")
	name("outer-local-x", "x;")
	take("header/open-a", "x;")
	take("header/open-b", "x;")
	take("header/line", "x;")
	const both = "x;#A,~'B'"
	read("body-a/text", both, strictBody, true)
	read("body-a/interpolation", both, strictBody, true)
	read("expression/left", both, embeddedBody, true)
	read("expression/operator", both, embeddedBody, true)
	read("expression/right", both, embeddedBody, true)
	read("expression/operator-stop", both, embeddedBody, false)
	read("expression/close", both, embeddedBody, true)
	read("body-a/line", both, strictBody, true)
	if broken {
		read("body-a/rejected-delimiter", both, strictBody, false)
		read("body-a/skip-delimiter", both, strictBody, true)
		read("body-a/consume-sync", both, strictBody, true)
	} else {
		read("body-a/close", both, strictBody, true)
	}
	read("body-b/quoted-text", "x;#~'B'", quotedBody, true)
	read("body-b/indent-insertion", "x;#~'B'", quotedBody, false)
	read("body-b/close", "x;#~'B'", quotedBody, true)
	name("final-use", "x;")
	read("file/final", "x;", p.Request{}, false)
	return readingPlan{"queued declarations", uses}
}

func queuedBuilders() p.Builders[tree.Node] {
	build := builders()
	node := build.Node
	build.Node = func(in p.Build[tree.Node]) (tree.Node, error) {
		if in.Kind == "body-text" || in.Kind == "quoted-text" {
			token, ok := in.Tokens.At(0)
			if !ok || in.Tokens.Len() != 1 {
				return nil, fmt.Errorf("expected one owned body token")
			}
			return tree.NewValue(in.Kind, in.Span, token.Text), nil
		}
		if in.Kind == "quoted" {
			indent, ok := in.Tokens.At(0)
			closer, closerOK := in.Tokens.At(1)
			if !ok || !closerOK || in.Tokens.Len() != 2 || indent.Kind != "body-indent" ||
				!indent.Synthetic || indent.Span != span(closer.Span.Start, closer.Span.Start) {
				return nil, fmt.Errorf("quoted body missing its placed indentation marker")
			}
		}
		return node(in)
	}
	return build
}

func TestComputedQueuedBodies(t *testing.T) {
	for _, broken := range []bool{false, true} {
		end := "A\n"
		if broken {
			end = "Z\n!\n"
		}
		input := queueLead + queueHeader + queueInterpolated + end + queueLiteral + "x;"
		delimiterAt := len(queueLead + queueHeader + queueInterpolated)
		var wantProblems []stateDiagnostic
		unquoted := "unquoted(body-text,add(local-x,local-x))"
		if broken {
			unquoted = "unquoted(body-text,add(local-x,local-x),leafkit:error)"
			bad := fixtureToken("wrong-end", "Z\n", delimiterAt, delimiterAt+2)
			sync := fixtureToken("sync", "!\n", delimiterAt+2, delimiterAt+4)
			wantProblems = []stateDiagnostic{{"closer", p.Problem{Span: span(delimiterAt, delimiterAt+4),
				Expected: []p.Pattern{p.Kind("end")}, Found: p.Look{Token: bad, Point: span(delimiterAt, delimiterAt)},
				Skipped: []p.Token{bad, sync},
			}}}
		}
		want := "file(call-x,declare,declare,declare,local-y,local-x,call-y,local-x," + unquoted + ",quoted(quoted-text),local-x,state:x;)"
		var reference requestGraph
		var wantRecords []record
		for _, backend := range []struct {
			name string
			open func(*testing.T, *adapterInput) p.Source
		}{
			{"handmade", queuedHandmade}, {"bytescan", queuedBytescan},
		} {
			for _, hand := range []bool{false, true} {
				for _, chart := range []bool{false, true} {
					engine := configured(t, queuedGrammar(hand, chart), queuedBuilders(), p.Limits{})
					for _, schedule := range []string{"whole", "byte-stepped", "polled"} {
						t.Run(fmt.Sprintf("broken=%v/%s/hand=%v/chart=%v/%s", broken, backend.name, hand, chart, schedule), func(t *testing.T) {
							root, log := runRequestTrace(t, engine, input, "", schedule, backend.open)
							checkStateShape(t, root, want)
							{
								got := stateDiagnostics(t, root)
								require.Equalf(t, wantProblems, got, "diagnostics = %+v, want %+v", got, wantProblems)
							}
							var literal []string
							for _, item := range records(root) {
								if item.Kind == "body-text" || item.Kind == "quoted-text" {
									literal = append(literal, item.Text)
								}
							}
							require.Equalf(t, []string{"hello", "  raw${x}\n"}, literal, "body text = %q", literal)
							graph := log.graph(t, queuedPlan(broken))
							if len(reference.readings) == 0 {
								reference, wantRecords = graph, records(root)
							} else {
								checkRequestGraphs(t, reference, graph)
								equalRecords(t, root, wantRecords)
							}
							headerEnd := len(queueLead + queueHeader)
							bodyStarted := false
							for _, attempt := range log.attempts {
								bodyStarted = bodyStarted || attempt.offer.request == strictBody
								require.Falsef(t, bodyStarted && attempt.offer.mark.Byte < headerEnd, "resumption reread the consumed header at %+v", attempt.offer)
							}
							require.Truef(t, bodyStarted, "computed body was never requested")
						})
					}
				}
			}
		}
	}
}
