// Package parser recognizes token streams with caller-defined grammar steps
// and builds trees with caller-defined factories.
package parser

import (
	"fmt"

	"github.com/xiam/leafkit/tree"
)

// TokenKind identifies a token role supplied by a Source.
type TokenKind string

// Invalid is a token the source could locate but could not recognize.
const Invalid TokenKind = "leafkit:invalid"

// Trivia records a skipped item at its original source span.
type Trivia struct {
	Kind TokenKind
	Span tree.Span
}

// Token is a stable record. Text and Leading remain immutable after Read,
// even across later reads, discards and source feeds.
type Token struct {
	Kind                   TokenKind
	Text                   string
	Span                   tree.Span
	Leading                []Trivia
	BreakBefore, Synthetic bool
}

// Layout selects a source-defined layout policy.
type Layout uint8

const (
	TriviaOnly Layout = iota
	Lines
	Indentation
	Joined
)

// Request tells a source how to recognize the next token. The source defines
// the vocabulary of goals and arguments.
type Request struct {
	Goal, Argument string
	Layout         Layout
}

// Mark identifies a position and its lexer state within one parse. Byte and
// Virtual order progress. Key distinguishes lexer states at the same position.
type Mark struct {
	Byte, Virtual int
	Key           string
}

type ReadStatus uint8

const (
	TokenReady ReadStatus = iota
	EndOfInput
	NeedInput
)

// Read carries a stable token and its successor, a final EOF with its actual
// end and trailing trivia, or a temporary shortage with no committed movement.
type Read struct {
	Status   ReadStatus
	Token    Token
	Next     Mark
	Trailing []Trivia
}

// Source supplies replayable token reads for one session. A successful result
// for the same mark and request must remain stable across input feeds. If
// additional bytes could change the result, return NeedInput. Begin names
// the immutable logical input. Feeding exposes more bytes of that input.
// DiscardBefore retires reads at earlier byte marks, never returned token data.
type Source interface {
	Begin() (tree.SourceID, Mark)
	Read(Mark, Request) (Read, error)
	DiscardBefore(byteOffset int)
}

// Look is the offered token or final EOF. Point is the zero-width position
// at its start, or at the final EOF byte.
type Look struct {
	Token Token
	End   bool
	Point tree.Span
}

// Pattern tests an offered event without advancing the source.
type Pattern struct {
	kind     TokenKind
	word     string
	text     bool
	end      bool
	sameLine bool
}

func Kind(kind TokenKind) Pattern { return Pattern{kind: kind} }
func Word(word string) Pattern    { return Pattern{text: true, word: word} }
func EndOfFile() Pattern          { return Pattern{end: true} }
func OnSameLine(pattern Pattern) Pattern {
	pattern.sameLine = true
	return pattern
}

func Matches(look Look, pattern Pattern) bool {
	if pattern.sameLine && (look.End || look.Token.BreakBefore) {
		return false
	}
	switch {
	case pattern.end:
		return look.End
	case look.End:
		return false
	case pattern.text:
		return look.Token.Text == pattern.word
	default:
		return look.Token.Kind == pattern.kind
	}
}

func point(source tree.SourceID, offset int) tree.Span {
	return tree.Span{Source: source, Start: offset, End: offset}
}

func checkRead(read Read, mark Mark, source tree.SourceID) error {
	switch read.Status {
	case NeedInput:
		return nil
	case EndOfInput:
		if read.Next.Byte < mark.Byte {
			return fmt.Errorf("EOF at byte %d precedes mark at %d", read.Next.Byte, mark.Byte)
		}
		return checkTrivia(read.Trailing, source, mark.Byte, read.Next.Byte)
	case TokenReady:
		next := read.Next
		if next.Byte < mark.Byte ||
			next.Byte == mark.Byte && next.Virtual <= mark.Virtual ||
			next.Byte > mark.Byte && next.Virtual != 0 {
			return fmt.Errorf("token at mark %+v has nonprogressing successor %+v", mark, next)
		}
		token := read.Token
		if token.Kind == "" || !inRange(token.Span, source, mark.Byte, next.Byte) {
			return fmt.Errorf("invalid token kind or span at mark %+v: %q %+v", mark, token.Kind, token.Span)
		}
		return checkTrivia(token.Leading, source, mark.Byte, next.Byte)
	default:
		return fmt.Errorf("unknown read status %d", read.Status)
	}
}

func checkTrivia(items []Trivia, source tree.SourceID, start, end int) error {
	for _, item := range items {
		if item.Kind == "" || !inRange(item.Span, source, start, end) {
			return fmt.Errorf("invalid trivia kind or span: %q %+v", item.Kind, item.Span)
		}
	}
	return nil
}

func inRange(span tree.Span, source tree.SourceID, start, end int) bool {
	return span.Source == source && span.Start >= start && span.End >= span.Start && span.End <= end
}
