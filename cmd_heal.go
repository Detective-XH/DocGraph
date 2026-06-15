package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Detective-XH/docgraph/internal/store"
)

type healFlags struct {
	project    string
	fix        bool
	owner      string
	gitTimeout time.Duration
}

func cmdHeal(args []string) {
	fset := flag.NewFlagSet("heal", flag.ExitOnError)
	var flags healFlags
	fset.StringVar(&flags.project, "project", ".", "project root")
	fset.BoolVar(&flags.fix, "fix", false, "apply inferred patches (default: dry-run)")
	fset.StringVar(&flags.owner, "owner", "", "override owner for all matched files")
	fset.DurationVar(&flags.gitTimeout, "git-timeout", 10*time.Second, "git log timeout per file (0 = no limit)")
	if err := fset.Parse(args); err != nil {
		log.Fatal(err)
	}

	root, err := filepath.Abs(flags.project)
	if err != nil {
		log.Fatal(err)
	}

	st, err := store.Open(filepath.Join(root, ".docgraph", "docgraph.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	if err := runHeal(st, root, flags); err != nil {
		log.Fatal(err)
	}
}

func runHeal(st *store.Store, root string, flags healFlags) error {
	docs, err := st.GetAllDocumentNodes()
	if err != nil {
		return fmt.Errorf("list documents: %w", err)
	}

	found := 0
	for _, doc := range docs {
		if !strings.HasSuffix(strings.ToLower(doc.FilePath), ".md") {
			continue
		}
		gov, err := st.GetGovernanceMetadata(doc.ID)
		if err != nil {
			return fmt.Errorf("governance for %s: %w", doc.FilePath, err)
		}

		var missing []string
		if gov == nil || gov.Status == "" {
			missing = append(missing, "status")
		}
		if gov == nil || gov.Owner == "" {
			missing = append(missing, "owner")
		}
		if len(missing) == 0 {
			continue
		}

		absPath := filepath.Join(root, doc.FilePath)
		if _, statErr := os.Stat(absPath); statErr != nil {
			continue
		}

		inferred, parts := healInferFields(doc.FilePath, absPath, missing, flags)

		label := "[dry-run]"
		if flags.fix && len(inferred) > 0 {
			if patchErr := patchMDFrontmatter(absPath, inferred); patchErr != nil {
				fmt.Fprintf(os.Stderr, "error patching %s: %v\n", doc.FilePath, patchErr)
			} else {
				label = "[patched]"
			}
		}

		fmt.Printf("%-9s %-60s %s\n", label, doc.FilePath, strings.Join(parts, "  "))
		found++
	}

	if found == 0 {
		fmt.Println("No documents with missing governance fields found.")
	}
	return nil
}

// healInferFields infers field values for a single document and returns the
// inferred map and the formatted output parts (e.g. "status=shipped").
func healInferFields(filePath, absPath string, missing []string, flags healFlags) (map[string]string, []string) {
	inferred := make(map[string]string)
	var parts []string
	for _, field := range missing {
		switch field {
		case "status":
			if v := inferStatus(filePath); v != "" {
				inferred["status"] = v
				parts = append(parts, "status="+v)
			}
		case "owner":
			v, skip := inferOwner(absPath, flags.owner, flags.gitTimeout)
			if skip != "" {
				parts = append(parts, "owner=skipped("+skip+")")
			} else if v != "" {
				inferred["owner"] = v
				parts = append(parts, "owner="+v)
			}
		}
	}
	return inferred, parts
}

// inferStatus returns the status value inferred from path segments, or "" to skip.
func inferStatus(filePath string) string {
	for seg := range strings.SplitSeq(filepath.ToSlash(filePath), "/") {
		switch seg {
		case "shipped":
			return "shipped"
		case "decisions":
			return "closed"
		}
	}
	return ""
}

// inferOwner returns the inferred owner name, or a non-empty skip reason.
// override bypasses git inference entirely (single-author / public-repo fast path).
func inferOwner(absPath, override string, timeout time.Duration) (value, skip string) {
	if override != "" {
		return override, ""
	}

	ctx := context.Background()
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "git", "log", "--follow", "--format=%aN", "--", absPath) // #nosec G204 -- fixed binary path; absPath is an absolute, store-validated file path, never user-controlled shell input
	cmd.Dir = filepath.Dir(absPath)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", "git-timeout"
		}
		return "", "git-error"
	}

	return parseGitOwnerOutput(string(out))
}

