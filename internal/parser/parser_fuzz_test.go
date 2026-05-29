package parser

import (
	"testing"
)

// FuzzParseMarkdown brings the Markdown/YAML pipeline up to fuzz parity with the
// PDF/DOCX/HTML extractors (security-audit backlog #5). It drives the real
// entry point ParseFile — frontmatter extraction (goldmark-meta + yaml.v3),
// the goldmark AST walk, and our own wikilink/definition/heading scanners —
// against arbitrary bytes.
//
// Unlike extractor.Extract, ParseFile has no recover of its own: on the serve
// watcher path it is shielded by the watcher's per-project recover (#4), but a
// foreground `docgraph index`/`sync` would crash on a panic here. So any panic
// the fuzzer finds is a real finding to fix at the source. The fuzzer reports a
// panic as a crasher with a minimized input; the committed seed corpus must
// always pass under plain `go test`.
func FuzzParseMarkdown(f *testing.F) {
	seeds := []string{
		"",
		"# Heading\n\nbody with [[wikilink]] and [text](./other.md).",
		"---\ntitle: T\ntags: [a, b]\nrelated_to: [[x]]\n---\n# H\n",
		"**Term:** a definition line\n",
		"---\nnot: valid: yaml: : :\n---\nbody",
		"![[embed.png]] ![[doc#frag|alias]] [[a|b]]",
		"你好 [[中文連結]] **定義:** 值\n# 標題🔥\n",
		"---\nx: " + string(rune(0)) + "\n---\nbody" + string(rune(0)),
		"# " + "#### nested ###### markers ##\n> quote\n\n- list\n\n```go\ncode\n```\n",
		buildAliasBomb(6, 10),
		buildDeepNest(4000),
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		// No output assertions — the contract is simply "never panic".
		_, _ = ParseFile("/tmp/fuzz.md", "fuzz.md", data, "deadbeef")
		// Also exercise the standalone frontmatter entry, which is reachable
		// independently (e.g. drift tooling) and wraps the YAML bomb defenses.
		_, _ = ExtractFrontmatter(data)
	})
}
