// Package bytescan connects bytescan token sources to the leafkit parser.
package bytescan

import (
	"errors"
	"fmt"
	"io"
	"strconv"

	scan "github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tree"
)

// Options describes the token kinds used for trivia and physical line breaks.
// Trivia kinds are skipped and attached by span. Newlines identifies kinds
// used for physical line terminators. They carry the break forward to the
// next significant token. A kind in both lists is skipped as trivia.
type Options struct {
	Trivia   []scan.TokenType
	Newlines []scan.TokenType
}

// Factory opens a fresh lexer at the current mark, using its immutable state
// and the complete parser request. It must reject requests it cannot honor.
// The lexer uses the supplied cursor, whose offsets are relative to the mark.
// The adapter can reset this cursor for a later read. The optional save
// function returns independent, immutable state at the next logical read,
// including any pending virtual tokens. A nil save function keeps state
// unchanged. A factory that needs more bytes can return scan.ErrMoreInput.
// Factories and save functions must be safe to replay.
type Factory[S any] func(cur *cursor.Reader, state S, request parser.Request) (
	lexer scan.Source, save func() S, err error,
)

// Source serves one parse. The caller serializes its reads and input feeds.
// Marks and returned data are immutable. DiscardBefore retires cached reads
// and state independently of the caller's snapshot storage.
type Source[S any] struct {
	id       tree.SourceID
	begin    parser.Mark
	floor    int
	nextID   uint64
	marks    map[int]map[parser.Mark]*checkpoint[S]
	trivia   map[scan.TokenType]bool
	newlines map[scan.TokenType]bool
	fixed    *fixedStream
	open     func(offset int) io.Reader
	factory  Factory[S]
	cursor   *cursor.Reader
}

type checkpoint[S any] struct {
	state       S
	lineStart   bool
	breakBefore bool
	reads       map[parser.Request]parser.Read
}

type fixedStream struct {
	request parser.Request
	head    parser.Mark
	input   stream
	err     error
}

// New wraps an existing lexer and the cursor it reads. The cursor must start at
// byte zero of id. The adapter owns the lexer's Next calls for the parse. It
// caches the one stream, and accepts only the supplied request. The caller
// configures the lexer or its filters to honor that request.
func New(id tree.SourceID, lexer scan.Source, cur scan.Cursor, request parser.Request, options Options) (*Source[struct{}], error) {
	if lexer == nil || cur == nil || cur.Cursor().ByteOffset() != 0 {
		return nil, fmt.Errorf("bytescan adapter: expected a lexer and its cursor at byte zero")
	}
	if err := checkLayout(request); err != nil {
		return nil, err
	}
	src, err := newSource(id, struct{}{}, options)
	if err != nil {
		return nil, err
	}
	src.fixed = &fixedStream{
		request: request,
		head:    src.begin,
		input:   stream{lexer: lexer, cur: cur, lineStart: true},
	}
	return src, nil
}

// NewReplay opens independent lexers for uncached reads. open must provide a
// fresh reader from offset to the available end of id. The reader returns
// bytescan.ErrMoreInput at an open end and io.EOF only at the final end.
// Previously exposed bytes must stay fixed. A nil reader means that offset
// is unavailable. The caller retains bytes for all live marks and may keep the
// complete sealed snapshot separately for tree.SourceLookup.
func NewReplay[S any](id tree.SourceID, open func(offset int) io.Reader, initial S, build Factory[S], options Options) (*Source[S], error) {
	if open == nil || build == nil {
		return nil, fmt.Errorf("bytescan adapter: expected an input opener and a lexer factory")
	}
	src, err := newSource(id, initial, options)
	if err != nil {
		return nil, err
	}
	src.open, src.factory = open, build
	src.cursor = cursor.NewReader(nil)
	return src, nil
}

func newSource[S any](id tree.SourceID, initial S, options Options) (*Source[S], error) {
	if id == "" {
		return nil, fmt.Errorf("bytescan adapter: empty source ID")
	}
	src := &Source[S]{
		id:       id,
		marks:    make(map[int]map[parser.Mark]*checkpoint[S]),
		trivia:   make(map[scan.TokenType]bool),
		newlines: make(map[scan.TokenType]bool),
	}
	for _, kind := range options.Trivia {
		src.trivia[kind] = true
	}
	for _, kind := range options.Newlines {
		src.newlines[kind] = true
	}
	src.begin = src.remember(0, 0, checkpoint[S]{state: initial, lineStart: true})
	return src, nil
}

