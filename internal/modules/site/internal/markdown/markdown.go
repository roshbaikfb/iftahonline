// Package markdown renders the site's guides (S4c): CommonMark with
// tables and strikethrough, raw HTML left out and dangerous links
// dropped (goldmark's safe mode), so what the console writes cannot run
// on the site. A guide's "#" headings become <h2>: its page has the h1.
package markdown

import (
	"bytes"
	"fmt"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Renderer turns a guide's Markdown into safe HTML.
type Renderer struct{ md goldmark.Markdown }

// New builds the renderer. It is safe for concurrent use.
func New() *Renderer {
	return &Renderer{md: goldmark.New(
		goldmark.WithExtensions(extension.Table, extension.Strikethrough),
		goldmark.WithParserOptions(parser.WithAutoHeadingID(),
			parser.WithASTTransformers(util.Prioritized(demote{}, 100), util.Prioritized(codeLang{}, 100))),
		// No html.WithUnsafe: raw HTML is left out and javascript:,
		// vbscript:, file: and data: links are dropped.
	)}
}

// HTML is src rendered.
func (r *Renderer) HTML(src string) (string, error) {
	var b bytes.Buffer
	if err := r.md.Convert([]byte(src), &b); err != nil {
		return "", fmt.Errorf("markdown: %w", err)
	}
	return b.String(), nil
}

// demote makes every heading one level lower, down to h6, so a guide's
// "#" is the page's second level.
type demote struct{}

func (demote) Transform(doc *ast.Document, _ text.Reader, _ parser.Context) {
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) { //nolint:errcheck // the walker returns none
		if h, ok := n.(*ast.Heading); ok && entering {
			h.Level = min(h.Level+1, 6)
		}
		return ast.WalkContinue, nil
	})
}

// codeLang drops a fenced code block's language unless it is a plain
// name (letters, digits and + - _ . #). goldmark writes the language
// into a class attribute up to the first space only, so a tab would
// carry the rest of the line in with it.
type codeLang struct{}

func (codeLang) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	src := reader.Source()
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) { //nolint:errcheck // the walker returns none
		// The info's first word, read here and not through Language(),
		// which keeps what it read the first time.
		if c, ok := n.(*ast.FencedCodeBlock); ok && entering && c.Info != nil {
			word, _, _ := bytes.Cut(bytes.TrimSpace(c.Info.Segment.Value(src)), []byte(" "))
			if !plainName(word) {
				c.Info = nil
			}
		}
		return ast.WalkContinue, nil
	})
}

func plainName(b []byte) bool {
	for _, c := range b {
		plain := 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || bytes.IndexByte([]byte("+-_.#"), c) >= 0
		if !plain {
			return false
		}
	}
	return true
}
