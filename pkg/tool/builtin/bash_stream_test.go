package toolbuiltin

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/middleware"
	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/runtime/commands"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

func TestBashToolStreamExecuteEmitsIncrementally(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	script := writeScript(t, dir, "stream.sh", "#!/bin/sh\nsleep 0.1\necho first\nsleep 0.2\necho second 1>&2\n")

	tool := NewBashToolWithRoot(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	type chunk struct {
		text   string
		stderr bool
	}
	chunks := make(chan chunk, 4)

	done := make(chan struct{})
	var resultOutput string
	var execErr error
	go func() {
		res, err := tool.StreamExecute(ctx, map[string]any{
			"command": "./" + filepath.Base(script),
			"workdir": dir,
		}, func(text string, isStderr bool) {
			chunks <- chunk{text: text, stderr: isStderr}
		})
		if res != nil {
			resultOutput = strings.TrimSpace(res.Output)
		}
		execErr = err
		close(done)
	}()

	var first chunk
	select {
	case first = <-chunks:
	case <-time.After(3 * time.Second):
		t.Fatalf("did not receive streaming chunk before timeout")
	}
	if first.text != "first" || first.stderr {
		t.Fatalf("unexpected first chunk %+v", first)
	}

	<-done
	if execErr != nil {
		t.Fatalf("StreamExecute returned error: %v", execErr)
	}

	drained := []chunk{first}
	for len(chunks) > 0 {
		drained = append(drained, <-chunks)
	}
	if len(drained) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(drained))
	}
	if drained[1].text != "second" || !drained[1].stderr {
		t.Fatalf("unexpected second chunk %+v", drained[1])
	}
	if resultOutput != "first\nsecond" {
		t.Fatalf("unexpected final output %q", resultOutput)
	}
}