// parseGitOwnerOutput applies the ≥85%/commit_count≥3 rule to raw git-log output.
func parseGitOwnerOutput(output string) (value, skip string) {
	counts := make(map[string]int)
	total := 0
	for line := range strings.SplitSeq(output, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			counts[line]++
			total++
		}
	}

	if total < 3 {
		return "", "commit-count<3"
	}

	type entry struct {
		name  string
		count int
	}
	entries := make([]entry, 0, len(counts))
	for name, cnt := range counts {
		entries = append(entries, entry{name, cnt})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].name < entries[j].name
	})

	pct := float64(entries[0].count) / float64(total)
	if pct >= 0.85 {
		return entries[0].name, ""
	}

	p2 := float64(entries[1].count) / float64(total)
	return "", fmt.Sprintf("top-2: %s %.0f%%, %s %.0f%%", entries[0].name, pct*100, entries[1].name, p2*100)
}

// patchMDFrontmatter patches missing fields into the file's frontmatter.
// Uses yaml.Node to detect existing top-level keys — never re-encodes existing content.
// Follows the plan's step-3 logic: strip leading HTML comment first, then detect frontmatter.
func patchMDFrontmatter(path string, fields map[string]string) error {
	if len(fields) == 0 {
		return nil
	}

	raw, err := os.ReadFile(path) // #nosec G304 -- path is from the store index (absolute, cleaned) and validated by os.Stat before this call
	if err != nil {
		return err
	}

	// Strip UTF-8 BOM.
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})

	crlf := bytes.Contains(raw, []byte("\r\n"))

	// Strip leading HTML comment; fall through to normal frontmatter detection.
	var comment []byte
	body := raw
	if bytes.HasPrefix(raw, []byte("<!--")) {
		end := bytes.Index(raw, []byte("-->"))
		if end < 0 {
			return fmt.Errorf("unterminated HTML comment in %s", path)
		}
		end += 3
		comment = raw[:end]
		if bytes.Contains(comment, []byte("---")) || bytes.Count(comment, []byte("\n")) > 5 {
			return fmt.Errorf("%s: HTML comment too complex (contains --- or >5 lines); skipping", path)
		}
		body = bytes.TrimLeft(raw[end:], "\r\n")
	}

	var result []byte
	if bytes.HasPrefix(body, []byte("---")) {
		patched, patchErr := appendToExistingFrontmatter(body, fields, crlf)
		if patchErr != nil {
			return patchErr
		}
		if len(comment) > 0 {
			result = insertCommentAfterClose(patched, comment, crlf)
		} else {
			result = patched
		}
	} else {
		var buildErr error
		result, buildErr = buildFreshFrontmatter(body, comment, fields, crlf)
		if buildErr != nil {
			return buildErr
		}
	}

	return atomicWrite(path, result)
}

