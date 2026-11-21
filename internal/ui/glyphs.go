package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// NeedsBasicGlyphs reports whether the table should avoid symbols that the
// legacy Windows console cannot draw.
//
// The legacy console (conhost, still the default on Windows 10) does no font
// fallback, and its default fonts, Consolas and Lucida Console, have no check
// marks, most of the sparkline bar heights, or rounded box corners, so it
// draws them as empty boxes. Windows Terminal, VS Code, ConEmu and mintty all
// draw them, and each sets one of the variables checked below.
//
// PINGM_GLYPHS=basic or PINGM_GLYPHS=full overrides the detection.
func NeedsBasicGlyphs(goos string, getenv func(string) string) bool {
	switch strings.ToLower(getenv("PINGM_GLYPHS")) {
	case "basic":
		return true
	case "full":
		return false
	}
	if goos != "windows" {
		return false
	}
	for _, name := range []string{"WT_SESSION", "TERM_PROGRAM", "TERM", "ConEmuANSI"} {
		if getenv(name) != "" {
			return false
		}
	}
	return true
}

// UseBasicGlyphs swaps in symbols that Consolas and Lucida Console have. The
// rest of the table's symbols (arrows, triangles, dashes, the half and full
// blocks) are already in those fonts and stay as they are.
//
// It changes package state, so call it once, before the table is first drawn.
func UseBasicGlyphs() {
	glyphUp = "+"
	glyphDown = "x"
	sparkRamp = []rune{'_', '▄', '█'}
	styleHelpBox = styleHelpBox.Border(lipgloss.NormalBorder())
}