func TestBashToolStreamExecuteRespectsContextCancel(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	tool := NewBashToolWithRoot(dir)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	_, err := tool.StreamExecute(ctx, map[string]any{
		"command": "sleep 2",
		"workdir": dir,
	}, nil)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "timeout") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func TestBashToolStreamExecuteOutputLimit(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	tool := NewBashToolWithRoot(dir)

	res, err := tool.StreamExecute(context.Background(), map[string]any{
		"command": "printf '%.0sA' {1..40000}",
		"workdir": dir,
	}, nil)
	if err != nil {
		t.Fatalf("StreamExecute failed: %v", err)
	}

	path := extractSavedPath(res.Output)
	if path == "" {
		t.Fatalf("expected output file reference, got %q", res.Output)
	}
	if !strings.Contains(path, filepath.Join(string(filepath.Separator), "tmp", "agentsdk", "bash-output", "default")) {
		t.Fatalf("expected path to include default session directory, got %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if len(data) != 40000 {
		t.Fatalf("expected output file length 40000 got %d", len(data))
	}
	for i, b := range data {
		if b != 'A' {
			t.Fatalf("unexpected byte at %d: %q", i, b)
		}
	}
}

func TestBashToolExecuteSpoolsLargeOutputToDisk(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	tool := NewBashToolWithRoot(dir)

	res, err := tool.Execute(context.Background(), map[string]any{
		"command": "printf '%.0sA' {1..40000}",
		"workdir": dir,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	path := extractSavedPath(res.Output)
	if path == "" {
		t.Fatalf("expected output file reference, got %q", res.Output)
	}
	if !strings.Contains(path, filepath.Join(string(filepath.Separator), "tmp", "agentsdk", "bash-output", "default")) {
		t.Fatalf("expected path to include default session directory, got %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if len(data) != 40000 {
		t.Fatalf("expected output file length 40000 got %d", len(data))
	}
}

func TestBashToolExecuteSpoolPathIncludesSessionID(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	tool := NewBashToolWithRoot(dir)

	ctx := context.WithValue(context.Background(), model.MiddlewareStateKey, &middleware.State{
		Values: map[string]any{"session_id": "sess-42"},
	})

	res, err := tool.Execute(ctx, map[string]any{
		"command": "printf '%.0sA' {1..40000}",
		"workdir": dir,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	path := extractSavedPath(res.Output)
	if path == "" {
		t.Fatalf("expected output file reference, got %q", res.Output)
	}
	if !strings.Contains(path, filepath.Join(string(filepath.Separator), "tmp", "agentsdk", "bash-output", "sess-42")) {
		t.Fatalf("expected path to include session directory, got %q", path)
	}
}

func TestBashToolExecuteDoesNotSpoolAtThreshold(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	tool := NewBashToolWithRoot(dir)

	res, err := tool.Execute(context.Background(), map[string]any{
		"command": "printf '%.0sA' {1.." + strconv.Itoa(maxBashOutputLen) + "}",
		"workdir": dir,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if got := extractSavedPath(res.Output); got != "" {
		t.Fatalf("expected inline output at threshold, got %q", res.Output)
	}
	if len(res.Output) != maxBashOutputLen {
		t.Fatalf("expected output length %d got %d", maxBashOutputLen, len(res.Output))
	}
}

func TestBashToolExecuteSpoolsLargeStderrToDisk(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	tool := NewBashToolWithRoot(dir)
	script := writeScript(t, dir, "stderr-large.sh", "#!/bin/bash\necho out\nprintf '%.0sE' {1..40000} 1>&2\n")

	res, err := tool.Execute(context.Background(), map[string]any{
		"command": "./" + filepath.Base(script),
		"workdir": dir,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	path := extractSavedPath(res.Output)
	if path == "" {
		t.Fatalf("expected output file reference, got %q", res.Output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if len(data) != 40004 {
		t.Fatalf("expected output file length 40004 got %d", len(data))
	}
	if string(data[:4]) != "out\n" {
		t.Fatalf("unexpected prefix %q", string(data[:4]))
	}
	for i, b := range data[4:] {
		if b != 'E' {
			t.Fatalf("unexpected byte at %d: %q", i+4, b)
		}
	}
}

func TestBashToolExecuteSpoolsStdoutAndAppendsStderr(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	tool := NewBashToolWithRoot(dir)
	script := writeScript(t, dir, "stdout-large.sh", "#!/bin/bash\nprintf '%.0sA' {1..40000}\necho err 1>&2\n")

	res, err := tool.Execute(context.Background(), map[string]any{
		"command": "./" + filepath.Base(script),
		"workdir": dir,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	path := extractSavedPath(res.Output)
	if path == "" {
		t.Fatalf("expected output file reference, got %q", res.Output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if len(data) != 40004 {
		t.Fatalf("expected output file length 40004 got %d", len(data))
	}
	for i, b := range data[:40000] {
		if b != 'A' {
			t.Fatalf("unexpected byte at %d: %q", i, b)
		}
	}
	if string(data[40000:]) != "\nerr" {
		t.Fatalf("unexpected suffix %q", string(data[40000:]))
	}
}

func TestBashToolExecuteSpoolsWhenCombinedOutputExceedsThreshold(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	tool := NewBashToolWithRoot(dir)
	script := writeScript(t, dir, "combined-large.sh", "#!/bin/bash\nprintf '%.0sA' {1..20000}\nprintf '%.0sB' {1..20000} 1>&2\n")

	res, err := tool.Execute(context.Background(), map[string]any{
		"command": "./" + filepath.Base(script),
		"workdir": dir,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	path := extractSavedPath(res.Output)
	if path == "" {
		t.Fatalf("expected output file reference, got %q", res.Output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if len(data) != 40001 {
		t.Fatalf("expected output file length 40001 got %d", len(data))
	}
	for i, b := range data[:20000] {
		if b != 'A' {
			t.Fatalf("unexpected byte at %d: %q", i, b)
		}
	}
	if data[20000] != '\n' {
		t.Fatalf("expected separator newline at %d got %q", 20000, data[20000])
	}
	for i, b := range data[20001:] {
		if b != 'B' {
			t.Fatalf("unexpected byte at %d: %q", i+20001, b)
		}
	}
}

func TestBashEnsureBashOutputDirRejectsEmpty(t *testing.T) {
	if err := ensureBashOutputDir(" "); err == nil {
		t.Fatalf("expected error for empty directory")
	}
}

func TestBashBuildSlashCommandDescriptionDefaults(t *testing.T) {
	desc := buildSlashCommandDescription([]commands.Definition{
		{Name: "   ", Description: "   "},
		{Name: "foo", Description: ""},
	})
	if !strings.Contains(desc, "/unnamed") || !strings.Contains(desc, "No description provided.") {
		t.Fatalf("unexpected description: %q", desc)
	}
}

func TestBashHostWithoutPortBranches(t *testing.T) {
	if got := hostWithoutPort("example.com:80"); got != "example.com" {
		t.Fatalf("expected host example.com got %q", got)
	}
	if got := hostWithoutPort("example.com:80:90"); got != "example.com:80:90" {
		t.Fatalf("expected host to remain unchanged, got %q", got)
	}
}

func TestBashTrimRightNewlinesInFileNil(t *testing.T) {
	n, err := trimRightNewlinesInFile(nil)
	if err != nil || n != 0 {
		t.Fatalf("expected 0,nil got %d,%v", n, err)
	}
}

func TestBashSessionIDUsesTraceValueFromState(t *testing.T) {
	ctx := context.WithValue(context.Background(), model.MiddlewareStateKey, &middleware.State{
		Values: map[string]any{"trace.session_id": " trace-1 "},
	})
	if got := bashSessionID(ctx); got != "trace-1" {
		t.Fatalf("expected trace-1 got %q", got)
	}
}

func TestBashSessionIDUsesContextFallbackKey(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TraceSessionIDContextKey, "ctx-1")
	if got := bashSessionID(ctx); got != "ctx-1" {
		t.Fatalf("expected ctx-1 got %q", got)
	}
}

func TestBashSanitizePathComponentFallsBack(t *testing.T) {
	if got := sanitizePathComponent("---"); got != "default" {
		t.Fatalf("expected default fallback got %q", got)
	}
}

func TestBashOutputSpoolFinalizeNilReceiver(t *testing.T) {
	var spool *bashOutputSpool
	out, path, err := spool.Finalize()
	if out != "" || path != "" || err != nil {
		t.Fatalf("expected empty finalize result, got out=%q path=%q err=%v", out, path, err)
	}
}

func TestBashOutputSpoolFinalizeWhenTruncatedReturnsCombinedOutput(t *testing.T) {
	stdout := tool.NewSpoolWriter(4, nil)
	stderr := tool.NewSpoolWriter(100, nil)
	_, _ = stdout.Write([]byte("out\n"))
	_, _ = stdout.Write([]byte("x"))
	_, _ = stderr.Write([]byte("err\n"))

	spool := &bashOutputSpool{
		threshold:  100,
		outputPath: "ignored",
		stdout:     stdout,
		stderr:     stderr,
	}

	out, file, err := spool.Finalize()
	if err == nil {
		t.Fatalf("expected Finalize to surface truncation error")
	}
	if file != "" {
		t.Fatalf("expected no file, got %q", file)
	}
	if out != "out\nerr" {
		t.Fatalf("unexpected output %q", out)
	}
}

func TestBashEscapeXMLHandlesEmptyString(t *testing.T) {
	if got := escapeXML(""); got != "" {
		t.Fatalf("expected empty string got %q", got)
	}
}

func TestBashShellHandleAppendNilReturnsError(t *testing.T) {
	var h *ShellHandle
	if err := h.Append(ShellStreamStdout, "x"); err == nil {
		t.Fatalf("expected error on nil shell handle")
	}
}

func TestBashFileSandboxReadFileRejectsDirectory(t *testing.T) {
	dir := cleanTempDir(t)
	sb := newFileSandbox(dir)

	if _, err := sb.readFile(dir); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("expected directory rejection, got %v", err)
	}
}

func TestBashFileSandboxReadFileHonorsMaxBytes(t *testing.T) {
	dir := cleanTempDir(t)
	sb := newFileSandbox(dir)
	sb.maxBytes = 5

	path := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(path, []byte("123456"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := sb.readFile(path); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected max bytes rejection, got %v", err)
	}
}

func extractSavedPath(output string) string {
	const prefix = "[Output saved to: "
	const suffix = "]"
	if !strings.HasPrefix(output, prefix) || !strings.HasSuffix(output, suffix) {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(output, prefix), suffix)
}

func TestBashCoverageHarness(t *testing.T) {
	run := flag.Lookup("test.run")
	if run == nil || run.Value.String() == "" || !strings.Contains(run.Value.String(), "TestBash") {
		t.Skip("coverage harness only runs when -run includes TestBash")
	}

	t.Run("TestAppendTruncatedNoteBranches", TestAppendTruncatedNoteBranches)
	t.Run("TestApplyWindowBoundaries", TestApplyWindowBoundaries)
	t.Run("TestAskUserQuestionAcceptsTypedArraysAndAnswers", TestAskUserQuestionAcceptsTypedArraysAndAnswers)
	t.Run("TestAskUserQuestionConcurrentExecutions", TestAskUserQuestionConcurrentExecutions)
	t.Run("TestAskUserQuestionErrors", TestAskUserQuestionErrors)
	t.Run("TestAskUserQuestionMetadata", TestAskUserQuestionMetadata)
	t.Run("TestAskUserQuestionMultiSelect", TestAskUserQuestionMultiSelect)
	t.Run("TestAskUserQuestionMultipleQuestions", TestAskUserQuestionMultipleQuestions)
	t.Run("TestAskUserQuestionSingleQuestionSingleSelect", TestAskUserQuestionSingleQuestionSingleSelect)
	t.Run("TestAsyncTaskManagerConcurrentStarts", TestAsyncTaskManagerConcurrentStarts)
	t.Run("TestAsyncTaskManagerContextCancellation", TestAsyncTaskManagerContextCancellation)
	t.Run("TestAsyncTaskManagerKillStopsTask", TestAsyncTaskManagerKillStopsTask)
	t.Run("TestAsyncTaskManagerStartGetOutputAndList", TestAsyncTaskManagerStartGetOutputAndList)
	t.Run("TestAsyncTaskManagerTaskLimit", TestAsyncTaskManagerTaskLimit)
	t.Run("TestBuildSkillDescriptionEscapesAndDefaults", TestBuildSkillDescriptionEscapesAndDefaults)
	t.Run("TestCleanResultURL", TestCleanResultURL)
	t.Run("TestCoerceString", TestCoerceString)
	t.Run("TestCollectNodeText", TestCollectNodeText)
	t.Run("TestCombineOutput", TestCombineOutput)
	t.Run("TestCustomSandboxConstructors", TestCustomSandboxConstructors)
	t.Run("TestDeduplicateResults", TestDeduplicateResults)
	t.Run("TestDurationFromParam", TestDurationFromParam)
	t.Run("TestDurationFromParamEmptyString", TestDurationFromParamEmptyString)
	t.Run("TestDurationFromParamHelpers", TestDurationFromParamHelpers)
	t.Run("TestDurationFromParamNegativeDuration", TestDurationFromParamNegativeDuration)
	t.Run("TestEditToolHelperErrors", TestEditToolHelperErrors)
	t.Run("TestEditToolMetadataAndBoolParser", TestEditToolMetadataAndBoolParser)
	t.Run("TestEditToolReplaceAll", TestEditToolReplaceAll)
	t.Run("TestEditToolSandboxAndContext", TestEditToolSandboxAndContext)
	t.Run("TestEditToolSingleReplacement", TestEditToolSingleReplacement)
	t.Run("TestEditToolUniquenessAndSizeChecks", TestEditToolUniquenessAndSizeChecks)
	t.Run("TestEditToolValidationErrors", TestEditToolValidationErrors)
	t.Run("TestExtractCommand", TestExtractCommand)
	t.Run("TestExtractHostHelper", TestExtractHostHelper)
	t.Run("TestExtractNonEmptyStringValidation", TestExtractNonEmptyStringValidation)
	t.Run("TestExtractURLValidation", TestExtractURLValidation)
	t.Run("TestFetchCacheMissAndNilSet", TestFetchCacheMissAndNilSet)
	t.Run("TestFetchCacheStoresAndExpires", TestFetchCacheStoresAndExpires)
	t.Run("TestFormatCountOutputBoundaries", TestFormatCountOutputBoundaries)
	t.Run("TestFormatSearchOutput", TestFormatSearchOutput)
	t.Run("TestFormatSkillOutputVariants", TestFormatSkillOutputVariants)
	t.Run("TestGlobAndGrepMetadata", TestGlobAndGrepMetadata)
	t.Run("TestGlobHelpers", TestGlobHelpers)
	t.Run("TestGlobToolContextCancellation", TestGlobToolContextCancellation)
	t.Run("TestGlobToolListsMatches", TestGlobToolListsMatches)
	t.Run("TestGlobToolRejectsEscapePatterns", TestGlobToolRejectsEscapePatterns)
	t.Run("TestGlobToolTruncatesResults", TestGlobToolTruncatesResults)
	t.Run("TestGrepCaseInsensitive", TestGrepCaseInsensitive)
	t.Run("TestGrepContextA", TestGrepContextA)
	t.Run("TestGrepContextB", TestGrepContextB)
	t.Run("TestGrepContextC", TestGrepContextC)
	t.Run("TestGrepContextPrecedence", TestGrepContextPrecedence)
	t.Run("TestGrepGlobAndType", TestGrepGlobAndType)
	t.Run("TestGrepGlobFilter", TestGrepGlobFilter)
	t.Run("TestGrepHeadLimit", TestGrepHeadLimit)
	t.Run("TestGrepHeadLimitAndOffset", TestGrepHeadLimitAndOffset)
	t.Run("TestGrepLineNumbers", TestGrepLineNumbers)
	t.Run("TestGrepMultiline", TestGrepMultiline)
	t.Run("TestGrepOffset", TestGrepOffset)
	t.Run("TestGrepOutputModeContent", TestGrepOutputModeContent)
	t.Run("TestGrepOutputModeCount", TestGrepOutputModeCount)
	t.Run("TestGrepOutputModeFiles", TestGrepOutputModeFiles)
	t.Run("TestGrepOutputModeInvalid", TestGrepOutputModeInvalid)
	t.Run("TestGrepSearchDirectoryHonorsDepthLimit", TestGrepSearchDirectoryHonorsDepthLimit)
	t.Run("TestGrepSearchDirectoryRespectsCancellation", TestGrepSearchDirectoryRespectsCancellation)
	t.Run("TestGrepSearchDirectorySkipsSymlinkDirs", TestGrepSearchDirectorySkipsSymlinkDirs)
	t.Run("TestGrepToolContextLinesValidation", TestGrepToolContextLinesValidation)
	t.Run("TestGrepToolRejectsInvalidRegex", TestGrepToolRejectsInvalidRegex)
	t.Run("TestGrepToolRejectsTraversalInPath", TestGrepToolRejectsTraversalInPath)
	t.Run("TestGrepToolSearchDirectory", TestGrepToolSearchDirectory)
	t.Run("TestGrepToolSearchesFile", TestGrepToolSearchesFile)
	t.Run("TestGrepTypeFilter", TestGrepTypeFilter)
	t.Run("TestHostRedirectErrorString", TestHostRedirectErrorString)
	t.Run("TestHostValidatorAllowsPrivate", TestHostValidatorAllowsPrivate)
	t.Run("TestHostValidatorWhitelist", TestHostValidatorWhitelist)
	t.Run("TestHtmlToMarkdownAdvanced", TestHtmlToMarkdownAdvanced)
	t.Run("TestHtmlToMarkdownFallback", TestHtmlToMarkdownFallback)
	t.Run("TestHtmlToMarkdownInlineElements", TestHtmlToMarkdownInlineElements)
	t.Run("TestHtmlToMarkdownParseError", TestHtmlToMarkdownParseError)
	t.Run("TestIntFromInt64Boundaries", TestIntFromInt64Boundaries)
	t.Run("TestIntFromParamAdditionalTypes", TestIntFromParamAdditionalTypes)
	t.Run("TestIntFromParamVariants", TestIntFromParamVariants)
	t.Run("TestIntFromUint64Bounds", TestIntFromUint64Bounds)
	t.Run("TestKillTaskToolCancelledContextReturnsError", TestKillTaskToolCancelledContextReturnsError)
	t.Run("TestKillTaskToolErrorsOnMissingTask", TestKillTaskToolErrorsOnMissingTask)
	t.Run("TestKillTaskToolKillsRunningTask", TestKillTaskToolKillsRunningTask)
	t.Run("TestKillTaskToolMetadata", TestKillTaskToolMetadata)
	t.Run("TestKillTaskToolNilContextHandling", TestKillTaskToolNilContextHandling)
	t.Run("TestKillTaskToolTaskIDValidation", TestKillTaskToolTaskIDValidation)
	t.Run("TestNewGrepToolWithSandbox", TestNewGrepToolWithSandbox)
	t.Run("TestNilContextExecutions", TestNilContextExecutions)
	t.Run("TestNodeHasClass", TestNodeHasClass)
	t.Run("TestNormaliseDomainsHelper", TestNormaliseDomainsHelper)
	t.Run("TestOptionalAsyncTaskID", TestOptionalAsyncTaskID)
	t.Run("TestParseAsyncFlag", TestParseAsyncFlag)
	t.Run("TestParseBoolParam", TestParseBoolParam)
	t.Run("TestParseContextLinesBoundaries", TestParseContextLinesBoundaries)
	t.Run("TestParseContextParams", TestParseContextParams)
	t.Run("TestParseFileTypeFilter", TestParseFileTypeFilter)
	t.Run("TestParseGlobFilter", TestParseGlobFilter)
	t.Run("TestParseGlobPatternErrors", TestParseGlobPatternErrors)
	t.Run("TestParseGrepPatternErrors", TestParseGrepPatternErrors)
	t.Run("TestParseGrepPatternWhitespace", TestParseGrepPatternWhitespace)
	t.Run("TestParseHeadLimit", TestParseHeadLimit)
	t.Run("TestParseOffset", TestParseOffset)
	t.Run("TestParseOutputMode", TestParseOutputMode)
	t.Run("TestParseTaskParamsModelAliases", TestParseTaskParamsModelAliases)
	t.Run("TestReadBoundedError", TestReadBoundedError)
	t.Run("TestReadToolCatFormatting", TestReadToolCatFormatting)
	t.Run("TestReadToolMetadataAndHelpers", TestReadToolMetadataAndHelpers)
	t.Run("TestReadToolOffsetLimitAndTruncation", TestReadToolOffsetLimitAndTruncation)
	t.Run("TestReadToolOffsetOutOfRange", TestReadToolOffsetOutOfRange)
	t.Run("TestReadToolValidationErrors", TestReadToolValidationErrors)
	t.Run("TestRedirectPolicyLimits", TestRedirectPolicyLimits)
	t.Run("TestRelativeDepth", TestRelativeDepth)
	t.Run("TestRelativeDepthOutsideReturnsZero", TestRelativeDepthOutsideReturnsZero)
	t.Run("TestResolveRoot", TestResolveRoot)
	t.Run("TestResolveSearchPathErrors", TestResolveSearchPathErrors)
	t.Run("TestResolveTypeGlobsMappings", TestResolveTypeGlobsMappings)
	t.Run("TestSearchDirectoryCancelled", TestSearchDirectoryCancelled)
	t.Run("TestSearchDirectoryMissingRoot", TestSearchDirectoryMissingRoot)
	t.Run("TestSearchFileEmptyFile", TestSearchFileEmptyFile)
	t.Run("TestSearchFileReadFailures", TestSearchFileReadFailures)
	t.Run("TestSearchFileTruncatesAtLimit", TestSearchFileTruncatesAtLimit)
	t.Run("TestSecondsToDuration", TestSecondsToDuration)
	t.Run("TestShellHandleCloseAndFail", TestShellHandleCloseAndFail)
	t.Run("TestShellStoreAppendAfterClose", TestShellStoreAppendAfterClose)
	t.Run("TestShellStoreConcurrentAppend", TestShellStoreConcurrentAppend)
	t.Run("TestShellStoreDuplicateRegister", TestShellStoreDuplicateRegister)
	t.Run("TestShellStoreFail", TestShellStoreFail)
	t.Run("TestSkillToolDefaultActivationProviderUsesContext", TestSkillToolDefaultActivationProviderUsesContext)
	t.Run("TestSkillToolExecutesSkill", TestSkillToolExecutesSkill)
	t.Run("TestSkillToolMetadataAndContextHelpers", TestSkillToolMetadataAndContextHelpers)
	t.Run("TestSkillToolUnknownSkill", TestSkillToolUnknownSkill)
	t.Run("TestSkillToolValidatesInput", TestSkillToolValidatesInput)
	t.Run("TestSlashCommandExecutes", TestSlashCommandExecutes)
	t.Run("TestSlashCommandExecutorErrors", TestSlashCommandExecutorErrors)
	t.Run("TestSlashCommandMetadataAndFormatting", TestSlashCommandMetadataAndFormatting)
	t.Run("TestSlashCommandMultiple", TestSlashCommandMultiple)
	t.Run("TestSlashCommandRejectsInvalidInput", TestSlashCommandRejectsInvalidInput)
	t.Run("TestSplitGrepLinesEdges", TestSplitGrepLinesEdges)
	t.Run("TestSplitLinesHandlesWindowsNewlines", TestSplitLinesHandlesWindowsNewlines)
	t.Run("TestStringValueBytes", TestStringValueBytes)
	t.Run("TestStringValueCoercion", TestStringValueCoercion)
	t.Run("TestTaskSchemaEnumerationsStayInSync", TestTaskSchemaEnumerationsStayInSync)
	t.Run("TestTaskToolExecuteContextCanceled", TestTaskToolExecuteContextCanceled)
	t.Run("TestTaskToolExecuteRequiresRunner", TestTaskToolExecuteRequiresRunner)
	t.Run("TestTaskToolExecuteSuccess", TestTaskToolExecuteSuccess)
	t.Run("TestTaskToolExecuteValidation", TestTaskToolExecuteValidation)
	t.Run("TestTaskToolMetadata", TestTaskToolMetadata)
	t.Run("TestTodoWriteAcceptsTypedArray", TestTodoWriteAcceptsTypedArray)
	t.Run("TestTodoWriteAllowsClearing", TestTodoWriteAllowsClearing)
	t.Run("TestTodoWriteConcurrentExecutions", TestTodoWriteConcurrentExecutions)
	t.Run("TestTodoWriteMetadata", TestTodoWriteMetadata)
	t.Run("TestTodoWriteMissingTodos", TestTodoWriteMissingTodos)
	t.Run("TestTodoWriteRejectsEmptyContent", TestTodoWriteRejectsEmptyContent)
	t.Run("TestTodoWriteRejectsInvalidStatus", TestTodoWriteRejectsInvalidStatus)
	t.Run("TestTodoWriteRejectsNonArray", TestTodoWriteRejectsNonArray)
	t.Run("TestTodoWriteUpdatesState", TestTodoWriteUpdatesState)
	t.Run("TestUniqueFilesAndCountsEmpty", TestUniqueFilesAndCountsEmpty)
	t.Run("TestWebFetchConvertsHTMLAndCaches", TestWebFetchConvertsHTMLAndCaches)
	t.Run("TestWebFetchExecuteValidations", TestWebFetchExecuteValidations)
	t.Run("TestWebFetchMetadataAndHelpers", TestWebFetchMetadataAndHelpers)
	t.Run("TestWebFetchNormaliseURL", TestWebFetchNormaliseURL)
	t.Run("TestWebFetchRejectsBlockedHosts", TestWebFetchRejectsBlockedHosts)
	t.Run("TestWebFetchRejectsLargeResponse", TestWebFetchRejectsLargeResponse)
	t.Run("TestWebFetchReturnsRedirectNotice", TestWebFetchReturnsRedirectNotice)
	t.Run("TestWebFetchTimeout", TestWebFetchTimeout)
	t.Run("TestWebSearchBlockedDomain", TestWebSearchBlockedDomain)
	t.Run("TestWebSearchDomainListValidation", TestWebSearchDomainListValidation)
	t.Run("TestWebSearchExecuteValidation", TestWebSearchExecuteValidation)
	t.Run("TestWebSearchFallbackURL", TestWebSearchFallbackURL)
	t.Run("TestWebSearchFiltersDomains", TestWebSearchFiltersDomains)
	t.Run("TestWebSearchFormatsEmptyResults", TestWebSearchFormatsEmptyResults)
	t.Run("TestWebSearchHTTPError", TestWebSearchHTTPError)
	t.Run("TestWebSearchMetadata", TestWebSearchMetadata)
	t.Run("TestWebSearchSendsPOSTForm", TestWebSearchSendsPOSTForm)
	t.Run("TestWebSearchShortQuery", TestWebSearchShortQuery)
	t.Run("TestWebSearchTimeout", TestWebSearchTimeout)
	t.Run("TestWriteToolCreatesFile", TestWriteToolCreatesFile)
	t.Run("TestWriteToolHelperErrors", TestWriteToolHelperErrors)
	t.Run("TestWriteToolMetadata", TestWriteToolMetadata)
	t.Run("TestWriteToolSizeLimitAndCancellation", TestWriteToolSizeLimitAndCancellation)
	t.Run("TestWriteToolValidationErrors", TestWriteToolValidationErrors)
}
