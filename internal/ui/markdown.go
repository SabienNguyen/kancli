package ui

import (
	"strings"

	"github.com/charmbracelet/glamour"
	gansi "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// markdown renders task text with Glamour, matched to the theme: no
// colours in mono mode or when the output is not a terminal, otherwise a
// light or dark style depending on the terminal background.
type markdown struct {
	mono bool
	// cache holds the last render so cursor moves in the detail view do
	// not re-run the parser.
	text  string
	width int
	out   string
}

func (m *markdown) render(text string, width int) string {
	if text == m.text && width == m.width && m.out != "" {
		return m.out
	}
	m.text, m.width = text, width
	m.out = renderMarkdown(text, width, m.mono)
	return m.out
}

// renderMarkdown converts Markdown to styled terminal text of the given
// width. Plain text comes back unchanged apart from wrapping.
func renderMarkdown(text string, width int, mono bool) string {
	profile := lipgloss.ColorProfile()
	var cfg gansi.StyleConfig
	switch {
	case mono || profile == termenv.Ascii:
		cfg = styles.NoTTYStyleConfig
	case lipgloss.HasDarkBackground():
		cfg = styles.DarkStyleConfig
	default:
		cfg = styles.LightStyleConfig
	}
	// The detail view supplies its own indentation and spacing.
	zero := uint(0)
	cfg.Document.Margin = &zero
	cfg.Document.BlockPrefix = ""
	cfg.Document.BlockSuffix = ""
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(cfg),
		glamour.WithColorProfile(profile),
		glamour.WithWordWrap(max(10, width)),
		glamour.WithEmoji(),
	)
	if err != nil {
		return text
	}
	out, err := r.Render(text)
	if err != nil {
		return text
	}
	return strings.TrimRight(out, "\n")
}

// plainText strips the Markdown marks that would look odd on a one-line
// card summary: emphasis, code spans, headings and list bullets.
func plainText(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, "#> ")
	for _, p := range []string{"- [ ] ", "- [x] ", "- [X] ", "- ", "* ", "+ "} {
		s = strings.TrimPrefix(s, p)
	}
	r := strings.NewReplacer("**", "", "__", "", "`", "", "~~", "")
	return r.Replace(s)
}
