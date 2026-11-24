package api

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"
)

func ruleJSON(name string) string {
	return fmt.Sprintf(`{"name":%q,"type":"novel","baseUrl":"https://example.com","search":{"url":"/s?q={{key}}","list":"css:li","title":"css:a","bookUrl":"css:a@href"},"toc":{"list":"css:#list a","name":"@text","url":"@href"},"content":{"body":"css:#content"}}`, name)
}

func TestStreamRulesShapes(t *testing.T) {
	a, b := ruleJSON("甲"), ruleJSON("乙")
	for name, tc := range map[string]struct {
		doc         string
		rules, left int
	}{
		"array":            {"\xef\xbb\xbf \n[" + a + "," + b + ",{\"name\":\"坏\"}," + a + "]", 2, 2},
		"one rule":         {a, 1, 0},
		"bundle":           {`{"version":1,"sources":[` + a + `,` + b + `],"note":{"x":[1,2]}}`, 2, 0},
		"sources not list": {`{"sources":{"a":[{}]},` + a[1:], 1, 0},
		"empty array":      {`[]`, 0, 0},
	} {
		// One byte at a time: nothing may depend on how the document is cut into reads.
		rules, left, err := streamRules(iotest.OneByteReader(strings.NewReader(tc.doc)))
		if err != nil || len(rules) != tc.rules || len(left) != tc.left {
			t.Errorf("%s: %d rules, %d left, %v", name, len(rules), len(left), err)
		}
	}
	if _, _, err := streamRules(strings.NewReader("<!DOCTYPE html>")); !errors.Is(err, errNotRules) {
		t.Errorf("html: %v", err)
	}
	if _, _, err := streamRules(strings.NewReader("[" + a + ",{")); err == nil {
		t.Error("a list cut short was read")
	}
}

// repeated yields a JSON array of n rules without ever holding it.
type repeated struct {
	n, i int
	cur  io.Reader
}

func (r *repeated) Read(p []byte) (int, error) {
	for {
		if r.cur == nil {
			switch {
			case r.i > r.n:
				return 0, io.EOF
			case r.i == r.n:
				r.cur = strings.NewReader("]")
			case r.i == 0:
				r.cur = strings.NewReader("[" + ruleJSON("s0"))
			default:
				// Padding stands for the long rules of real lists.
				r.cur = strings.NewReader("," + strings.Replace(ruleJSON(fmt.Sprint("s", r.i)), "{", `{"pad":"`+strings.Repeat("x", 4000)+`",`, 1))
			}
			r.i++
		}
		if n, _ := r.cur.Read(p); n > 0 {
			return n, nil
		}
		r.cur = nil
	}
}

func TestStreamRulesReadsALargeListInLittleMemory(t *testing.T) {
	const n = 20000 // about 85 MB of JSON
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	rules, left, err := streamRules(&repeated{n: n})
	runtime.GC()
	runtime.ReadMemStats(&after)
	if err != nil || len(rules) != n || len(left) != 0 {
		t.Fatalf("%d rules, %d left, %v", len(rules), len(left), err)
	}
	if kept := int64(after.HeapAlloc) - int64(before.HeapAlloc); kept > 40<<20 {
		t.Fatalf("%d MB kept for %d rules", kept>>20, n)
	}
}

func TestStreamRulesBoundsOneRule(t *testing.T) {
	big := `{"name":"` + strings.Repeat("x", maxRule) + `"}`
	rules, left, err := streamRules(strings.NewReader("[" + big + "," + ruleJSON("甲") + "]"))
	if err != nil || len(rules) != 1 || len(left) != 1 {
		t.Fatalf("%d rules, %+v, %v", len(rules), len(left), err)
	}
	endless := io.MultiReader(strings.NewReader(`[{"name":"`), &forever{})
	if _, _, err := streamRules(endless); !errors.Is(err, errRuleSize) {
		t.Fatalf("endless rule: %v", err)
	}
}

type forever struct{}

func (forever) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

func TestStreamRulesRejectsADocumentThatDoesNotEnd(t *testing.T) {
	rule := `{"name":"a","type":"novel","baseUrl":"https://example.com"}`
	for _, doc := range []string{
		`{"sources":[` + rule + `]`,
		`[` + rule + `] x`,
		`[` + rule + `][`,
		`{"sources":[` + rule + `]} {}`,
		rule + ` 1`,
	} {
		if rules, _, err := streamRules(strings.NewReader(doc)); err == nil {
			t.Errorf("%q: read as %d rules", doc, len(rules))
		}
	}
	for _, doc := range []string{`[` + rule + `]` + " \n", `{"sources":[` + rule + `]}` + "\n"} {
		if rules, _, err := streamRules(strings.NewReader(doc)); err != nil || len(rules) != 1 {
			t.Errorf("%q: %d rules, %v", doc, len(rules), err)
		}
	}
}

func TestStreamRulesBoundsTheNumberOfRules(t *testing.T) {
	doc := "[" + strings.Repeat("0,", maxListRules) + "0]"
	if _, _, err := streamRules(strings.NewReader(doc)); !errors.Is(err, errTooMany) {
		t.Fatalf("err: %v", err)
	}
}
