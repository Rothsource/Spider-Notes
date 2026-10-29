package xlsx

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestColName(t *testing.T) {
	for i, want := range map[int]string{0: "A", 25: "Z", 26: "AA", 27: "AB", 701: "ZZ", 702: "AAA"} {
		if got := ColName(i); got != want {
			t.Errorf("ColName(%d) = %s, want %s", i, got, want)
		}
	}
}

func TestSerial(t *testing.T) {
	loc := time.FixedZone("ICT", 7*3600)
	if got := Serial(time.Date(2026, 3, 1, 18, 0, 0, 0, loc)); got != 46082.75 {
		t.Fatalf("serial %v", got)
	}
}

func TestEscapingAndSheetNames(t *testing.T) {
	var buf bytes.Buffer
	err := Write(&buf, []Sheet{
		{Name: "a/b:c", Rows: [][]Cell{{{V: "=HYPERLINK(\"x\")"}, {V: "<&>\x01"}}}},
		{Name: "A/B:C", Rows: [][]Cell{{{V: 1}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	if !strings.Contains(s, "xl/worksheets/sheet2.xml") {
		t.Fatal("second sheet missing")
	}
	if strings.Contains(s, "<f>") {
		t.Fatal("text must never become a formula")
	}
	if err := Write(&buf, nil); err == nil {
		t.Fatal("empty workbook should fail")
	}
	used := map[string]bool{}
	if n := sheetName("a/b:c", 1, used); n != "a-b-c" {
		t.Fatalf("name %q", n)
	}
	if n := sheetName("A-B-C", 2, used); n != "Sheet2" {
		t.Fatalf("duplicate name should fall back: %q", n)
	}
	if got := esc("a<b>\x01&"); got != "a&lt;b&gt;&amp;" {
		t.Fatalf("esc %q", got)
	}
}
