package skills

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cexll/agentsdk-go/pkg/config"
	"gopkg.in/yaml.v3"
)

// fileOps abstracts filesystem operations for testability.
type fileOps struct {
	readFile func(string) ([]byte, error)
	openFile func(string) (fs.File, error)
	statFile func(string) (fs.FileInfo, error)
}

var (
	fileOpOverridesMu sync.RWMutex
	fileOpOverrides   = struct {
		read func(string) ([]byte, error)
		stat func(string) (fs.FileInfo, error)
	}{}
)

func readFileOverrideOrOS(path string) ([]byte, error) {
	fileOpOverridesMu.RLock()
	override := fileOpOverrides.read
	fileOpOverridesMu.RUnlock()
	if override != nil {
		return override(path)
	}
	return os.ReadFile(path)
}

func statFileOverrideOrOS(path string) (fs.FileInfo, error) {
	fileOpOverridesMu.RLock()
	override := fileOpOverrides.stat
	fileOpOverridesMu.RUnlock()
	if override != nil {
		return override(path)
	}
	return os.Stat(path)
}

// LoaderOptions controls how skills are discovered from the filesystem.
type LoaderOptions struct {
	ProjectRoot string
	// Deprecated: user-level scanning has been removed; this field is ignored.
	UserHome string
	// Deprecated: user-level scanning has been removed; this flag is ignored.
	EnableUser bool
	// FS is the filesystem abstraction layer for loading skills.
	// If nil, falls back to os.* functions for backward compatibility.
	FS *config.FS
}

// SkillFile captures an on-disk SKILL.md entry.
type SkillFile struct {
	Name     string
	Path     string
	Metadata SkillMetadata
	fs       *config.FS
}

// readFile is swappable in tests to track filesystem IO.
var readFile = os.ReadFile

// ToolList supports YAML string or sequence, normalizing to a de-duplicated list.
type ToolList []string

func (t *ToolList) UnmarshalYAML(value *yaml.Node) error {
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
	*t = ToolList(deduped)
	return nil
}

// SkillMetadata mirrors the YAML frontmatter fields inside SKILL.md.
type SkillMetadata struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	License       string            `yaml:"license,omitempty"`
	Compatibility string            `yaml:"compatibility,omitempty"`
	Metadata      map[string]string `yaml:"metadata,omitempty"`
	AllowedTools  ToolList          `yaml:"allowed-tools,omitempty"`
}

// SkillRegistration wires a definition to its handler.
type SkillRegistration struct {
	Definition Definition
	Handler    Handler
}

// Skill names must be 1-64 characters, lowercase alphanumeric plus hyphens, and
// cannot start or end with a hyphen.
var skillNameRegexp = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

func isValidSkillName(name string) bool {
	return skillNameRegexp.MatchString(strings.TrimSpace(name))
}

// LoadFromFS loads skills from the filesystem. Errors are aggregated so one
// broken file will not block others. Duplicate names are skipped with a
// warning entry in the error list.
func LoadFromFS(opts LoaderOptions) ([]SkillRegistration, []error) {
	var (
		registrations []SkillRegistration
		errs          []error
		allFiles      []SkillFile
	)

	fsLayer := opts.FS
	if fsLayer == nil {
		fsLayer = config.NewFS(opts.ProjectRoot, nil)
	}

	ops := resolveFileOps(opts.FS)

	projectDir := filepath.Join(opts.ProjectRoot, ".claude", "skills")
	files, loadErrs := loadSkillDir(projectDir, fsLayer)
	errs = append(errs, loadErrs...)
	allFiles = append(allFiles, files...)

	if len(allFiles) == 0 {
		return nil, errs
	}

	sort.Slice(allFiles, func(i, j int) bool {
		if allFiles[i].Metadata.Name != allFiles[j].Metadata.Name {
			return allFiles[i].Metadata.Name < allFiles[j].Metadata.Name
		}
		return allFiles[i].Path < allFiles[j].Path
	})

	seen := map[string]string{}
	for _, file := range allFiles {
		if prev, ok := seen[file.Metadata.Name]; ok {
			errs = append(errs, fmt.Errorf("skills: duplicate skill %q at %s (already from %s)", file.Metadata.Name, file.Path, prev))
			continue
		}
		seen[file.Metadata.Name] = file.Path

		def := Definition{
			Name:        file.Metadata.Name,
			Description: file.Metadata.Description,
			Metadata:    buildDefinitionMetadata(file),
		}
		reg := SkillRegistration{
			Definition: def,
			Handler:    buildHandler(file, ops),
		}
		registrations = append(registrations, reg)
	}

	return registrations, errs
}

