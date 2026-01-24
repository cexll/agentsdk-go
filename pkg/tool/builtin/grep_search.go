package toolbuiltin

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cexll/agentsdk-go/pkg/gitignore"
)

type grepSearchOptions struct {
	before           int
	after            int
	glob             string
	typeGlobs        []string
	root             string
	multiline        bool
	gitignoreMatcher *gitignore.Matcher
}

type fileCount struct {
	File  string `json:"file"`
	Count int    `json:"count"`
}

type formattedGrepResult struct {
	output       string
	matches      []GrepMatch
	files        []string
	counts       map[string]int
	displayCount int
	truncated    bool
}

func (g *GrepTool) searchDirectory(ctx context.Context, root string, re *regexp.Regexp, opts grepSearchOptions, matches *[]GrepMatch) (bool, error) {
	root = filepath.Clean(root)
	opts.root = root
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Filter out gitignored paths
		if opts.gitignoreMatcher != nil {
			relPath := displayPath(path, root)
			if opts.gitignoreMatcher.Match(relPath, d.IsDir()) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		if d.IsDir() {
			if relativeDepth(root, path) > g.maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		truncated, err := g.searchFile(ctx, path, re, opts, matches)
		if err != nil {
			return err
		}
		if truncated {
			return errGrepLimitReached
		}
		return nil
	})
	if errors.Is(err, errGrepLimitReached) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

func (g *GrepTool) searchFile(ctx context.Context, path string, re *regexp.Regexp, opts grepSearchOptions, matches *[]GrepMatch) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := g.sandbox.ValidatePath(path); err != nil {
		return false, err
	}
	allowed, err := opts.allow(path)
	if err != nil {
		return false, err
	}
	if !allowed {
		return false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read file: %w", err)
	}
	contents := string(data)
	lines := splitGrepLines(contents)
	display := displayPath(path, g.root)

	if opts.multiline {
		cursor := 0
		lineNumber := 1
		for _, loc := range re.FindAllStringIndex(contents, -1) {
			start := loc[0]
			lineNumber += strings.Count(contents[cursor:start], "\n")
			cursor = start
			match := GrepMatch{
				File:  display,
				Line:  lineNumber,
				Match: strings.TrimRight(contents[loc[0]:loc[1]], "\r\n"),
			}
			if before, after := surroundingLines(lines, lineNumber-1, opts.before, opts.after); len(before) > 0 || len(after) > 0 {
				if len(before) > 0 {
					match.Before = before
				}
				if len(after) > 0 {
					match.After = after
				}
			}
			*matches = append(*matches, match)
			if len(*matches) >= g.maxResults {
				return true, nil
			}
		}
		return false, nil
	}

	for idx, line := range lines {
		if !re.MatchString(line) {
			continue
		}
		match := GrepMatch{
			File:  display,
			Line:  idx + 1,
			Match: line,
		}
		if before, after := surroundingLines(lines, idx, opts.before, opts.after); len(before) > 0 || len(after) > 0 {
			if len(before) > 0 {
				match.Before = before
			}
			if len(after) > 0 {
				match.After = after
			}
		}
		*matches = append(*matches, match)
		if len(*matches) >= g.maxResults {
			return true, nil
		}
	}
	return false, nil
}

func (opts grepSearchOptions) allow(path string) (bool, error) {
	rel := path
	if opts.root != "" {
		if r, err := filepath.Rel(opts.root, path); err == nil {
			clean := filepath.Clean(r)
			if !strings.HasPrefix(clean, "..") {
				rel = clean
			}
		}
	}
	if opts.glob != "" {
		ok, err := filepath.Match(opts.glob, rel)
		if err != nil {
			return false, fmt.Errorf("invalid glob %q: %w", opts.glob, err)
		}
		if !ok {
			return false, nil
		}
	}
	if len(opts.typeGlobs) > 0 {
		base := filepath.Base(path)
		matched := false
		for _, pattern := range opts.typeGlobs {
			ok, err := filepath.Match(pattern, base)
			if err != nil {
				return false, fmt.Errorf("invalid type pattern %q: %w", pattern, err)
			}
			if ok {
				matched = true
				break
			}
		}
		if !matched {
			return false, nil
		}
	}
	return true, nil
}

func relativeDepth(base, target string) int {
	if base == target {
		return 0
	}
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return 0
	}
	rel = filepath.Clean(rel)
	if rel == "." || strings.HasPrefix(rel, "..") {
		return 0
	}
	return len(strings.Split(rel, string(filepath.Separator)))
}

