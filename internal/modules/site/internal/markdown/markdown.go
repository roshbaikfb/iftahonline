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
		goldmark.WithParserOptions(parser.WithAutoHeadingID(), parser.WithASTTransformers(util.Prioritized(demote{}, 100))),
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