func loadSkillDir(root string, fsLayer *config.FS) ([]SkillFile, []error) {
	var (
		results []SkillFile
		errs    []error
	)

	if fsLayer == nil {
		fsLayer = config.NewFS("", nil)
	}

	info, err := fsLayer.Stat(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, []error{fmt.Errorf("skills: stat %s: %w", root, err)}
	}
	if !info.IsDir() {
		return nil, []error{fmt.Errorf("skills: path %s is not a directory", root)}
	}

	entries, err := fsLayer.ReadDir(root)
	if err != nil {
		return nil, []error{fmt.Errorf("skills: read dir %s: %w", root, err)}
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dirName := entry.Name()
		path := filepath.Join(root, dirName, "SKILL.md")
		file, parseErr := parseSkillFile(path, dirName, fsLayer)
		if parseErr != nil {
			if errors.Is(parseErr, fs.ErrNotExist) {
				continue
			}
			errs = append(errs, parseErr)
			continue
		}

		results = append(results, file)
	}
	return results, errs
}

func parseSkillFile(path, dirName string, fsLayer *config.FS) (SkillFile, error) {
	meta, err := readFrontMatter(path, fsLayer)
	if err != nil {
		return SkillFile{}, fmt.Errorf("skills: read %s: %w", path, err)
	}
	if meta.Name != "" && dirName != "" && meta.Name != dirName {
		return SkillFile{}, fmt.Errorf("skills: name %q does not match directory %q in %s", meta.Name, dirName, path)
	}
	if err := validateMetadata(meta); err != nil {
		return SkillFile{}, fmt.Errorf("skills: validate %s: %w", path, err)
	}

	return SkillFile{
		Name:     meta.Name,
		Path:     path,
		Metadata: meta,
		fs:       fsLayer,
	}, nil
}

func readFrontMatter(path string, fsLayer *config.FS) (SkillMetadata, error) {
	var (
		file fs.File
		err  error
	)
	if fsLayer != nil {
		file, err = fsLayer.Open(path)
	} else {
		file, err = os.Open(path)
	}
	if err != nil {
		return SkillMetadata{}, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	first, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return SkillMetadata{}, err
	}

	first = strings.TrimPrefix(first, "\uFEFF")
	if strings.TrimSpace(first) != "---" {
		return SkillMetadata{}, errors.New("missing YAML frontmatter")
	}

	var lines []string
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return SkillMetadata{}, readErr
		}
		if strings.TrimSpace(line) == "---" {
			metaText := strings.Join(lines, "")
			var meta SkillMetadata
			if err := yaml.Unmarshal([]byte(metaText), &meta); err != nil {
				return SkillMetadata{}, fmt.Errorf("decode YAML: %w", err)
			}
			return meta, nil
		}

		if line != "" {
			lines = append(lines, line)
		}

		if errors.Is(readErr, io.EOF) {
			return SkillMetadata{}, errors.New("missing closing frontmatter separator")
		}
	}
}

