package shellhist

import "regexp"

const mask = "***"

// redactions mask the secrets people most often type on a command line. Each
// keeps its first group (the label) and masks what follows. This cuts the
// risk of a shared log leaking a credential; it cannot remove it.
var redactions = []struct {
	re   *regexp.Regexp
	with string
}{
	// password=x, token: x, API_KEY=x, export GITHUB_TOKEN=x
	{regexp.MustCompile(`(?i)(\b[\w-]*(?:pass(?:word|wd)?|pwd|token|secret|api[_-]?key)[\w-]*\s*[=:]\s*)("[^"]*"|'[^']*'|\S+)`), "${1}" + mask},
	// --password x, --token x
	{regexp.MustCompile(`(?i)(--?[\w-]*(?:pass(?:word|wd)?|token|secret|api-?key)[\w-]*\s+)("[^"]*"|'[^']*'|\S+)`), "${1}" + mask},
	// Authorization: Bearer x
	{regexp.MustCompile(`(?i)(authorization:\s*(?:bearer|basic|token)?\s*)\S+`), "${1}" + mask},
	// scheme://user:password@host
	{regexp.MustCompile(`(://[^/\s:@]+:)[^@\s]+@`), "${1}" + mask + "@"},
	// Well-known token shapes: OpenAI, Anthropic, GitHub, Slack, AWS.
	{regexp.MustCompile(`()\b(?:sk-[\w-]{16,}|gh[pousr]_\w{20,}|xox[abprs]-[\w-]{10,}|AKIA[0-9A-Z]{16})`), "${1}" + mask},
}

// Redact masks secrets in a command line.
func Redact(cmd string) string {
	for _, r := range redactions {
		cmd = r.re.ReplaceAllString(cmd, r.with)
	}
	return cmd
}
