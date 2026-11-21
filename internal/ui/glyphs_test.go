package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/maxajbarlow/pingm/internal/monitor"
)

func TestNeedsBasicGlyphs(t *testing.T) {
	tests := []struct {
		name string
		goos string
		env  map[string]string
		want bool
	}{
		{"legacy Windows console", "windows", nil, true},
		{"Windows Terminal", "windows", map[string]string{"WT_SESSION": "abc"}, false},
		{"VS Code on Windows", "windows", map[string]string{"TERM_PROGRAM": "vscode"}, false},
		{"Git Bash mintty", "windows", map[string]string{"TERM": "xterm-256color"}, false},
		{"ConEmu", "windows", map[string]string{"ConEmuANSI": "ON"}, false},
		{"macOS", "darwin", nil, false},
		{"Linux", "linux", nil, false},
		{"forced basic", "darwin", map[string]string{"PINGM_GLYPHS": "basic"}, true},
		{"forced full on legacy console", "windows", map[string]string{"PINGM_GLYPHS": "FULL"}, false},
		{"unknown override is ignored", "windows", map[string]string{"PINGM_GLYPHS": "fancy"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(k string) string { return tt.env[k] }
			if got := NeedsBasicGlyphs(tt.goos, getenv); got != tt.want {
				t.Errorf("NeedsBasicGlyphs = %v, want %v", got, tt.want)
			}
		})
	}
}

// useBasicGlyphsForTest switches to the basic set and puts the full set back
// when the test ends, so other tests see the defaults.
func useBasicGlyphsForTest(t *testing.T) {
	t.Helper()
	up, down, ramp, box := glyphUp, glyphDown, sparkRamp, styleHelpBox
	t.Cleanup(func() { glyphUp, glyphDown, sparkRamp, styleHelpBox = up, down, ramp, box })
	UseBasicGlyphs()
}

func TestBasicGlyphsAvoidSymbolsTheLegacyConsoleLacks(t *testing.T) {
	useBasicGlyphsForTest(t)

	// None of these are in Consolas or Lucida Console.
	missing := "✔✘▁▂▃▅▆▇╭╮╰╯"
	for _, r := range glyphUp + glyphDown + string(sparkRamp) {
		if strings.ContainsRune(missing, r) {
			t.Errorf("basic set still uses %q", r)
		}
	}
	if got := styleHelpBox.GetBorderStyle(); got != lipgloss.NormalBorder() {
		t.Errorf("help box border = %+v, want the square NormalBorder", got)
	}
}

func TestBasicSparklineStillShowsHighAndLow(t *testing.T) {
	useBasicGlyphsForTest(t)

	s := []monitor.Sample{
		{OK: true, RTT: ms(1)},
		{OK: false},
		{OK: true, RTT: ms(50)},
	}
	got := []rune(plain(Sparkline(s, len(s))))
	want := []rune{sparkRamp[0], []rune(glyphLost)[0], sparkRamp[len(sparkRamp)-1]}
	if string(got) != string(want) {
		t.Errorf("sparkline = %q, want %q", string(got), string(want))
	}
}
