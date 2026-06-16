package plane

import (
	"bytes"
	"strings"
	"sync"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/strikethrough"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
	xhtml "golang.org/x/net/html"
)

// sanitizePolicy returns the shared bluemonday policy: UGC defaults plus
// task-list checkbox inputs (goldmark's GFM renderer emits
// <input type="checkbox"> for "- [ ]" items, which must survive
// sanitization to round-trip).
var sanitizePolicy = sync.OnceValue(func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("type", "checked", "disabled").OnElements("input")
	return p
})

// mdConverter returns the shared HTML->Markdown converter: commonmark plus
// the GFM pieces Plane descriptions need (tables, strikethrough, task-list
// checkboxes). The converter is safe for concurrent use.
var mdConverter = sync.OnceValue(func() *converter.Converter {
	conv := converter.NewConverter(converter.WithPlugins(
		base.NewBasePlugin(),
		commonmark.NewCommonmarkPlugin(),
		table.NewTablePlugin(),
		strikethrough.NewStrikethroughPlugin(),
	))
	// The base plugin removes <input> entirely; override it to render
	// task-list checkboxes back to their markdown form.
	conv.Register.TagType("input", converter.TagTypeInline, converter.PriorityStandard-10)
	conv.Register.RendererFor("input", converter.TagTypeInline, renderCheckboxInput, converter.PriorityStandard-10)
	return conv
})

// renderCheckboxInput converts <input type="checkbox"> nodes to "[ ]"/"[x]"
// task-list markers; any other input falls through to the next renderer.
func renderCheckboxInput(_ converter.Context, w converter.Writer, n *xhtml.Node) converter.RenderStatus {
	inputType, checked := "", false
	for _, a := range n.Attr {
		switch a.Key {
		case "type":
			inputType = a.Val
		case "checked":
			checked = true
		}
	}
	if inputType != "checkbox" {
		return converter.RenderTryNext
	}
	if checked {
		_, _ = w.WriteString("[x]")
	} else {
		_, _ = w.WriteString("[ ]")
	}
	return converter.RenderSuccess
}

// markdownRenderer is the shared Markdown->HTML renderer: GFM (tables,
// strikethrough, task lists, autolinks) with XHTML output and no raw HTML
// passthrough. goldmark.Markdown is safe for concurrent use.
var markdownRenderer = sync.OnceValue(func() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(
			html.WithXHTML(),
		),
	)
})

// HTMLToMarkdown converts a Plane description_html value to Markdown for
// beads storage. It sanitizes the HTML first via bluemonday to strip
// dangerous elements (script tags, event handlers), then converts the clean
// HTML to Markdown. Returns empty string for empty input.
func HTMLToMarkdown(rawHTML string) (string, error) {
	if strings.TrimSpace(rawHTML) == "" {
		return "", nil
	}

	sanitized := sanitizePolicy().Sanitize(rawHTML)

	if strings.TrimSpace(sanitized) == "" {
		return "", nil
	}

	md, err := mdConverter().ConvertString(sanitized)
	if err != nil {
		return "", err
	}

	return strings.TrimRight(md, " \t\r\n"), nil
}

// descriptionMarkdown converts description HTML to Markdown, falling back
// to the sanitized HTML when conversion fails (e.g. pathologically nested
// markup exceeding the parser's depth limit) so the content is preserved
// rather than silently blanked.
func descriptionMarkdown(rawHTML string) string {
	md, err := HTMLToMarkdown(rawHTML)
	if err == nil {
		return md
	}
	return strings.TrimSpace(sanitizePolicy().Sanitize(rawHTML))
}

// MarkdownToHTML converts a beads Markdown description to HTML for Plane's
// description_html field. Uses goldmark with GFM extensions and safe
// renderer settings (no raw HTML passthrough). Returns empty string for
// empty input.
func MarkdownToHTML(md string) (string, error) {
	if strings.TrimSpace(md) == "" {
		return "", nil
	}

	var buf bytes.Buffer
	if err := markdownRenderer().Convert([]byte(md), &buf); err != nil {
		return "", err
	}

	return strings.TrimRight(buf.String(), " \t\r\n"), nil
}
