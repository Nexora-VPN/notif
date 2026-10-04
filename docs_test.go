package main

import (
	"os"
	"regexp"
	"testing"
)

// TestTheDocsKeepStep: the four languages have the same sections and the
// same code blocks, so none falls behind the others.
func TestTheDocsKeepStep(t *testing.T) {
	count := func(lang string) [3]int {
		b, err := os.ReadFile("docs/" + lang + ".md")
		if err != nil {
			t.Fatal(err)
		}
		return [3]int{
			len(regexp.MustCompile(`(?m)^## `).FindAll(b, -1)),
			len(regexp.MustCompile(`(?m)^### `).FindAll(b, -1)),
			len(regexp.MustCompile("(?m)^```").FindAll(b, -1)),
		}
	}
	en := count("en")
	for _, l := range []string{"fa", "ru", "zh"} {
		if got := count(l); got != en {
			t.Errorf("docs/%s.md has %v sections, subsections and fences; en has %v", l, got, en)
		}
	}
}
