package main

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed all:skills
var skillsFS embed.FS

func installSkills(root string, overwrite bool) error {
	entries, err := fs.ReadDir(skillsFS, "skills")
	if err != nil {
		return fmt.Errorf("read embedded skills: %w", err)
	}
	dest := filepath.Join(root, ".claude", "skills")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("create .claude/skills: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillDir := filepath.Join(dest, e.Name())
		if _, statErr := os.Stat(skillDir); statErr == nil {
			if overwrite {
				if err := os.RemoveAll(skillDir); err != nil {
					return fmt.Errorf("remove skill dir %s: %w", e.Name(), err)
				}
			} else {
				fmt.Fprintf(os.Stderr, "  skip (exists): .claude/skills/%s/\n", e.Name())
				continue
			}
		}
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			return fmt.Errorf("create skill dir %s: %w", e.Name(), err)
		}
		srcPath := "skills/" + e.Name() + "/SKILL.md"
		data, err := fs.ReadFile(skillsFS, srcPath)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", srcPath, err)
		}
		destPath := filepath.Join(skillDir, "SKILL.md")
		if err := os.WriteFile(destPath, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", destPath, err)
		}
		fmt.Fprintf(os.Stderr, "  installed: .claude/skills/%s/SKILL.md\n", e.Name())
	}
	return nil
}