// appendToExistingFrontmatter parses the YAML frontmatter block via yaml.Node and
// appends only keys absent at the top level — existing content is never re-encoded.
func appendToExistingFrontmatter(content []byte, fields map[string]string, crlf bool) ([]byte, error) {
	closeIdx := findFrontmatterClose(content)
	if closeIdx < 0 {
		return nil, fmt.Errorf("no closing --- in frontmatter")
	}

	nlIdx := bytes.IndexByte(content, '\n')
	if nlIdx < 0 {
		return nil, fmt.Errorf("malformed frontmatter (no newline after opening ---)")
	}
	yamlBytes := content[nlIdx+1 : closeIdx]

	var doc yaml.Node
	if err := yaml.Unmarshal(yamlBytes, &doc); err != nil {
		return nil, fmt.Errorf("parse frontmatter YAML: %w", err)
	}

	existing := make(map[string]bool)
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		switch n := doc.Content[0]; n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				existing[n.Content[i].Value] = true
			}
		case yaml.ScalarNode:
			if n.Tag != "!!null" {
				return nil, fmt.Errorf("frontmatter is not a YAML mapping")
			}
		default:
			return nil, fmt.Errorf("frontmatter is not a YAML mapping")
		}
	}

	var additions bytes.Buffer
	for _, k := range sortedFieldKeys(fields) {
		if !existing[k] {
			valBytes, encErr := yaml.Marshal(fields[k])
			if encErr != nil {
				return nil, fmt.Errorf("encode field %s: %w", k, encErr)
			}
			fmt.Fprintf(&additions, "%s: %s", k, string(valBytes))
		}
	}
	if additions.Len() == 0 {
		return content, nil
	}

	var out bytes.Buffer
	out.Write(content[:closeIdx])
	out.Write(additions.Bytes())
	out.Write(content[closeIdx:])
	result := out.Bytes()
	if crlf {
		result = normCRLF(result)
	}
	return result, nil
}

// buildFreshFrontmatter inserts ---\n<fields>\n---\n before body.
// If comment is non-nil it is placed after the closing ---.
func buildFreshFrontmatter(body, comment []byte, fields map[string]string, crlf bool) ([]byte, error) {
	var out bytes.Buffer
	out.WriteString("---\n")
	for _, k := range sortedFieldKeys(fields) {
		valBytes, err := yaml.Marshal(fields[k])
		if err != nil {
			return nil, fmt.Errorf("encode field %s: %w", k, err)
		}
		fmt.Fprintf(&out, "%s: %s", k, string(valBytes))
	}
	out.WriteString("---\n")
	if len(comment) > 0 {
		out.Write(comment)
		out.WriteByte('\n')
	}
	out.Write(body)
	result := out.Bytes()
	if crlf {
		result = normCRLF(result)
	}
	return result, nil
}

// insertCommentAfterClose places comment on the line immediately after the
// closing --- of an already-patched content block.
func insertCommentAfterClose(content, comment []byte, crlf bool) []byte {
	closeIdx := findFrontmatterClose(content)
	if closeIdx < 0 {
		return append(content, append([]byte("\n"), comment...)...)
	}
	endLine := closeIdx + 3 // past ---
	if endLine < len(content) && content[endLine] == '\r' {
		endLine++
	}
	if endLine < len(content) && content[endLine] == '\n' {
		endLine++
	}

	var out bytes.Buffer
	out.Write(content[:endLine])
	out.Write(comment)
	out.WriteByte('\n')
	out.Write(content[endLine:])
	result := out.Bytes()
	if crlf {
		result = normCRLF(result)
	}
	return result
}

// findFrontmatterClose returns the byte index of the first unindented ---
// on its own line after the opening fence. Returns -1 if not found.
func findFrontmatterClose(content []byte) int {
	i := bytes.IndexByte(content, '\n')
	if i < 0 {
		return -1
	}
	i++
	for i < len(content) {
		end := bytes.IndexByte(content[i:], '\n')
		var line []byte
		if end < 0 {
			line = content[i:]
		} else {
			line = content[i : i+end]
		}
		line = bytes.TrimSuffix(line, []byte("\r"))
		if string(line) == "---" {
			return i
		}
		if end < 0 {
			break
		}
		i += end + 1
	}
	return -1
}

// atomicWrite writes data to a .heal_tmp file, syncs, then renames over path.
func atomicWrite(path string, data []byte) error {
	tmp := path + ".heal_tmp"
	f, err := os.Create(tmp) // #nosec G304 -- tmp = path + ".heal_tmp"; path is an absolute, store-validated file path
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// normCRLF normalises all line endings to \r\n (idempotent).
func normCRLF(b []byte) []byte {
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	return bytes.ReplaceAll(b, []byte("\n"), []byte("\r\n"))
}

// sortedFieldKeys returns map keys in sorted order for deterministic output.
func sortedFieldKeys(fields map[string]string) []string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