func splitGrepLines(contents string) []string {
	if contents == "" {
		return nil
	}
	lines := strings.Split(contents, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	return lines
}

func surroundingLines(lines []string, idx, before, after int) ([]string, []string) {
	if before <= 0 && after <= 0 {
		return nil, nil
	}
	start := idx - before
	if start < 0 {
		start = 0
	}
	beforeLines := append([]string(nil), lines[start:idx]...)

	end := idx + after + 1
	if end > len(lines) {
		end = len(lines)
	}
	afterLines := append([]string(nil), lines[idx+1:end]...)
	return beforeLines, afterLines
}

func applyWindow[T any](items []T, offset, head int) ([]T, bool) {
	if len(items) == 0 {
		return nil, false
	}
	if offset >= len(items) {
		return nil, true
	}
	end := len(items)
	if head > 0 && offset+head < end {
		end = offset + head
	}
	truncated := offset > 0 || end < len(items)
	return items[offset:end], truncated
}

func uniqueFiles(matches []GrepMatch) []string {
	seen := make(map[string]struct{})
	files := make([]string, 0, len(matches))
	for _, match := range matches {
		if _, ok := seen[match.File]; ok {
			continue
		}
		seen[match.File] = struct{}{}
		files = append(files, match.File)
	}
	return files
}

func collectFileCounts(matches []GrepMatch) []fileCount {
	if len(matches) == 0 {
		return nil
	}
	counter := make(map[string]int)
	ordered := make([]fileCount, 0, len(matches))
	for _, match := range matches {
		if counter[match.File] == 0 {
			ordered = append(ordered, fileCount{File: match.File})
		}
		counter[match.File]++
	}
	for i := range ordered {
		ordered[i].Count = counter[ordered[i].File]
	}
	return ordered
}

func countsToMap(entries []fileCount) map[string]int {
	if len(entries) == 0 {
		return nil
	}
	out := make(map[string]int, len(entries))
	for _, entry := range entries {
		out[entry.File] = entry.Count
	}
	return out
}

func formatContentOutput(matches []GrepMatch, lineNumbers bool) string {
	if len(matches) == 0 {
		return "no matches"
	}
	var b strings.Builder
	for i, match := range matches {
		if i > 0 {
			b.WriteByte('\n')
		}
		if lineNumbers {
			fmt.Fprintf(&b, "%s:%d: %s", match.File, match.Line, match.Match)
		} else {
			fmt.Fprintf(&b, "%s: %s", match.File, match.Match)
		}
		if len(match.Before) > 0 || len(match.After) > 0 {
			if len(match.Before) > 0 {
				for idx, line := range match.Before {
					fmt.Fprintf(&b, "\n  -%d: %s", len(match.Before)-idx, line)
				}
			}
			if len(match.After) > 0 {
				for idx, line := range match.After {
					fmt.Fprintf(&b, "\n  +%d: %s", idx+1, line)
				}
			}
		}
	}
	return b.String()
}

func formatFilesOutput(files []string) string {
	if len(files) == 0 {
		return "no matches"
	}
	return strings.Join(files, "\n")
}

func formatCountOutput(counts []fileCount) string {
	if len(counts) == 0 {
		return "no matches"
	}
	var b strings.Builder
	for i, entry := range counts {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%s: %d", entry.File, entry.Count)
	}
	return b.String()
}

func appendTruncatedNote(output string, truncated bool, shown int) string {
	if !truncated {
		return output
	}
	if output == "" {
		return fmt.Sprintf("... truncated to %d results", shown)
	}
	return fmt.Sprintf("%s\n... truncated to %d results", output, shown)
}

func formatGrepOutput(mode string, matches []GrepMatch, lineNumbers bool, headLimit, offset int, searchTruncated bool) formattedGrepResult {
	switch mode {
	case "content":
		window, paginated := applyWindow(matches, offset, headLimit)
		truncated := searchTruncated || paginated
		return formattedGrepResult{
			output:       appendTruncatedNote(formatContentOutput(window, lineNumbers), truncated, len(window)),
			matches:      window,
			displayCount: len(window),
			truncated:    truncated,
		}
	case "count":
		counts := collectFileCounts(matches)
		window, paginated := applyWindow(counts, offset, headLimit)
		truncated := searchTruncated || paginated
		return formattedGrepResult{
			output:       appendTruncatedNote(formatCountOutput(window), truncated, len(window)),
			counts:       countsToMap(window),
			displayCount: len(window),
			truncated:    truncated,
		}
	default:
		files := uniqueFiles(matches)
		window, paginated := applyWindow(files, offset, headLimit)
		truncated := searchTruncated || paginated
		return formattedGrepResult{
			output:       appendTruncatedNote(formatFilesOutput(window), truncated, len(window)),
			files:        window,
			displayCount: len(window),
			truncated:    truncated,
		}
	}
}
