// Inlined from github.com/yuin/goldmark-meta v1.1.0 (MIT).
// Only the minimal block parser, Get, and Extender are kept;
// Table rendering, MapSlice, TryGet, options, and StoresInDocument
// have been removed.

package parser

import (
	"bytes"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	"gopkg.in/yaml.v3"
)

// contextKey stores the parsed YAML map in parser.Context.
var metaContextKey = parser.NewContextKey()

type metaData struct {
	Map   map[string]interface{}
	Error error
}

// MetaGet returns the YAML frontmatter map stored in a parser.Context.
func MetaGet(pc parser.Context) map[string]interface{} {
	v := pc.Get(metaContextKey)
	if v == nil {
		return nil
	}
	return v.(*metaData).Map
}

// ---------------------------------------------------------------------------
// Block parser
// ---------------------------------------------------------------------------

type metaParser struct{}

var defaultMetaParser = &metaParser{}

func isSeparator(line []byte) bool {
	line = util.TrimRightSpace(util.TrimLeftSpace(line))
	for i := 0; i < len(line); i++ {
		if line[i] != '-' {
			return false
		}
	}
	return true
}

func (b *metaParser) Trigger() []byte { return []byte{'-'} }

func (b *metaParser) Open(parent gast.Node, reader text.Reader, pc parser.Context) (gast.Node, parser.State) {
	linenum, _ := reader.Position()
	if linenum != 0 {
		return nil, parser.NoChildren
	}
	line, _ := reader.PeekLine()
	if isSeparator(line) {
		return gast.NewTextBlock(), parser.NoChildren
	}
	return nil, parser.NoChildren
}

func (b *metaParser) Continue(node gast.Node, reader text.Reader, pc parser.Context) parser.State {
	line, segment := reader.PeekLine()
	if isSeparator(line) && !util.IsBlank(line) {
		reader.Advance(segment.Len())
		return parser.Close
	}
	node.Lines().Append(segment)
	return parser.Continue | parser.NoChildren
}

const maxFrontmatterBytes = 8192

func (b *metaParser) Close(node gast.Node, reader text.Reader, pc parser.Context) {
	lines := node.Lines()
	var buf bytes.Buffer
	for i := 0; i < lines.Len(); i++ {
		segment := lines.At(i)
		buf.Write(segment.Value(reader.Source()))
		if buf.Len() > maxFrontmatterBytes {
			break
		}
	}
	d := &metaData{}
	raw := buf.Bytes()
	if len(raw) > maxFrontmatterBytes {
		d.Error = bytes.ErrTooLarge
	} else {
		m := map[string]interface{}{}
		if err := yaml.Unmarshal(raw, &m); err != nil {
			d.Error = err
		} else {
			d.Map = m
		}
	}
	pc.Set(metaContextKey, d)
	if d.Error == nil {
		node.Parent().RemoveChild(node.Parent(), node)
	}
}

func (b *metaParser) CanInterruptParagraph() bool { return false }
func (b *metaParser) CanAcceptIndentedLine() bool  { return false }

// ---------------------------------------------------------------------------
// AST transformer (no-op — frontmatter node is already removed in Close)
// ---------------------------------------------------------------------------

type metaTransformer struct{}

func (a *metaTransformer) Transform(_ *gast.Document, _ text.Reader, _ parser.Context) {}

// ---------------------------------------------------------------------------
// Extender
// ---------------------------------------------------------------------------

type metaExtension struct{}

// MetaExt is a goldmark.Extender that parses YAML frontmatter.
var MetaExt goldmark.Extender = &metaExtension{}

func (e *metaExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(
		parser.WithBlockParsers(
			util.Prioritized(defaultMetaParser, 0),
		),
		parser.WithASTTransformers(
			util.Prioritized(&metaTransformer{}, 0),
		),
	)
}
