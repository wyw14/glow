// Package utils provides utility functions.
package utils

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"charm.land/glow/v3/styleplan"
	"charm.land/lipgloss/v2"
	"github.com/mitchellh/go-homedir"
)

// RemoveFrontmatter removes the front matter header of a markdown file.
func RemoveFrontmatter(content []byte) []byte {
	if frontmatterBoundaries := detectFrontmatter(content); frontmatterBoundaries[0] == 0 {
		return content[frontmatterBoundaries[1]:]
	}
	return content
}

var yamlPattern = regexp.MustCompile(`(?m)^---\r?\n(\s*\r?\n)?`)

func detectFrontmatter(c []byte) []int {
	if matches := yamlPattern.FindAllIndex(c, 2); len(matches) > 1 {
		return []int{matches[0][0], matches[1][1]}
	}
	return []int{-1, -1}
}

// ExpandPath expands tilde and all environment variables from the given path.
func ExpandPath(path string) string {
	s, err := homedir.Expand(path)
	if err == nil {
		return os.ExpandEnv(s)
	}
	return os.ExpandEnv(path)
}

// WrapCodeBlock wraps a string in a code block with the given language.
func WrapCodeBlock(s, language string) string {
	return "```" + language + "\n" + s + "```"
}

var markdownExtensions = []string{
	".md", ".mdown", ".mkdn", ".mkd", ".markdown",
}

// IsMarkdownFile returns whether the filename has a markdown extension.
func IsMarkdownFile(filename string) bool {
	ext := filepath.Ext(filename)

	if ext == "" {
		// By default, assume it's a markdown file.
		return true
	}

	for _, v := range markdownExtensions {
		if strings.EqualFold(ext, v) {
			return true
		}
	}

	// Has an extension but not markdown
	// so assume this is a code file.
	return false
}

// GlamourStyle returns a glamour.TermRendererOption based on the given style.
func GlamourStyle(style string, isCode bool) glamour.TermRendererOption {
	return func(tr *glamour.TermRenderer) error {
		styleConfig, err := styleConfigFor(style, isCode)
		if err != nil {
			return err
		}
		if !isCode && TextSizingEnabled() {
			AddHeadingSizeMarkers(&styleConfig)
		}
		if err := glamour.WithStyles(styleConfig)(tr); err != nil {
			return fmt.Errorf("glamour: error applying styles: %w", err)
		}
		return nil
	}
}

// GlamourStyleWithPlan returns a glamour.TermRendererOption that honors the
// immutable rendering strategy planned from the detected terminal
// capabilities. Planning never writes to the terminal or changes the user
// theme; degradations are applied to a caller owned style config copy.
func GlamourStyleWithPlan(style string, isCode bool, plan *styleplan.Strategy) glamour.TermRendererOption {
	return func(tr *glamour.TermRenderer) error {
		theme := style
		if plan != nil && plan.ResolvedTheme() != "" {
			theme = plan.ResolvedTheme()
		}

		styleConfig, err := resolveStyleConfig(theme, isCode)
		if err != nil {
			return err
		}
		if plan != nil {
			styleplan.Apply(&styleConfig, plan)
			if !isCode && plan.TextSizing() {
				AddHeadingSizeMarkers(&styleConfig)
			}
		}

		if err := glamour.WithStyles(styleConfig)(tr); err != nil {
			return fmt.Errorf("glamour: error applying styles: %w", err)
		}
		return nil
	}
}

func styleConfigFor(style string, isCode bool) (ansi.StyleConfig, error) {
	// The TUI path keeps the interactive background probe. CLI rendering goes
	// through GlamourStyleWithPlan instead, whose planning phase must stay
	// free of terminal writes.
	if isCode && style == "auto" {
		if lipgloss.HasDarkBackground(os.Stdin, os.Stdout) {
			style = styles.DarkStyle
		} else {
			style = styles.LightStyle
		}
	}
	return resolveStyleConfig(style, isCode)
}

// resolveStyleConfig loads the style config for an already resolved theme
// without probing the terminal. Default styles are returned as struct copies
// and code files get their code block margin removed. Nested pointers stay
// shared with the default styles, so callers must replace pointers instead of
// mutating their targets, as styleplan.Apply does.
func resolveStyleConfig(theme string, isCode bool) (ansi.StyleConfig, error) {
	if theme == "auto" || theme == "" {
		theme = styles.DarkStyle
	}
	if isCode && theme == styles.TokyoNightStyle {
		theme = styles.DraculaStyle
	}
	if cfg, ok := styles.DefaultStyles[theme]; ok {
		styleConfig := *cfg
		if isCode {
			var margin uint
			styleConfig.CodeBlock.Margin = &margin
		}
		return styleConfig, nil
	}
	return styleConfigFromFile(theme)
}

func styleConfigFromFile(path string) (ansi.StyleConfig, error) {
	jsonBytes, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return ansi.StyleConfig{}, fmt.Errorf("glamour: error reading file: %w", err)
	}

	var styleConfig ansi.StyleConfig
	if err := json.Unmarshal(jsonBytes, &styleConfig); err != nil {
		return ansi.StyleConfig{}, fmt.Errorf("glamour: error parsing style file: %w", err)
	}
	return styleConfig, nil
}
