package entitler_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func normalised(source string) string {
	var lines []string
	for line := range strings.SplitSeq(source, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// TestEverySnippetCompiles checks that each Go snippet in the README and docs is, line for line,
// in a file the build compiles: an example program or one of this package's example tests.
func TestEverySnippetCompiles(t *testing.T) {
	var sources strings.Builder
	files, _ := filepath.Glob("examples/*/*.go")
	files = append(files, "example_test.go", "docs_examples_test.go")
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		sources.WriteString(normalised(string(data)) + "\n")
	}
	compiled := sources.String()
	docs, _ := filepath.Glob("docs/*.md")
	docs = append(docs, "README.md")
	block := regexp.MustCompile("(?s)```go\n(.*?)```")
	count := 0
	for _, doc := range docs {
		data, err := os.ReadFile(doc)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range block.FindAllStringSubmatch(string(data), -1) {
			count++
			if !strings.Contains(compiled, normalised(m[1])) {
				t.Errorf("%s: a snippet is not in any compiled file:\n%s", doc, m[1])
			}
		}
	}
	if count < 20 {
		t.Errorf("only %d snippets", count)
	}
}
