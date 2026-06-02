package callout

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	divider     = "════════════════════════════════════════════════════════════════════════"
	thinDivider = "────────────────────────────────────────────────────────────────────────"
)

// PendingDoc is one document awaiting LLM processing.
type PendingDoc struct {
	FilePath    string // relative to WorkspaceDir
	BodyExcerpt string // from DB; used for token estimation only — no disk read
}

// ImpactOpts configures BuildImpactGraph output.
type ImpactOpts struct {
	ToolName          string      // "docgraph_embeddings" or "docgraph_enrichment"
	ModelHint         string      // shown in header, e.g. "text-embedding-3-small"
	WorkspaceDir      string      // workspace root (or project root in single-project mode)
	Rates             []ModelRate // from DefaultRates()
	ConfirmationToken string      // hex token; empty when all-sensitive or N=0
}

// BuildImpactGraph returns the full 3-section ASCII block:
//   - section 1 — scope tree (per-folder file + token counts)
//   - section 2 — RELAY block (pre-written user message with computed numbers)
//   - section 3 — ACTION block (LLM instruction: wait → confirm → process)
//
// Returns a single-line "no pending" message when len(docs) == 0.
func BuildImpactGraph(docs []PendingDoc, opts ImpactOpts) string {
	if len(docs) == 0 {
		return "No pending documents — all files are already processed."
	}

	paths := make([]string, len(docs))
	for i, d := range docs {
		paths[i] = d.FilePath
	}

	allSensitive := IsAllSensitive(paths)
	sensitiveFlags := FlagSensitivePaths(paths)

	totalTok := 0
	for _, d := range docs {
		totalTok += EstimateTokens(d.BodyExcerpt)
	}

	var sb strings.Builder

	header := fmt.Sprintf("## Pending scope — %s", opts.ToolName)
	if opts.ModelHint != "" {
		header += fmt.Sprintf(" (model: %s)", opts.ModelHint)
	}
	sb.WriteString(header)
	sb.WriteString("\n")
	sb.WriteString(divider)
	sb.WriteString("\n\n")

	// --- Section 1: Scope tree ---
	buildScopeTree(&sb, docs, opts, totalTok)

	sb.WriteString("\n" + divider + "\n")

	// --- Section 2: RELAY block ---
	buildRelaySection(&sb, docs, opts, totalTok, allSensitive, sensitiveFlags)

	sb.WriteString("\n" + thinDivider + "\n")

	// --- Section 3: ACTION block ---
	buildActionSection(&sb, opts, allSensitive)

	sb.WriteString(divider + "\n")

	return sb.String()
}

// buildScopeTree writes the scope-tree portion of Section 1 into sb.
func buildScopeTree(sb *strings.Builder, docs []PendingDoc, opts ImpactOpts, totalTok int) {
	workspaceName := filepath.Base(opts.WorkspaceDir)
	if workspaceName == "" || workspaceName == "." {
		workspaceName = "workspace"
	}
	fmt.Fprintf(sb, "%-52s %5d files  ~%d tok\n", workspaceName+"/", len(docs), totalTok)

	topDirOrder, topDirFiles, topDirTok, subDirOrder, subDirFiles, subDirTok := buildTree(docs)

	for i, top := range topDirOrder {
		isLastTop := i == len(topDirOrder)-1
		topPfx := "├── "
		if isLastTop {
			topPfx = "└── "
		}
		topSens := ""
		if pathHasSensitiveComponent(top) {
			topSens = "  ⚠️ SENSITIVE"
		}
		label := top + "/"
		fmt.Fprintf(sb, "%s%-42s%s %5d files  ~%d tok\n",
			topPfx, label, topSens, topDirFiles[top], topDirTok[top])

		childPfx := "│   "
		if isLastTop {
			childPfx = "    "
		}
		subs := subDirOrder[top]
		for j, sub := range subs {
			isLastSub := j == len(subs)-1
			subPfx := childPfx + "├── "
			if isLastSub {
				subPfx = childPfx + "└── "
			}
			subSens := ""
			if pathHasSensitiveComponent(sub) {
				subSens = "  ⚠️ SENSITIVE"
			}
			subLabel := strings.TrimPrefix(sub, top+"/") + "/"
			fmt.Fprintf(sb, "%s%-38s%s %5d files  ~%d tok\n",
				subPfx, subLabel, subSens, subDirFiles[sub], subDirTok[sub])
		}
	}
}

// buildCostSection writes the cost-estimates block and rates footer into sb.
// Called from within buildRelaySection's non-all-sensitive path.
func buildCostSection(sb *strings.Builder, totalTok int, opts ImpactOpts) {
	costLines := EstimateCost(totalTok, opts.Rates)
	for _, cl := range costLines {
		marker := ""
		if opts.ModelHint != "" && strings.EqualFold(cl.ModelName, opts.ModelHint) {
			marker = "  ← selected model"
		}
		fmt.Fprintf(sb, "  • %-32s : ~$%.2f%s\n", cl.ModelName, cl.CostUSD, marker)
	}

	asOf := mostRecentAsOf(opts.Rates)
	if asOf != "" {
		sb.WriteString("\nRates as of ")
		sb.WriteString(asOf)
		if warn := staleRateWarning(opts.Rates, time.Now()); warn != "" {
			sb.WriteString(" — ")
			sb.WriteString(warn)
		}
		sb.WriteString("\n")
	}
}