func parseFrontMatter(content string) (SkillMetadata, string, error) {
	trimmed := strings.TrimPrefix(content, "\uFEFF") // drop BOM if present
	lines := strings.Split(trimmed, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return SkillMetadata{}, "", errors.New("missing YAML frontmatter")
	}

	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return SkillMetadata{}, "", errors.New("missing closing frontmatter separator")
	}

	metaText := strings.Join(lines[1:end], "\n")
	var meta SkillMetadata
	if err := yaml.Unmarshal([]byte(metaText), &meta); err != nil {
		return SkillMetadata{}, "", fmt.Errorf("decode YAML: %w", err)
	}

	body := strings.Join(lines[end+1:], "\n")
	body = strings.TrimPrefix(body, "\n")

	return meta, body, nil
}

func validateMetadata(meta SkillMetadata) error {
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

func loadSupportFiles(dir string) (map[string][]string, []error) {
	return loadSupportFilesWithFS(dir, nil)
}

func loadSupportFilesWithFS(dir string, fsLayer *config.FS) (map[string][]string, []error) {
	out := map[string][]string{}
	var errs []error

	if fsLayer == nil {
		fsLayer = config.NewFS("", nil)
	}

	for _, sub := range []string{"scripts", "references", "assets"} {
		root := filepath.Join(dir, sub)
		info, err := fsLayer.Stat(root)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, fmt.Errorf("skills: stat %s: %w", root, err))
			}
			continue
		}
		if !info.IsDir() {
			errs = append(errs, fmt.Errorf("skills: %s is not a directory", root))
			continue
		}

		var files []string
		if walkErr := fsLayer.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				errs = append(errs, fmt.Errorf("skills: walk %s: %w", path, walkErr))
				return nil
			}
			if d.IsDir() {
				return nil
			}

			rel, err := filepath.Rel(root, path)
			if err != nil {
				rel = d.Name()
			}
			files = append(files, filepath.ToSlash(rel))
			return nil
		}); walkErr != nil {
			errs = append(errs, fmt.Errorf("skills: walk %s: %w", root, walkErr))
			continue
		}

		sort.Strings(files)
		if len(files) > 0 {
			out[sub] = files
		}
	}

	if len(out) == 0 {
		return nil, errs
	}
	return out, errs
}

func buildDefinitionMetadata(file SkillFile) map[string]string {
	var meta map[string]string
	if len(file.Metadata.Metadata) > 0 {
		meta = make(map[string]string, len(file.Metadata.Metadata)+4)
		for k, v := range file.Metadata.Metadata {
			meta[k] = v
		}
	}

	if tools := file.Metadata.AllowedTools; len(tools) > 0 {
		if meta == nil {
			meta = map[string]string{}
		}
		meta["allowed-tools"] = strings.Join(tools, ",")
	}

	if license := strings.TrimSpace(file.Metadata.License); license != "" {
		if meta == nil {
			meta = map[string]string{}
		}
		meta["license"] = license
	}

	if compat := strings.TrimSpace(file.Metadata.Compatibility); compat != "" {
		if meta == nil {
			meta = map[string]string{}
		}
		meta["compatibility"] = compat
	}

	if file.Path != "" {
		if meta == nil {
			meta = map[string]string{}
		}
		meta["source"] = file.Path
	}

	return meta
}

func resolveFileOps(fsLayer *config.FS) fileOps {
	if fsLayer != nil {
		return fileOps{
			readFile: fsLayer.ReadFile,
			openFile: fsLayer.Open,
			statFile: fsLayer.Stat,
		}
	}
	return fileOps{
		readFile: readFileOverrideOrOS,
		openFile: func(path string) (fs.File, error) { return os.Open(path) },
		statFile: statFileOverrideOrOS,
	}
}

func buildHandler(file SkillFile, ops fileOps) Handler {
	return &lazySkillHandler{
		path: file.Path,
		file: file,
		ops:  ops,
	}
}

