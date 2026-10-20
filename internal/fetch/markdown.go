package fetch

import (
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/strikethrough"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
)

// HTMLToMarkdown converts an HTML page to Markdown.
// Port of the Turndown setup in buildFetchCode, S:3098-3101:
// {headingStyle:'atx', codeBlockStyle:'fenced'} + gfm (tables, strikethrough),
// with script, style, nav, header, footer, noscript removed.
func HTMLToMarkdown(html string) (string, error) {
	// Built per call: cheap, and no shared mutable converter state.
	conv := converter.NewConverter(converter.WithPlugins(
		base.NewBasePlugin(), // already removes script, style, noscript, head, …
		commonmark.NewCommonmarkPlugin(
			commonmark.WithHeadingStyle(commonmark.HeadingStyleATX),
			commonmark.WithCodeBlockFence("```"),
			commonmark.WithEmDelimiter("_"),      // Turndown default
			commonmark.WithBulletListMarker("*"), // Turndown default
			commonmark.WithListEndComment(false), // no "<!--THE END-->" noise in indexed text
		),
		strikethrough.NewStrikethroughPlugin(),
		// Minimal padding like Turndown gfm; aligned padding bloats chunks when one cell is long.
		table.NewTablePlugin(table.WithCellPaddingBehavior(table.CellPaddingBehaviorMinimal)),
	))
	for _, tag := range []string{"nav", "header", "footer"} {
		conv.Register.TagType(tag, converter.TagTypeRemove, converter.PriorityStandard)
	}
	return conv.ConvertString(html)
}