// buildRelaySection writes Section 2 (the RELAY block) into sb.
func buildRelaySection(sb *strings.Builder, docs []PendingDoc, opts ImpactOpts, totalTok int, allSensitive bool, sensitiveFlags []SensitiveFlag) {
	sb.WriteString("── RELAY THIS TO THE USER (copy verbatim) ──────────────────────────────\n\n")

	if allSensitive {
		sb.WriteString("⛔ ALL pending files are in sensitive-path folders. I cannot proceed.\n")
		sb.WriteString("Please add non-sensitive files or exclude these paths via .docgraphignore before retrying.\n")
		return
	}

	totalSensitive := 0
	var sensFolders []string
	for _, f := range sensitiveFlags {
		totalSensitive += f.FileCount
		if f.Path != "" {
			sensFolders = append(sensFolders, f.Path+"/")
		} else {
			sensFolders = append(sensFolders, "(root)")
		}
	}

	fmt.Fprintf(sb, "I found **%d documents** pending processing (~%d tokens).\n\n", len(docs), totalTok)
	sb.WriteString("Estimated cost to process all files:\n")

	buildCostSection(sb, totalTok, opts)

	if totalSensitive > 0 {
		fmt.Fprintf(sb, "\n⚠️  %d files are in sensitive-path folder(s) (%s).\n",
			totalSensitive, strings.Join(sensFolders, ", "))
		sb.WriteString("   I will not process anything until you confirm.\n")
	}

	sb.WriteString("\nPlease confirm:\n")
	sb.WriteString("  1. **Scope** — all files, a specific folder, or a limit?\n")
	sb.WriteString("  2. **Model** — which provider and model?\n")
	if totalSensitive > 0 {
		sb.WriteString("  3. **Sensitive files** — include or skip?\n")
	}
}

// buildActionSection writes Section 3 (the ACTION block) into sb.
func buildActionSection(sb *strings.Builder, opts ImpactOpts, allSensitive bool) {
	sb.WriteString("── ACTION (for LLM only — do not show to user) ─────────────────────────\n")

	if allSensitive {
		sb.WriteString("⛔ ALL SENSITIVE — do NOT call the processing action.\n")
		sb.WriteString("WAIT for user to add non-sensitive files or update .docgraphignore.\n")
		return
	}

	sb.WriteString("WAIT for user to confirm scope + model + sensitive paths (if any).\n")
	if opts.ConfirmationToken != "" {
		fmt.Fprintf(sb, "CONFIRMATION_TOKEN: %s\n", opts.ConfirmationToken)
		sb.WriteString("TOKEN_EXPIRES: 30 minutes from now\n")
		nextAction := "action=store"
		if opts.ToolName == "docgraph_enrichment" {
			nextAction = "action=process"
		}
		fmt.Fprintf(sb, "CONFIRMED → call %s scope=<choice> model=<choice> confirmation_token=<token above>\n", nextAction)
		sb.WriteString("NOT CONFIRMED or \"cancel\" → do NOT call the processing action.\n")
	}
}

// EstimateTokens estimates token count using a blended CJK/Latin ratio.
// CJK ≈ 1 tok/char; Latin/other ≈ 0.25 tok/char. Errs high for CJK-aware models.
func EstimateTokens(text string) int {
	var cjk, other int
	for _, r := range text {
		if unicode.Is(unicode.Han, r) ||
			unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r) ||
			unicode.Is(unicode.Hangul, r) {
			cjk++
		} else {
			other++
		}
	}
	return cjk + other/4
}

// buildTree groups docs into top-level and second-level directories.
// Returns ordered slices and maps for deterministic output.
func buildTree(docs []PendingDoc) (
	topOrder []string,
	topFiles map[string]int,
	topTok map[string]int,
	subOrder map[string][]string,
	subFiles map[string]int,
	subTok map[string]int,
) {
	topFiles = make(map[string]int)
	topTok = make(map[string]int)
	subFiles = make(map[string]int)
	subTok = make(map[string]int)
	subOrder = make(map[string][]string)
	subSet := make(map[string]map[string]bool)

	for _, d := range docs {
		tok := EstimateTokens(d.BodyExcerpt)
		parts := strings.SplitN(filepath.ToSlash(d.FilePath), "/", 3)

		var top string
		if len(parts) > 1 {
			top = parts[0]
		} else {
			top = ""
		}
		topFiles[top]++
		topTok[top] += tok

		if len(parts) > 2 {
			sub := parts[0] + "/" + parts[1]
			subFiles[sub]++
			subTok[sub] += tok
			if subSet[top] == nil {
				subSet[top] = make(map[string]bool)
			}
			subSet[top][sub] = true
		}
	}

	for top := range topFiles {
		topOrder = append(topOrder, top)
	}
	sort.Strings(topOrder)

	for top, subs := range subSet {
		sl := make([]string, 0, len(subs))
		for sub := range subs {
			sl = append(sl, sub)
		}
		sort.Strings(sl)
		subOrder[top] = sl
	}

	return
}
