package participle

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/alecthomas/participle/lexer"
)

// A Parser for a particular grammar and lexer.
type Parser struct {
	root            node
	lex             lexer.Definition
	typ             reflect.Type
	mapper          Mapper
	useLookahead    bool
	caseInsensitive map[string]bool
}

// MustBuild calls Build(grammar, options...) and panics if an error occurs.
func MustBuild(grammar interface{}, options ...Option) *Parser {
	parser, err := Build(grammar, options...)
	if err != nil {
		panic(err)
	}
	return parser
}

// Build constructs a parser for the given grammar.
//
// If "Lexer()" is not provided as an option, a default lexer based on text/scanner will be used. This scans typical Go-
// like tokens.
//
// See documentation for details
func Build(grammar interface{}, options ...Option) (parser *Parser, err error) {
	// Configure Parser struct with defaults + options.
	p := &Parser{
		lex:             lexer.TextScannerLexer,
		mapper:          identityMapper,
		caseInsensitive: map[string]bool{},
	}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("nil Option passed, signature has changed; " +
				"if you intended to provide a custom Lexer, try participle.Build(grammar, participle.Lexer(lexer))")
		}
		if err = option(p); err != nil {
			return nil, err
		}
	}
	// If we have any mapping functions, wrap the lexer.
	if p.mapper != nil {
		p.lex = &mappingLexerDef{p.lex, p.mapper}
	}

	context := newGeneratorContext(p.lex)
	p.typ = reflect.TypeOf(grammar)
	p.root, err = context.parseType(p.typ)
	if err != nil {
		return nil, err
	}
	// TODO: Fix lookahead - see SQL example.
	if p.useLookahead {
		return p, applyLookahead(p.root, map[node]bool{})
	}
	return p, nil
}

// Lex uses the parser's lexer to tokenise input.
func (p *Parser) Lex(r io.Reader) ([]lexer.Token, error) {
	lex := p.lex.Lex(r)
	return lexer.ConsumeAll(lex)
}

// Parse from r into grammar v which must be of the same type as the grammar passed to
// participle.Build().
func (p *Parser) Parse(r io.Reader, v interface{}) (err error) {
	if reflect.TypeOf(v) != p.typ {
		return fmt.Errorf("must parse into value of type %s not %T", p.typ, v)
	}
	lex := lexer.Upgrade(p.lex.Lex(r))
	caseInsensitive := map[rune]bool{}
	for sym, rn := range p.lex.Symbols() {
		if p.caseInsensitive[sym] {
			caseInsensitive[rn] = true
		}
	}
	ctx := parseContext{PeekingLexer: lex, caseInsensitive: caseInsensitive}
	// If the grammar implements Parseable, use it.
	if parseable, ok := v.(Parseable); ok {
		return p.rootParseable(lex, parseable)
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.Elem().Kind() != reflect.Struct {
		return errors.New("target must be a pointer to a struct")
	}
	pv, err := p.root.Parse(ctx, rv.Elem())
	if err != nil {
		return err
	}
	if len(pv) > 0 {
		rv.Elem().Set(reflect.Indirect(pv[0]))
	}
	token, err := lex.Peek(0)
	if err != nil {
		return err
	} else if !token.EOF() {
		return lexer.Errorf(token.Pos, "expected %s but got %q", p.root, token)
	}
	if pv == nil {
		return lexer.Errorf(token.Pos, "invalid syntax")
	}
	return nil
}

func (p *Parser) rootParseable(lex lexer.PeekingLexer, parseable Parseable) error {
	peek, err := lex.Peek(0)
	if err != nil {
		return err
	}
	err = parseable.Parse(lex)
	if err == NextMatch {
		return lexer.Errorf(peek.Pos, "invalid syntax")
	}
	if err == nil && !peek.EOF() {
		return lexer.Errorf(peek.Pos, "unexpected token %q", peek)
	}
	return err
}

// ParseString is a convenience around Parse().
func (p *Parser) ParseString(s string, v interface{}) error {
	return p.Parse(strings.NewReader(s), v)
}

// ParseBytes is a convenience around Parse().
func (p *Parser) ParseBytes(b []byte, v interface{}) error {
	return p.Parse(bytes.NewReader(b), v)
}

// String representation of the grammar.
func (p *Parser) String() string {
	return dumpNode(p.root)
}