func (s *Source[S]) remember(at, virtual int, state checkpoint[S]) parser.Mark {
	s.nextID++
	mark := parser.Mark{Byte: at, Virtual: virtual, Key: strconv.FormatUint(s.nextID, 10)}
	if s.marks[at] == nil {
		s.marks[at] = make(map[parser.Mark]*checkpoint[S])
	}
	s.marks[at][mark] = &state
	return mark
}

func (s *Source[S]) Begin() (tree.SourceID, parser.Mark) {
	return s.id, s.begin
}

func (s *Source[S]) Read(mark parser.Mark, request parser.Request) (parser.Read, error) {
	if err := checkLayout(request); err != nil {
		return parser.Read{}, err
	}
	cp := s.marks[mark.Byte][mark]
	if cp == nil {
		return parser.Read{}, fmt.Errorf("bytescan adapter: unknown or retired mark %+v", mark)
	}
	if read, ok := cp.reads[request]; ok {
		return read, nil
	}

	var read parser.Read
	var err error
	if s.fixed != nil {
		read, err = s.readFixed(mark, request)
	} else {
		read, err = s.readReplay(mark, request, cp)
	}
	if err == nil && read.Status != parser.NeedInput {
		if cp.reads == nil {
			cp.reads = make(map[parser.Request]parser.Read)
		}
		cp.reads[request] = read
	}
	return read, err
}

func checkLayout(request parser.Request) error {
	if request.Layout > parser.Joined {
		return fmt.Errorf("bytescan adapter: unknown layout %d", request.Layout)
	}
	return nil
}

func (s *Source[S]) readFixed(mark parser.Mark, request parser.Request) (parser.Read, error) {
	fixed := s.fixed
	if request != fixed.request {
		return parser.Read{}, fmt.Errorf("bytescan adapter: fixed stream does not support request %+v", request)
	}
	if fixed.err != nil {
		return parser.Read{}, fixed.err
	}
	if mark != fixed.head {
		return parser.Read{}, fmt.Errorf("bytescan adapter: fixed stream cannot advance mark %+v", mark)
	}

	read, err := consume(&fixed.input, s.id, s.trivia, s.newlines)
	switch {
	case err != nil:
		fixed.err = err
	case read.Status == parser.TokenReady:
		var state S
		read.Next = s.successor(mark, &fixed.input, state)
		fixed.head = read.Next
		fixed.input.leading = nil
	}
	return read, err
}

func (s *Source[S]) readReplay(mark parser.Mark, request parser.Request, cp *checkpoint[S]) (parser.Read, error) {
	input := s.open(mark.Byte)
	if input == nil {
		return parser.Read{}, fmt.Errorf("bytescan adapter: input unavailable at byte %d", mark.Byte)
	}
	cur := s.cursor
	if err := cur.ResetStart(input, cursor.Start{InText: mark.Byte != 0, LineStart: cp.lineStart}); err != nil {
		return parser.Read{}, err
	}
	lexer, save, err := s.factory(cur, cp.state, request)
	if errors.Is(err, scan.ErrMoreInput) {
		return parser.Read{Status: parser.NeedInput}, nil
	}
	if err != nil {
		return parser.Read{}, err
	}
	if lexer == nil {
		return parser.Read{}, fmt.Errorf("bytescan adapter: factory returned a nil lexer")
	}
	raw := stream{
		lexer: lexer, cur: cur, base: mark.Byte, offset: mark.Byte,
		lineStart: cp.lineStart, breakBefore: cp.breakBefore,
	}
	read, err := consume(&raw, s.id, s.trivia, s.newlines)
	if err == nil && read.Status == parser.TokenReady {
		state := cp.state
		if save != nil {
			state = save()
		}
		read.Next = s.successor(mark, &raw, state)
	}
	return read, err
}

func (s *Source[S]) successor(mark parser.Mark, raw *stream, state S) parser.Mark {
	virtual := 0
	if raw.offset == mark.Byte {
		virtual = mark.Virtual + 1
	}
	return s.remember(raw.offset, virtual, checkpoint[S]{
		state: state, lineStart: raw.lineStart, breakBefore: raw.breakBefore,
	})
}

func (s *Source[S]) DiscardBefore(byteOffset int) {
	if byteOffset <= s.floor {
		return
	}
	if byteOffset-s.floor < len(s.marks) {
		for offset := s.floor; offset < byteOffset; offset++ {
			delete(s.marks, offset)
		}
	} else {
		for offset := range s.marks {
			if offset < byteOffset {
				delete(s.marks, offset)
			}
		}
	}
	s.floor = byteOffset
}
