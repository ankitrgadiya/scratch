package markdown

import (
	"bytes"
	"html/template"

	chromahtml "github.com/alecthomas/chroma/formatters/html"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark-emoji"
	"github.com/yuin/goldmark-highlighting"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

func NewParser() *Parser {
	return &Parser{
		md: goldmark.New(
			goldmark.WithExtensions(
				extension.GFM,
				extension.Footnote,
				emoji.Emoji,
				highlighting.NewHighlighting(
					highlighting.WithStyle("friendly"),
					highlighting.WithFormatOptions(chromahtml.WithLineNumbers(true)),
				),
				WikiLinkExtension(),
			),
			goldmark.WithParserOptions(parser.WithAutoHeadingID()),
			goldmark.WithRendererOptions(html.WithHardWraps()),
		),
	}
}

type Parser struct {
	md goldmark.Markdown
}

func (p *Parser) Convert(data string) (template.HTML, error) {
	var buf bytes.Buffer

	if err := p.md.Convert([]byte(data), &buf); err != nil {
		return "", nil
	}

	return template.HTML(buf.String()), nil
}
