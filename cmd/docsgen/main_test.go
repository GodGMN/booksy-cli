package main

import "testing"

// Only the opening delimiters are MDX syntax; `>` and `}` are ordinary text, so
// escaping them would show backslashes to the reader for nothing.
func TestMdxProseEscapesOnlyTheOpeningDelimiters(t *testing.T) {
	got := mdxProse("pass <hash> and {x}")
	want := `pass \<hash> and \{x}`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSplitExamplesLiftsTheExampleBlock(t *testing.T) {
	body, examples := splitExamples("Does a thing.\n\nExamples:\n  booksy a\n\n  booksy b\n")
	if body != "Does a thing." {
		t.Fatalf("body = %q", body)
	}
	if examples != "booksy a\nbooksy b" {
		t.Fatalf("examples = %q", examples)
	}
}