func loadSkillContent(file SkillFile) (Result, error) {
	body, err := loadSkillBodyFromFS(file.Path, file.fs)
	if err != nil {
		return Result{}, err
	}

	support, supportErrs := loadSupportFilesWithFS(filepath.Dir(file.Path), file.fs)
	if err := errors.Join(supportErrs...); err != nil {
		return Result{}, err
	}

	output := map[string]any{"body": body}
	meta := map[string]any{}

	if len(file.Metadata.AllowedTools) > 0 {
		meta["allowed-tools"] = []string(file.Metadata.AllowedTools)
	}
	meta["source"] = file.Path

	if len(support) > 0 {
		output["support_files"] = support
		count := 0
		for _, files := range support {
			count += len(files)
		}
		meta["support-file-count"] = count
	}

	if len(meta) == 0 {
		meta = nil
	}

	return Result{
		Skill:    file.Metadata.Name,
		Output:   output,
		Metadata: meta,
	}, nil
}

func loadSkillBody(path string) (string, error) {
	return loadSkillBodyFromFS(path, nil)
}

func loadSkillBodyFromFS(path string, fsLayer *config.FS) (string, error) {
	var (
		data []byte
		err  error
	)
	if fsLayer != nil {
		data, err = fsLayer.ReadFile(path)
	} else {
		data, err = readFile(path)
	}
	if err != nil {
		return "", fmt.Errorf("skills: read %s: %w", path, err)
	}
	_, body, err := parseFrontMatter(string(data))
	if err != nil {
		return "", fmt.Errorf("skills: parse %s: %w", path, err)
	}
	return body, nil
}

// SetReadFileForTest swaps the file reader; intended for white-box tests only.
func SetReadFileForTest(fn func(string) ([]byte, error)) (restore func()) {
	prev := readFile
	readFile = fn
	return func() {
		readFile = prev
	}
}

// SetSkillFileOpsForTest swaps filesystem helpers; intended for white-box tests only.
func SetSkillFileOpsForTest(
	read func(string) ([]byte, error),
	stat func(string) (fs.FileInfo, error),
) (restore func()) {
	fileOpOverridesMu.Lock()
	prev := fileOpOverrides
	if read != nil {
		fileOpOverrides.read = read
	}
	if stat != nil {
		fileOpOverrides.stat = stat
	}
	fileOpOverridesMu.Unlock()
	return func() {
		fileOpOverridesMu.Lock()
		fileOpOverrides = prev
		fileOpOverridesMu.Unlock()
	}
}

// lazySkillHandler defers loading the skill body until first execution and
// supports hot-reload by checking file modification time on each access.
type lazySkillHandler struct {
	path string
	file SkillFile
	ops  fileOps

	mu      sync.Mutex
	cached  Result
	loadErr error
	loaded  bool
	modTime time.Time
}

func (h *lazySkillHandler) Execute(_ context.Context, _ ActivationContext) (Result, error) {
	if h == nil {
		return Result{}, errors.New("skills: handler is nil")
	}

	info, err := h.ops.statFile(h.path)
	if err != nil {
		return Result{}, fmt.Errorf("skills: stat %s: %w", h.path, err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.loaded && !info.ModTime().After(h.modTime) {
		if h.loadErr != nil {
			return Result{}, h.loadErr
		}
		return h.cached, nil
	}

	h.cached, h.loadErr = loadSkillContent(h.file)
	h.loaded = true
	h.modTime = info.ModTime()

	if h.loadErr != nil {
		return Result{}, h.loadErr
	}
	return h.cached, nil
}

// BodyLength reports the cached body length without triggering a load. The
// second return value indicates whether a body has been loaded.
func (h *lazySkillHandler) BodyLength() (int, bool) {
	if h == nil {
		return 0, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.loaded {
		return 0, false
	}
	return skillBodyLength(h.cached), true
}

func skillBodyLength(res Result) int {
	if res.Output == nil {
		return 0
	}
	if output, ok := res.Output.(map[string]any); ok {
		if body, ok := output["body"].(string); ok {
			return len(body)
		}
		if raw, ok := output["body"].([]byte); ok {
			return len(raw)
		}
	}
	return 0
}
