// Package markdown renders user Markdown to sanitized HTML and extracts #tags.
package markdown

import (
	"bytes"
	"fmt"
	"html/template"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

var (
	md = goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(html.WithHardWraps()),
	)
	policy = newPolicy()

	checkboxRe = regexp.MustCompile(`<input[^>]*type="checkbox"[^>]*>`)
	taskLineRe = regexp.MustCompile(`^((?:\s*>)*\s*(?:[-*+]|\d+[.)])\s+\[)([ xX])(\])`)
	fenceRe    = regexp.MustCompile(`^[ \t]*(` + "```" + `|~~~)`)
	tagRe      = regexp.MustCompile(`(?:^|[^\p{L}\p{M}\p{N}_&/#\]])#([\p{L}\p{N}_][\p{L}\p{M}\p{N}_\-/]*)`)
	inlineCode = regexp.MustCompile("`[^`\n]*`")
	tagNameRe  = regexp.MustCompile(`^[\p{L}\p{N}_][\p{L}\p{M}\p{N}_\-/]*$`)
	digitsRe   = regexp.MustCompile(`^[0-9]+$`)
)

func newPolicy() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowElements("input")
	p.AllowAttrs("type").Matching(regexp.MustCompile(`^checkbox$`)).OnElements("input")
	p.AllowAttrs("checked", "disabled").OnElements("input")
	p.AddTargetBlankToFullyQualifiedLinks(true)
	return p
}

// Render converts Markdown to sanitized HTML. When interactive is true, task
// list checkboxes are enabled and numbered with data-cb so the UI can toggle them.
func Render(src string, interactive bool) template.HTML {
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		return template.HTML(template.HTMLEscapeString(src)) // #nosec -- escaped
	}
	out := policy.SanitizeBytes(buf.Bytes())
	n := 0
	out = checkboxRe.ReplaceAllFunc(out, func(m []byte) []byte {
		checked := bytes.Contains(m, []byte("checked"))
		var b strings.Builder
		b.WriteString(`<input type="checkbox"`)
		if interactive {
			fmt.Fprintf(&b, ` data-cb="%d"`, n)
		} else {
			b.WriteString(` disabled`)
		}
		if checked {
			b.WriteString(` checked`)
		}
		b.WriteString(`>`)
		n++
		return []byte(b.String())
	})
	return template.HTML(out) // #nosec -- sanitized by bluemonday
}

// Sanitize returns sanitized HTML for already-HTML input (used in tests and snippets).
func Sanitize(s string) string { return policy.Sanitize(s) }

// ToggleCheckbox flips the idx-th task-list checkbox in src (0-based, in document order,
// ignoring fenced code blocks).
func ToggleCheckbox(src string, idx int) (string, error) {
	lines := strings.Split(src, "\n")
	inFence := false
	n := 0
	for i, l := range lines {
		if fenceRe.MatchString(l) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		m := taskLineRe.FindStringSubmatchIndex(l)
		if m == nil {
			continue
		}
		if n == idx {
			mark := l[m[4]:m[5]]
			repl := "x"
			if mark != " " {
				repl = " "
			}
			lines[i] = l[:m[4]] + repl + l[m[5]:]
			return strings.Join(lines, "\n"), nil
		}
		n++
	}
	return src, fmt.Errorf("checkbox %d not found", idx)
}

// ParseTags extracts #tags from Markdown text, lower-cased and de-duplicated,
// ignoring code blocks, inline code, headings ("# Title") and numeric refs ("#12").
func ParseTags(src string) []string {
	var tags []string
	seen := map[string]bool{}
	inFence := false
	for _, l := range strings.Split(src, "\n") {
		if fenceRe.MatchString(l) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		l = inlineCode.ReplaceAllString(l, " ")
		for _, m := range tagRe.FindAllStringSubmatch(l, -1) {
			t := strings.ToLower(strings.TrimRight(m[1], "-/"))
			if t == "" || digitsRe.MatchString(t) || len(t) > 50 || seen[t] {
				continue
			}
			seen[t] = true
			tags = append(tags, t)
		}
	}
	return tags
}

// NormalizeTags cleans user-entered tags ("#Work, home" -> ["work","home"]).
// Invalid tags are dropped.
func NormalizeTags(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range in {
		for _, t := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' }) {
			t = strings.ToLower(strings.TrimLeft(strings.TrimSpace(t), "#"))
			if t == "" || len(t) > 50 || !tagNameRe.MatchString(t) || seen[t] {
				continue
			}
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}
