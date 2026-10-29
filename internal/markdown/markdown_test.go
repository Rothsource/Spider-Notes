package markdown

import (
	"reflect"
	"strings"
	"testing"
)

func TestRenderSanitizes(t *testing.T) {
	out := string(Render("hello <script>alert(1)</script> [x](javascript:alert(1)) <img src=x onerror=alert(1)>\n\n| a | b |\n|---|---|\n| 1 | 2 |", false))
	for _, bad := range []string{"<script", "javascript:", "onerror"} {
		if strings.Contains(out, bad) {
			t.Fatalf("unsanitized %q in %s", bad, out)
		}
	}
	if !strings.Contains(out, "<table>") {
		t.Fatalf("GFM table not rendered: %s", out)
	}
	if !strings.Contains(string(Render("see https://example.com", false)), `href="https://example.com"`) {
		t.Fatal("autolink missing")
	}
}

func TestRenderCheckboxes(t *testing.T) {
	out := string(Render("- [ ] one\n- [x] two", true))
	if !strings.Contains(out, `data-cb="0"`) || !strings.Contains(out, `data-cb="1" checked`) {
		t.Fatalf("checkboxes: %s", out)
	}
	if strings.Contains(out, "disabled") {
		t.Fatal("interactive checkboxes should not be disabled")
	}
}

func TestToggleCheckbox(t *testing.T) {
	src := "intro\n```\n- [ ] code\n```\n- [ ] a\n  - [x] b\n> - [ ] quoted\n1. [ ] numbered"
	got, err := ToggleCheckbox(src, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "  - [ ] b") {
		t.Fatalf("toggle b: %s", got)
	}
	got, _ = ToggleCheckbox(src, 0)
	if !strings.Contains(got, "- [x] a") || !strings.Contains(got, "- [ ] code") {
		t.Fatalf("toggle a: %s", got)
	}
	got, _ = ToggleCheckbox(src, 3)
	if !strings.Contains(got, "1. [x] numbered") {
		t.Fatalf("toggle numbered: %s", got)
	}
	if _, err := ToggleCheckbox(src, 9); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseTags(t *testing.T) {
	src := "# Heading\nWent running #Health #work/project and #health again.\nIssue #123, color `#fff` url http://x.com/#frag\n```\n#incode\n```\n#ខ្មែរ end"
	got := ParseTags(src)
	want := []string{"health", "work/project", "ខ្មែរ"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestNormalizeTags(t *testing.T) {
	got := NormalizeTags([]string{"#Work, home", "home", "bad tag!", ""})
	// "bad tag!" splits to "bad" (valid) and "tag!" (invalid, dropped).
	want := []string{"work", "home", "bad"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
