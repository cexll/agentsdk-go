package prompts

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/cexll/agentsdk-go/pkg/runtime/skills"
	"gopkg.in/yaml.v3"
)

var skillNameRegexp = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

// skillMetadata mirrors the YAML frontmatter fields inside SKILL.md.
type skillMetadata struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	License       string            `yaml:"license,omitempty"`
	Compatibility string            `yaml:"compatibility,omitempty"`
	Metadata      map[string]string `yaml:"metadata,omitempty"`
	AllowedTools  toolList          `yaml:"allowed-tools,omitempty"`
}

// toolList supports YAML string or sequence, normalizing to a de-duplicated list.
type toolList []string

func (t *toolList) UnmarshalYAML(value *yaml.Node) error {
	if value == nil || value.Tag == "!!null" {
		*t = nil
		return nil
	}

	var tools []string
	switch value.Kind {
	case yaml.ScalarNode:
		for _, entry := range strings.Split(value.Value, ",") {
			tool := strings.TrimSpace(entry)
			if tool != "" {
				tools = append(tools, tool)
			}
		}
	case yaml.SequenceNode:
		for i, entry := range value.Content {
			if entry.Kind != yaml.ScalarNode {
				return fmt.Errorf("allowed-tools[%d]: expected string", i)
			}
			tool := strings.TrimSpace(entry.Value)
			if tool != "" {
				tools = append(tools, tool)
			}
		}
	default:
		return errors.New("allowed-tools: expected string or sequence")
	}

	seen := map[string]struct{}{}
	deduped := tools[:0]
	for _, tool := range tools {
		if _, ok := seen[tool]; ok {
			continue
		}
		seen[tool] = struct{}{}
		deduped = append(deduped, tool)
	}

	if len(deduped) == 0 {
		*t = nil
		return nil
	}
	*t = toolList(deduped)
	return nil
}

// parseSkills parses all skills from the given directory in the filesystem.
func parseSkills(fsys fs.FS, dir string, validate bool) ([]SkillRegistration, []error) {
	var (
		regs []SkillRegistration
		errs []error
	)

	info, err := fs.Stat(fsys, dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, []error{fmt.Errorf("prompts: stat skills dir %s: %w", dir, err)}
	}
	if !info.IsDir() {
		return nil, []error{fmt.Errorf("prompts: skills path %s is not a directory", dir)}
	}

	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, []error{fmt.Errorf("prompts: read skills dir %s: %w", dir, err)}
	}

	type skillFile struct {
		name string
		path string
		meta skillMetadata
		body string
	}
	var files []skillFile

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dirName := entry.Name()
		skillPath := path.Join(dir, dirName, "SKILL.md")
		content, err := fs.ReadFile(fsys, skillPath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			errs = append(errs, fmt.Errorf("prompts: read skill %s: %w", skillPath, err))
			continue
		}

		meta, body, err := parseSkillFrontMatter(string(content))
		if err != nil {
			errs = append(errs, fmt.Errorf("prompts: parse skill %s: %w", skillPath, err))
			continue
		}

		if meta.Name != "" && dirName != "" && meta.Name != dirName {
			errs = append(errs, fmt.Errorf("prompts: skill name %q does not match directory %q in %s", meta.Name, dirName, skillPath))
			continue
		}

		if validate {
			if err := validateSkillMetadata(meta); err != nil {
				errs = append(errs, fmt.Errorf("prompts: validate skill %s: %w", skillPath, err))
				continue
			}
		}

		files = append(files, skillFile{
			name: meta.Name,
			path: skillPath,
			meta: meta,
			body: body,
		})
	}

	if len(files) == 0 {
		return nil, errs
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].meta.Name != files[j].meta.Name {
			return files[i].meta.Name < files[j].meta.Name
		}
		return files[i].path < files[j].path
	})

	seen := map[string]string{}
	for _, file := range files {
		if prev, ok := seen[file.meta.Name]; ok {
			errs = append(errs, fmt.Errorf("prompts: duplicate skill %q at %s (already from %s)", file.meta.Name, file.path, prev))
			continue
		}
		seen[file.meta.Name] = file.path

		def := skills.Definition{
			Name:        file.meta.Name,
			Description: file.meta.Description,
			Metadata:    buildSkillDefinitionMetadata(file.meta, file.path),
		}

		body := file.body
		meta := file.meta
		skillPath := file.path
		handler := skills.HandlerFunc(func(_ context.Context, _ skills.ActivationContext) (skills.Result, error) {
			output := map[string]any{"body": body}
			resultMeta := map[string]any{"source": skillPath}
			if len(meta.AllowedTools) > 0 {
				resultMeta["allowed-tools"] = []string(meta.AllowedTools)
			}
			return skills.Result{
				Skill:    meta.Name,
				Output:   output,
				Metadata: resultMeta,
			}, nil
		})

		regs = append(regs, SkillRegistration{
			Definition: def,
			Handler:    handler,
		})
	}

	return regs, errs
}

func parseSkillFrontMatter(content string) (skillMetadata, string, error) {
	trimmed := strings.TrimPrefix(content, "\uFEFF")
	lines := strings.Split(trimmed, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return skillMetadata{}, "", errors.New("missing YAML frontmatter")
	}

	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return skillMetadata{}, "", errors.New("missing closing frontmatter separator")
	}

	metaText := strings.Join(lines[1:end], "\n")
	var meta skillMetadata
	if err := yaml.Unmarshal([]byte(metaText), &meta); err != nil {
		return skillMetadata{}, "", fmt.Errorf("decode YAML: %w", err)
	}

	body := strings.Join(lines[end+1:], "\n")
	body = strings.TrimPrefix(body, "\n")

	return meta, body, nil
}

func validateSkillMetadata(meta skillMetadata) error {
	name := strings.TrimSpace(meta.Name)
	if name == "" {
		return errors.New("name is required")
	}
	if !skillNameRegexp.MatchString(name) {
		return fmt.Errorf("invalid name %q", meta.Name)
	}
	desc := strings.TrimSpace(meta.Description)
	if desc == "" {
		return errors.New("description is required")
	}
	if len(desc) > 1024 {
		return errors.New("description exceeds 1024 characters")
	}
	compat := strings.TrimSpace(meta.Compatibility)
	if len(compat) > 500 {
		return errors.New("compatibility exceeds 500 characters")
	}
	return nil
}

func buildSkillDefinitionMetadata(meta skillMetadata, path string) map[string]string {
	var out map[string]string
	if len(meta.Metadata) > 0 {
		out = make(map[string]string, len(meta.Metadata)+4)
		for k, v := range meta.Metadata {
			out[k] = v
		}
	}

	if tools := meta.AllowedTools; len(tools) > 0 {
		if out == nil {
			out = map[string]string{}
		}
		out["allowed-tools"] = strings.Join(tools, ",")
	}

	if license := strings.TrimSpace(meta.License); license != "" {
		if out == nil {
			out = map[string]string{}
		}
		out["license"] = license
	}

	if compat := strings.TrimSpace(meta.Compatibility); compat != "" {
		if out == nil {
			out = map[string]string{}
		}
		out["compatibility"] = compat
	}

	if path != "" {
		if out == nil {
			out = map[string]string{}
		}
		out["source"] = path
	}

	return out
}
