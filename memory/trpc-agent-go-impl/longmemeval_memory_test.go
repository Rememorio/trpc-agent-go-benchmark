//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	embeddingopenai "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	memorymem0 "trpc.group/trpc-go/trpc-agent-go/memory/mem0"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type lmeExtractorStub struct {
	extractor.MemoryExtractor
	ops []*extractor.Operation
}

type lmeStagedExtractorStub struct {
	*lmeExtractorStub
	assistantOps []*extractor.Operation
}

type lmePairProvenanceBackend struct {
	ingested int
}

func (b *lmePairProvenanceBackend) Name() string { return "pair-provenance" }

func (b *lmePairProvenanceBackend) IngestPair(
	context.Context,
	*session.Session,
	ingestMeta,
) (*extractionTrace, error) {
	b.ingested++
	return nil, nil
}

func (b *lmePairProvenanceBackend) Flush(context.Context) error { return nil }

func (b *lmePairProvenanceBackend) Search(
	context.Context,
	memory.UserKey,
	string,
	int,
) ([]memoryHit, error) {
	return []memoryHit{{ID: "memory-1", Memory: "unrelated second-pair fact"}}, nil
}

func (b *lmePairProvenanceBackend) Read(
	context.Context,
	memory.UserKey,
) ([]memorySnapshot, bool, error) {
	if b.ingested < 2 {
		return nil, false, nil
	}
	return []memorySnapshot{{
		ID:     "memory-1",
		Memory: "unrelated second-pair fact",
	}}, false, nil
}

func (b *lmePairProvenanceBackend) SnapshotProviderUsage() lmeProviderUsage {
	return lmeProviderUsage{}
}

func (b *lmePairProvenanceBackend) Close() error { return nil }

func (s *lmeStagedExtractorStub) ExtractOperationStages(
	context.Context,
	[]model.Message,
	[]*memory.Entry,
) ([]*extractor.Operation, []*extractor.Operation, error) {
	return s.ops, s.assistantOps, nil
}

func (s *lmeExtractorStub) Extract(
	context.Context,
	[]model.Message,
	[]*memory.Entry,
) ([]*extractor.Operation, error) {
	return s.ops, nil
}

func TestLongMemEvalBlindProgressRedactsOutcomes(t *testing.T) {
	t.Parallel()
	inst := &lmeInstance{
		QuestionID:       "case-1",
		QuestionType:     "knowledge-update",
		Answer:           flexString("expected-secret"),
		HaystackSessions: make([][]lmeTurn, 2),
	}
	result := &backendResult{
		IngestedPairs: 3,
		FinalMemories: make([]memorySnapshot, 4),
		Retrieval:     make([]memoryHit, 5),
		FailureStage:  "retrieval-turn-miss",
		ExactMatch:    true,
		F1:            0.75,
		Answer:        "model-secret",
	}

	caseProgress := longMemEvalCaseProgress(1, 2, inst, true)
	backendProgress := longMemEvalBackendProgress("pgvector", result, true)
	for _, secret := range []string{
		"expected-secret", "model-secret", "retrieval-turn-miss",
		"evidence=", "em=", "f1=",
	} {
		if strings.Contains(caseProgress+backendProgress, secret) {
			t.Fatalf("blind progress exposed %q: %q / %q",
				secret, caseProgress, backendProgress)
		}
	}
	for _, operational := range []string{
		"case-1", "sessions=2", "pairs=3", "memories=4", "hits=5",
	} {
		if !strings.Contains(caseProgress+backendProgress, operational) {
			t.Fatalf("blind progress omitted %q: %q / %q",
				operational, caseProgress, backendProgress)
		}
	}

	normalProgress := longMemEvalCaseProgress(1, 2, inst, false) +
		longMemEvalBackendProgress("pgvector", result, false)
	for _, outcome := range []string{
		"expected-secret", "model-secret", "retrieval-turn-miss", "em=true", "f1=0.750",
	} {
		if !strings.Contains(normalProgress, outcome) {
			t.Fatalf("normal progress omitted %q: %q", outcome, normalProgress)
		}
	}
}

func TestSaveCaseLogBlindProgressRedactsOutcomeContent(t *testing.T) {
	t.Parallel()
	outputDir := t.TempDir()
	cr := &caseResult{
		QuestionID:       "case-1",
		QuestionType:     "knowledge-update",
		QuestionDate:     "2026-07-21",
		Question:         "question-secret",
		Answer:           "reference-secret",
		AnswerSessionIDs: []string{"answer-session-secret"},
	}
	br := &backendResult{
		Backend:               "pgvector",
		UserID:                "user-1",
		IngestedPairs:         1,
		SnapshotTruncated:     true,
		ProviderUsageReported: true,
		ProviderUsageError:    "provider-error-secret",
		FailureStage:          "failure-stage-secret",
		Answer:                "model-answer-secret",
		AnswerError:           "answer-error-secret",
		Error:                 "backend-error-secret",
		TokenUsage: &lmeTokenUsage{
			PromptTokens:     10,
			CompletionTokens: 2,
			TotalTokens:      12,
			CachedTokens:     4,
			LLMCalls:         1,
		},
		EmbeddingUsage: &lmeEmbeddingUsage{
			PromptTokens:      3,
			TotalTokens:       3,
			Calls:             1,
			Requests:          2,
			ResponseCacheHits: 1,
		},
		IngestTraces: []ingestTrace{{
			SessionIndex:      2,
			SessionID:         "session-2",
			Date:              "2026-07-20",
			PairIndex:         3,
			HasAnswer:         true,
			Messages:          []traceMessage{{Role: "user", Content: "message-secret"}},
			Extraction:        &extractionTrace{Error: "extraction-error-secret"},
			NewMemories:       []memorySnapshot{{Memory: "new-memory-secret"}},
			MemoryCount:       4,
			SnapshotTruncated: true,
			Error:             "trace-error-secret",
			DurationMs:        25,
		}},
		FinalMemories: []memorySnapshot{{Memory: "final-memory-secret"}},
		Retrieval:     []memoryHit{{Memory: "retrieval-secret"}},
	}

	saveCaseLog(outputDir, cr, br, true)
	data, err := os.ReadFile(filepath.Join(outputDir, "case-1_pgvector.log"))
	if err != nil {
		t.Fatalf("read blind case log: %v", err)
	}
	logText := string(data)
	for _, secret := range []string{
		"question-secret",
		"reference-secret",
		"answer-session-secret",
		"user-1",
		"session-2",
		"2026-07-20",
		"model-answer-secret",
		"failure-stage-secret",
		"backend-error-secret",
		"answer-error-secret",
		"provider-error-secret",
		"trace-error-secret",
		"message-secret",
		"extraction-error-secret",
		"new-memory-secret",
		"final-memory-secret",
		"retrieval-secret",
		"has_answer=",
		"=== Answer ===",
		"Evidence:",
	} {
		if strings.Contains(logText, secret) {
			t.Fatalf("blind case log exposed %q:\n%s", secret, logText)
		}
	}
	for _, operational := range []string{
		"BlindProgress: true",
		"QuestionID: case-1",
		"Backend: pgvector",
		"Pairs: 1",
		"FinalMemories: 1",
		"RetrievalHits: 1",
		"ErrorPresent: true",
		"TokenUsage: prompt=10 completion=2 total=12 cached=4 calls=1",
		"EmbeddingUsage: requests=2 cache_hits=1 prompt=3 total=3 calls=1",
		"[session_idx=2 pair=3]",
		"duration=25ms new=1 total=4 snapshot_truncated=true error_present=true",
	} {
		if !strings.Contains(logText, operational) {
			t.Fatalf("blind case log omitted %q:\n%s", operational, logText)
		}
	}
}

func TestSaveCaseLogIncludesPersistenceTrace(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	cr := &caseResult{QuestionID: "case-1"}
	br := &backendResult{
		Backend: "pgvector",
		IngestTraces: []ingestTrace{{
			Extraction: &extractionTrace{
				Operations: []extractionOperation{{
					Stage:  "assistant_result",
					Type:   extractor.OperationAdd,
					Memory: "Recommended the museum.",
				}},
				Persistence: []extractionPersistenceTrace{{
					OperationIndex:      0,
					Status:              lmePersistenceObserved,
					Effect:              string(extractor.OperationAdd),
					Reason:              "snapshot_changed",
					ObservedMemoryID:    "memory-1",
					ObservedAttribution: lmeAttributionAssistant,
				}},
			},
		}},
	}

	saveCaseLog(outputDir, cr, br, false)
	data, err := os.ReadFile(filepath.Join(outputDir, "case-1_pgvector.log"))
	if err != nil {
		t.Fatalf("read case log: %v", err)
	}
	logText := string(data)
	for _, want := range []string{
		"op[0] stage=assistant_result type=add",
		"persistence: status=observed effect=add",
		"reason=snapshot_changed observed_id=memory-1",
		"observed_attribution=assistant",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("case log missing %q:\n%s", want, logText)
		}
	}
}

func TestLongMemEvalBuildProvenance(t *testing.T) {
	t.Parallel()
	if current := currentLongMemEvalBuildProvenance(); current.GoVersion == "" {
		t.Fatalf("current build provenance omitted Go version: %+v", current)
	}

	info := &debug.BuildInfo{
		GoVersion: "go-test",
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "benchmark-sha"},
			{Key: "vcs.modified", Value: "true"},
		},
		Deps: []*debug.Module{
			{
				Path:    lmeAgentModulePath,
				Version: "v1.7.0",
				Replace: &debug.Module{
					Path:    "github.com/example/trpc-agent-go",
					Version: "v0.0.0-test-agent",
				},
			},
			{
				Path:    lmePGVectorModulePath,
				Version: "v1.7.0",
				Replace: &debug.Module{Path: "/local/checkout", Version: "(devel)"},
			},
		},
	}

	got := longMemEvalBuildProvenance(info, true)
	if got.GoVersion != "go-test" || got.Revision != "benchmark-sha" || !got.Modified {
		t.Fatalf("unexpected build provenance: %+v", got)
	}
	agent := got.Modules[lmeAgentModulePath]
	if agent.ReplacementPath != "github.com/example/trpc-agent-go" ||
		agent.ReplacementVersion != "v0.0.0-test-agent" || agent.LocalReplacement {
		t.Fatalf("unexpected agent provenance: %+v", agent)
	}
	pgvector := got.Modules[lmePGVectorModulePath]
	if !pgvector.LocalReplacement || pgvector.ReplacementPath != "" {
		t.Fatalf("local path leaked into pgvector provenance: %+v", pgvector)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal provenance: %v", err)
	}
	if strings.Contains(string(encoded), "/local/checkout") {
		t.Fatalf("local replacement path leaked into JSON: %s", encoded)
	}
	empty := longMemEvalBuildProvenance(nil, false)
	if empty.GoVersion != "" || empty.Revision != "" || empty.Modified ||
		empty.BuildProfile != "" || empty.ModuleManifestSHA256 != "" ||
		empty.ModuleSumSHA256 != "" || empty.Modules != nil {
		t.Fatalf("unexpected unavailable build provenance: %+v", empty)
	}
	injected := applyLongMemEvalInjectedProvenance(
		empty,
		" injected-sha ",
		"true",
		" candidate ",
		" manifest-sha ",
		" sum-sha ",
	)
	if injected.Revision != "injected-sha" || !injected.Modified ||
		injected.BuildProfile != "candidate" ||
		injected.ModuleManifestSHA256 != "manifest-sha" ||
		injected.ModuleSumSHA256 != "sum-sha" {
		t.Fatalf("injected provenance was not applied: %+v", injected)
	}
	native := applyLongMemEvalInjectedProvenance(
		lmeBuildProvenance{
			Revision:             "native-sha",
			Modified:             true,
			BuildProfile:         "candidate",
			ModuleManifestSHA256: "native-manifest",
			ModuleSumSHA256:      "native-sum",
		},
		"injected-sha",
		"false",
		"upstream",
		"injected-manifest",
		"injected-sum",
	)
	if native.Revision != "native-sha" || native.Modified ||
		native.BuildProfile != "candidate" ||
		native.ModuleManifestSHA256 != "native-manifest" ||
		native.ModuleSumSHA256 != "native-sum" {
		t.Fatalf("native revision or injected modified state was not preserved: %+v", native)
	}
	invalid := applyLongMemEvalInjectedProvenance(
		empty,
		"",
		"not-a-bool",
		"",
		"",
		"",
	)
	if invalid.Revision != "" || invalid.Modified {
		t.Fatalf("invalid injected provenance changed result: %+v", invalid)
	}
}

func TestWriteLongMemEvalSelectionOmitsQuestionContent(t *testing.T) {
	originalPerType := *flagLMEPerType
	originalAbstentionCount := *flagLMEAbstentionCount
	originalSeed := *flagLMESampleSeed
	t.Cleanup(func() {
		*flagLMEPerType = originalPerType
		*flagLMEAbstentionCount = originalAbstentionCount
		*flagLMESampleSeed = originalSeed
	})
	*flagLMEPerType = 2
	*flagLMEAbstentionCount = 1
	*flagLMESampleSeed = 271

	instances := []*lmeInstance{
		{
			QuestionID:   "question-1",
			QuestionType: "single-session-user",
			Question:     "private question content",
			Answer:       "private answer content",
		},
		{
			QuestionID:   "question-2_abs",
			QuestionType: "single-session-user",
			Question:     "another private question",
			Answer:       "another private answer",
		},
	}
	var output strings.Builder
	if err := writeLongMemEvalSelection(
		&output,
		instances,
		[]string{"question-a", "question-z"},
		"dataset-digest",
		"selection-digest",
		"protocol-digest",
		currentLongMemEvalProtocol(),
	); err != nil {
		t.Fatalf("write selection: %v", err)
	}
	if strings.Contains(output.String(), "private") {
		t.Fatalf("selection leaked question content: %s", output.String())
	}

	var got lmeSelectionManifest
	if err := json.Unmarshal([]byte(output.String()), &got); err != nil {
		t.Fatalf("decode selection: %v", err)
	}
	if got.SampleSeed != 271 || got.SamplePerType != 2 ||
		got.AbstentionCount != 1 || got.ExcludedCount != 2 ||
		len(got.Cases) != 2 {
		t.Fatalf("unexpected manifest: %+v", got)
	}
	if got.SchemaVersion != lmeSelectionManifestSchemaVersion ||
		got.Protocol.Version != lmeProtocolVersion ||
		got.Protocol.TopK != *flagVectorTopK ||
		got.Protocol.AnswerModel != getModelName() ||
		got.Protocol.AnswerModelVariant != getModelVariant() ||
		got.Protocol.EmbeddingModel != getEmbedModelName() ||
		got.Protocol.JudgeModel != getEvalModelName() ||
		got.Protocol.JudgeRuns != *flagLMEJudgeRuns ||
		got.Protocol.AnswerPromptVersion != lmeAnswerPromptVersion ||
		got.Protocol.JudgePromptVersion != lmeJudgePromptVersion ||
		got.Protocol.JudgeProtocolVersion != lmeJudgeProtocolVersion {
		t.Fatalf("selection omitted protocol provenance: %+v", got.Protocol)
	}
	wantExcludedDigest, err := longMemEvalJSONSHA256([]string{"question-a", "question-z"})
	if err != nil {
		t.Fatalf("hash expected exclusions: %v", err)
	}
	if got.ExcludedSHA256 != wantExcludedDigest {
		t.Fatalf("excluded digest = %q, want %q", got.ExcludedSHA256, wantExcludedDigest)
	}
	if got.Build.GoVersion == "" {
		t.Fatalf("selection omitted build provenance: %+v", got.Build)
	}
	if got.Cases[0].QuestionID != "question-1" || got.Cases[0].Abstention {
		t.Fatalf("unexpected answerable case: %+v", got.Cases[0])
	}
	if got.Cases[1].QuestionID != "question-2_abs" || !got.Cases[1].Abstention {
		t.Fatalf("unexpected abstention case: %+v", got.Cases[1])
	}
}

func TestLongMemEvalBuildProvenanceIssue(t *testing.T) {
	t.Parallel()
	pinned := lmeBuildProvenance{
		GoVersion:            "go-test",
		Revision:             "benchmark-sha",
		BuildProfile:         "candidate",
		ModuleManifestSHA256: "manifest-sha",
		ModuleSumSHA256:      "sum-sha",
		Modules: map[string]lmeModuleProvenance{
			lmeAgentModulePath: {
				Version: "v1.7.0",
			},
			lmePGVectorModulePath: {
				ReplacementPath:    "github.com/example/trpc-agent-go/memory/pgvector",
				ReplacementVersion: "v0.0.0-test",
			},
		},
	}
	if issue := longMemEvalBuildProvenanceIssue(pinned); issue != "" {
		t.Fatalf("pinned build reported an issue: %s", issue)
	}

	tests := []struct {
		name  string
		build lmeBuildProvenance
		want  string
	}{
		{
			name:  "missing revision",
			build: lmeBuildProvenance{},
			want:  "benchmark revision is missing",
		},
		{
			name: "modified",
			build: lmeBuildProvenance{
				Revision: "benchmark-sha",
				Modified: true,
			},
			want: "benchmark worktree was modified at build time",
		},
		{
			name: "missing build profile",
			build: func() lmeBuildProvenance {
				build := pinned
				build.BuildProfile = ""
				return build
			}(),
			want: "build profile is missing or unsupported",
		},
		{
			name: "local memory module",
			build: func() lmeBuildProvenance {
				build := pinned
				build.Modules = maps.Clone(pinned.Modules)
				module := build.Modules[lmeAgentModulePath]
				module.LocalReplacement = true
				build.Modules[lmeAgentModulePath] = module
				return build
			}(),
			want: "uses an unpinned local replacement",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if issue := longMemEvalBuildProvenanceIssue(test.build); !strings.Contains(issue, test.want) {
				t.Fatalf("issue = %q, want substring %q", issue, test.want)
			}
		})
	}
}

func TestLongMemEvalExperimentDigests(t *testing.T) {
	t.Parallel()

	datasetPath := filepath.Join(t.TempDir(), "dataset.json")
	if err := os.WriteFile(datasetPath, []byte("abc"), 0644); err != nil {
		t.Fatalf("write dataset: %v", err)
	}
	protocol := lmeProtocolProvenance{
		Version:             lmeProtocolVersion,
		AnswerPromptVersion: lmeAnswerPromptVersion,
		TopK:                30,
	}
	dataset, selection, protocolDigest, err := longMemEvalExperimentDigests(
		datasetPath,
		[]*lmeInstance{{QuestionID: "q1"}, {QuestionID: "q2"}},
		protocol,
	)
	if err != nil {
		t.Fatalf("experiment digests: %v", err)
	}
	if dataset != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("dataset digest = %q", dataset)
	}
	wantSelection, err := longMemEvalJSONSHA256([]string{"q1", "q2"})
	if err != nil {
		t.Fatalf("selection digest: %v", err)
	}
	if selection != wantSelection || protocolDigest == "" {
		t.Fatalf("unexpected digests: selection=%q protocol=%q", selection, protocolDigest)
	}
	changed := protocol
	changed.TopK++
	changedDigest, err := longMemEvalJSONSHA256(changed)
	if err != nil {
		t.Fatalf("changed protocol digest: %v", err)
	}
	if changedDigest == protocolDigest {
		t.Fatal("protocol digest did not change with top-k")
	}
}

func TestWriteLongMemEvalResultsErrors(t *testing.T) {
	t.Parallel()

	result := &runResult{Metadata: map[string]any{"invalid": make(chan int)}}
	if err := writeLongMemEvalResults(filepath.Join(t.TempDir(), "results.json"), result); err == nil {
		t.Fatal("non-JSON metadata should fail")
	}

	missingParent := filepath.Join(t.TempDir(), "missing", "results.json")
	if err := writeLongMemEvalResults(missingParent, &runResult{}); err == nil {
		t.Fatal("missing output directory should fail")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "results.json")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatalf("create target directory: %v", err)
	}
	if err := writeLongMemEvalResults(target, &runResult{}); err == nil {
		t.Fatal("replacing a directory should fail")
	}
	if _, err := os.Stat(target + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary result was not removed: %v", err)
	}
}

func TestLongMemEvalDateHelpers(t *testing.T) {
	t.Parallel()

	ts, ok := lmeUnixTimestamp("2023/04/10 (Mon) 14:47")
	if !ok {
		t.Fatal("expected date to parse")
	}
	if ts != 1681138020 {
		t.Fatalf("unexpected timestamp: got %d", ts)
	}

	if _, ok := lmeUnixTimestamp("not-a-date"); ok {
		t.Fatal("invalid date parsed")
	}

	observationDate, ok := lmeObservationDate("2023/04/10 (Mon) 14:47")
	if !ok || observationDate != "2023-04-10" {
		t.Fatalf("unexpected observation date: %q, %v", observationDate, ok)
	}
	if _, ok := lmeObservationDate("not-a-date"); ok {
		t.Fatal("invalid observation date parsed")
	}
}

func TestLatestSessionMessageTimestamp(t *testing.T) {
	t.Parallel()

	sess := session.NewSession(lmeAppName, "user", "session")
	appendMessages(sess, []model.Message{
		{Role: model.RoleUser, Content: "hello"},
		{Role: model.RoleAssistant, Content: "hi"},
	}, "source", 0)

	want := sess.GetEvents()[1].Timestamp.UTC()
	got, ok := latestSessionMessageTimestamp(sess)
	if !ok {
		t.Fatal("expected latest message timestamp")
	}
	if !got.Equal(want) {
		t.Fatalf("latest timestamp: got %s want %s", got, want)
	}
}

func TestWaitForAutoMemory(t *testing.T) {
	t.Parallel()

	sess := session.NewSession(lmeAppName, "user", "session")
	want := time.Now().UTC()
	go func() {
		time.Sleep(5 * time.Millisecond)
		sess.SetState(memory.SessionStateKeyAutoMemoryLastExtractAt,
			[]byte(want.Format(time.RFC3339Nano)))
	}()

	if err := waitForAutoMemory(context.Background(), sess, want, time.Second); err != nil {
		t.Fatalf("wait for auto memory: %v", err)
	}
}

func TestWaitForAutoMemoryTimeout(t *testing.T) {
	t.Parallel()

	sess := session.NewSession(lmeAppName, "user", "session")
	err := waitForAutoMemory(context.Background(), sess, time.Now().UTC(), time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func TestWaitForAutoMemoryError(t *testing.T) {
	t.Parallel()

	sess := session.NewSession(lmeAppName, "user", "session")
	sess.SetState(lmeAutoMemoryLastErrorStateKey,
		[]byte("generate embedding failed"))
	err := waitForAutoMemory(context.Background(), sess, time.Now().UTC(), 0)
	if err == nil || !strings.Contains(err.Error(), "generate embedding failed") {
		t.Fatalf("expected auto memory error, got %v", err)
	}
}

func TestWaitForAutoMemoryInvalidMarker(t *testing.T) {
	t.Parallel()

	sess := session.NewSession(lmeAppName, "user", "session")
	sess.SetState(memory.SessionStateKeyAutoMemoryLastExtractAt, []byte("invalid"))
	err := waitForAutoMemory(context.Background(), sess, time.Now().UTC(), time.Second)
	if err == nil || !strings.Contains(err.Error(), "parse auto memory completion marker") {
		t.Fatalf("expected completion marker error, got %v", err)
	}
}

func TestLMETracingExtractorRecordsOperations(t *testing.T) {
	t.Parallel()

	eventTime := time.Date(2023, time.May, 22, 0, 0, 0, 0, time.UTC)
	stub := &lmeExtractorStub{ops: []*extractor.Operation{{
		Type:       extractor.OperationUpdate,
		MemoryID:   "memory-1",
		Memory:     "Prefers Memrise for mnemonic-based study",
		Topics:     []string{"Memrise", "mnemonics"},
		MemoryKind: memory.KindFact,
		EventTime:  &eventTime,
	}}}
	tracing := &lmeTracingExtractor{MemoryExtractor: stub}
	existing := []*memory.Entry{{ID: "memory-1"}}

	if _, err := tracing.Extract(context.Background(), nil, existing); err != nil {
		t.Fatalf("extract: %v", err)
	}
	trace := tracing.Snapshot()
	if trace == nil || trace.ExistingMemoryCount != 1 || len(trace.Operations) != 1 {
		t.Fatalf("unexpected extraction trace: %#v", trace)
	}
	op := trace.Operations[0]
	if op.Type != extractor.OperationUpdate || op.MemoryID != "memory-1" {
		t.Fatalf("unexpected operation: %#v", op)
	}
	if op.EventTime != "2023-05-22T00:00:00Z" {
		t.Fatalf("unexpected event time: %q", op.EventTime)
	}
}

func TestLMETracingExtractorForwardsOperationStages(t *testing.T) {
	t.Parallel()

	primary := &extractor.Operation{
		Type: extractor.OperationAdd, Memory: "User prefers Go.",
	}
	assistantResult := &extractor.Operation{
		Type: extractor.OperationAdd, Memory: "Recommended Python.",
	}
	tracing := &lmeTracingExtractor{MemoryExtractor: &lmeStagedExtractorStub{
		lmeExtractorStub: &lmeExtractorStub{ops: []*extractor.Operation{primary}},
		assistantOps:     []*extractor.Operation{assistantResult},
	}}

	gotPrimary, gotResults, err := tracing.ExtractOperationStages(
		context.Background(), nil, []*memory.Entry{{ID: "existing"}},
	)
	if err != nil {
		t.Fatalf("extract operation stages: %v", err)
	}
	if len(gotPrimary) != 1 || gotPrimary[0] != primary ||
		len(gotResults) != 1 || gotResults[0] != assistantResult {
		t.Fatalf("unexpected staged operations: primary=%#v result=%#v",
			gotPrimary, gotResults)
	}
	trace := tracing.Snapshot()
	if trace == nil || trace.ExistingMemoryCount != 1 ||
		len(trace.Operations) != 2 {
		t.Fatalf("unexpected extraction trace: %#v", trace)
	}
	if trace.Operations[0].Stage != "primary" ||
		trace.Operations[1].Stage != "assistant_result" {
		t.Fatalf("unexpected operation stages: %#v", trace.Operations)
	}
}

func TestLMETracingExtractorStagesFallback(t *testing.T) {
	t.Parallel()

	op := &extractor.Operation{Type: extractor.OperationAdd, Memory: "fact"}
	tracing := &lmeTracingExtractor{MemoryExtractor: &lmeExtractorStub{
		ops: []*extractor.Operation{op},
	}}
	primary, assistantResults, err := tracing.ExtractOperationStages(
		context.Background(), nil, nil,
	)
	if err != nil {
		t.Fatalf("extract operation stages: %v", err)
	}
	if len(primary) != 1 || primary[0] != op || assistantResults != nil {
		t.Fatalf("unexpected fallback operations: primary=%#v result=%#v",
			primary, assistantResults)
	}
	trace := tracing.Snapshot()
	if trace == nil || len(trace.Operations) != 1 ||
		trace.Operations[0].Stage != "primary" {
		t.Fatalf("unexpected fallback trace: %#v", trace)
	}
}

func TestMem0OSSIngestRetriesProviderRateLimit(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)
		if r.URL.Path != "/memories" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if attempt == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(
				`{"detail":"Provider rate limit hit.","code":"provider_rate_limited"}`,
			))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sess := session.NewSession(lmeAppName, "user", "session")
	appendMessages(sess, []model.Message{
		{Role: model.RoleUser, Content: "hello"},
		{Role: model.RoleAssistant, Content: "hi"},
	}, "source", 0)
	backend := &mem0Backend{
		host:       server.URL,
		selfHosted: true,
		httpClient: server.Client(),
	}
	err := backend.ingestPairOSS(context.Background(), sess, ingestMeta{SessionID: "source"})
	if err != nil {
		t.Fatalf("ingest pair: %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("unexpected attempts: got %d want 2", got)
	}
}

func TestMem0OSSSearchRetriesProviderRateLimit(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(
				`{"detail":"Provider rate limit hit.","code":"provider_rate_limited"}`,
			))
			return
		}
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer server.Close()

	svc, err := memorymem0.NewService(
		memorymem0.WithHost(server.URL),
		memorymem0.WithHTTPClient(server.Client()),
		memorymem0.WithSelfHostedOSS(),
	)
	if err != nil {
		t.Fatalf("new mem0 service: %v", err)
	}
	defer svc.Close()
	backend := &mem0Backend{svc: svc, selfHosted: true}
	_, err = backend.Search(context.Background(), memory.UserKey{
		AppName: lmeAppName,
		UserID:  "user",
	}, "query", 30)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("unexpected attempts: got %d want 2", got)
	}
}

func TestMem0OSSSnapshotUsesServerLimitAndReportsBoundary(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		count     int
		truncated bool
	}{
		{name: "complete below limit", count: 600},
		{name: "ambiguous at limit", count: lmeMem0OSSSnapshotLimit, truncated: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.URL.Query().Get("top_k"); got != "1000" {
					t.Errorf("top_k = %q, want 1000", got)
				}
				results := make([]map[string]any, test.count)
				for i := range results {
					results[i] = map[string]any{
						"id":      fmt.Sprintf("memory-%04d", i),
						"memory":  fmt.Sprintf("memory %d", i),
						"user_id": "user",
						"metadata": map[string]any{
							"trpc_app_name": lmeAppName,
						},
					}
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"results": results}); err != nil {
					t.Errorf("encode response: %v", err)
				}
			}))
			defer server.Close()

			svc, err := memorymem0.NewService(
				memorymem0.WithHost(server.URL),
				memorymem0.WithHTTPClient(server.Client()),
				memorymem0.WithSelfHostedOSS(),
			)
			if err != nil {
				t.Fatalf("new mem0 service: %v", err)
			}
			defer svc.Close()
			backend := &mem0Backend{svc: svc, selfHosted: true}
			memories, truncated, err := backend.Read(context.Background(), memory.UserKey{
				AppName: lmeAppName,
				UserID:  "user",
			})
			if err != nil {
				t.Fatalf("read snapshot: %v", err)
			}
			if len(memories) != test.count || truncated != test.truncated {
				t.Fatalf("snapshot count=%d truncated=%v, want count=%d truncated=%v",
					len(memories), truncated, test.count, test.truncated)
			}
		})
	}
}

func TestMem0OSSIngestPassesObservationDateWithoutChangingMessages(t *testing.T) {
	t.Parallel()

	var payload struct {
		Messages []map[string]string `json:"messages"`
		Metadata map[string]any      `json:"metadata"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sess := session.NewSession(lmeAppName, "user", "session")
	appendMessages(sess, []model.Message{
		{Role: model.RoleUser, Content: "hello "},
		{Role: model.RoleAssistant, Content: " hi"},
	}, "source", 0)
	backend := &mem0Backend{
		host:       server.URL,
		selfHosted: true,
		httpClient: server.Client(),
	}
	err := backend.ingestPairOSS(context.Background(), sess, ingestMeta{
		SessionID: "source",
		Date:      "2023/04/10 (Mon) 14:47",
	})
	if err != nil {
		t.Fatalf("ingest pair: %v", err)
	}
	if len(payload.Messages) != 2 || payload.Messages[0]["content"] != "hello " ||
		payload.Messages[1]["content"] != " hi" {
		t.Fatalf("messages were changed: %#v", payload.Messages)
	}
	if got := payload.Metadata["observation_date"]; got != "2023-04-10" {
		t.Fatalf("unexpected observation date: %#v", got)
	}
}

func TestMem0OSSIngestUsesRequestTimeout(t *testing.T) {
	oldTimeout := *flagLMEModelCallTimeout
	*flagLMEModelCallTimeout = 20 * time.Millisecond
	defer func() { *flagLMEModelCallTimeout = oldTimeout }()

	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		select {
		case <-r.Context().Done():
		case <-time.After(150 * time.Millisecond):
		}
	}))
	defer server.Close()

	sess := session.NewSession(lmeAppName, "user", "session")
	appendMessages(sess, []model.Message{
		{Role: model.RoleUser, Content: "hello"},
		{Role: model.RoleAssistant, Content: "hi"},
	}, "source", 0)
	backend := &mem0Backend{
		host:       server.URL,
		selfHosted: true,
		httpClient: server.Client(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := backend.ingestPairOSS(ctx, sess, ingestMeta{SessionID: "source"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("request attempts = %d, want 1", got)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("request timeout took too long: %v", elapsed)
	}
}

func TestLongMemEvalMem0OSSRequestTimeout(t *testing.T) {
	oldTimeout := *flagLMEModelCallTimeout
	defer func() { *flagLMEModelCallTimeout = oldTimeout }()

	*flagLMEModelCallTimeout = 5 * time.Minute
	if got, want := longMemEvalMem0OSSRequestTimeout(), 6*time.Minute; got != want {
		t.Fatalf("request timeout = %v, want %v", got, want)
	}

	*flagLMEModelCallTimeout = 0
	if got := longMemEvalMem0OSSRequestTimeout(); got != lmeMem0RequestTimeout {
		t.Fatalf("disabled-model request timeout = %v, want %v",
			got, lmeMem0RequestTimeout)
	}

	maxDuration := time.Duration(1<<63 - 1)
	*flagLMEModelCallTimeout = maxDuration
	if got := longMemEvalMem0OSSRequestTimeout(); got != maxDuration {
		t.Fatalf("overflow-safe request timeout = %v, want %v", got, maxDuration)
	}
}

func TestNewLongMemEvalMem0HTTPClientUsesRequestTimeout(t *testing.T) {
	oldTimeout := *flagLMEModelCallTimeout
	defer func() { *flagLMEModelCallTimeout = oldTimeout }()

	*flagLMEModelCallTimeout = 5 * time.Minute
	usage := &lmeProviderUsageTracker{}
	client := newLongMemEvalMem0HTTPClient(usage)
	if got, want := client.Timeout, 6*time.Minute; got != want {
		t.Fatalf("HTTP client timeout = %v, want %v", got, want)
	}
	transport, ok := client.Transport.(*lmeMem0UsageTransport)
	if !ok {
		t.Fatalf("HTTP transport = %T, want *lmeMem0UsageTransport", client.Transport)
	}
	if transport.tracker != usage {
		t.Fatal("HTTP transport does not use the run usage tracker")
	}
}

func TestMem0UsageTransportRecordsHeader(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(lmeMem0UsageHeader, `{
  "llm":{"prompt_tokens":120,"completion_tokens":8,"total_tokens":128,"cached_tokens":32,"llm_calls":2,"usage_missing_calls":1},
  "embedding":{"prompt_tokens":16,"total_tokens":16,"calls":3,"usage_missing_calls":2}
}`)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	tracker := &lmeProviderUsageTracker{}
	client := &http.Client{Transport: &lmeMem0UsageTransport{
		base:    http.DefaultTransport,
		tracker: tracker,
	}}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()

	usage := tracker.Snapshot()
	if !usage.Reported || usage.LLM.TotalTokens != 128 || usage.LLM.CachedTokens != 32 {
		t.Fatalf("unexpected LLM usage: %#v", usage)
	}
	if usage.LLM.UsageMissingCalls != 1 || usage.Embedding.TotalTokens != 16 ||
		usage.Embedding.Calls != 3 || usage.Embedding.Requests != 3 ||
		usage.Embedding.UsageMissingCalls != 2 {
		t.Fatalf("unexpected embedding usage: %#v", usage)
	}
}

func TestLongMemEvalTrackingEmbedderRecordsUsage(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "object":"list",
  "data":[{"object":"embedding","embedding":[0.1,0.2],"index":0}],
  "model":"text-embedding-3-small",
  "usage":{"prompt_tokens":7,"total_tokens":7}
}`))
	}))
	defer server.Close()

	base := embeddingopenai.New(
		embeddingopenai.WithAPIKey("test"),
		embeddingopenai.WithBaseURL(server.URL),
		embeddingopenai.WithModel("text-embedding-3-small"),
		embeddingopenai.WithDimensions(2),
	)
	tracker := newLongMemEvalTrackingEmbedder(base)
	got, err := tracker.GetEmbedding(context.Background(), "hello")
	if err != nil {
		t.Fatalf("get embedding: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("embedding length = %d, want 2", len(got))
	}
	usage := tracker.Snapshot()
	if usage.PromptTokens != 7 || usage.TotalTokens != 7 || usage.Calls != 1 {
		t.Fatalf("unexpected embedding usage: %#v", usage)
	}
}

func TestPrepareLongMemEvalMem0ConfiguresAndSanitizesRuntime(t *testing.T) {
	oldHost := *flagMem0Host
	oldCloud := *flagMem0Cloud
	oldTemperature := *flagMem0LLMTemperature
	defer func() {
		*flagMem0Host = oldHost
		*flagMem0Cloud = oldCloud
		*flagMem0LLMTemperature = oldTemperature
	}()

	configured := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			configured = true
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
  "version":"v1.1",
  "llm":{"provider":"openai","config":{"api_key":"secret","model":"glm52","temperature":0}},
  "embedder":{"provider":"openai","config":{"api_key":"secret","model":"text-embedding-3-small"}},
  "vector_store":{"provider":"pgvector","config":{"password":"secret","embedding_model_dims":1536}}
}`))
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()

	*flagMem0Host = server.URL
	*flagMem0Cloud = false
	*flagMem0LLMTemperature = -1
	config, err := prepareLongMemEvalMem0(context.Background(), []string{"mem0"})
	if err != nil || config == nil || configured {
		t.Fatalf("read-only runtime configuration failed: config=%#v err=%v", config, err)
	}
	if config.LLMTemperature == nil || *config.LLMTemperature != 0 {
		t.Fatalf("runtime temperature was not recorded: %#v", config)
	}

	*flagMem0LLMTemperature = 0
	config, err = prepareLongMemEvalMem0(context.Background(), []string{"pgvector", "mem0"})
	if err != nil {
		t.Fatalf("prepare mem0: %v", err)
	}
	if !configured {
		t.Fatal("expected mem0 temperature configuration request")
	}
	if config == nil || config.LLMModel != "glm52" || config.EmbeddingDimensions != 1536 {
		t.Fatalf("unexpected runtime configuration: %#v", config)
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal runtime configuration: %v", err)
	}
	if strings.Contains(string(encoded), "secret") {
		t.Fatalf("runtime configuration leaked credentials: %s", encoded)
	}
}

func TestValidateLongMemEvalAttributionBackendsRejectsMem0Cloud(t *testing.T) {
	oldCloud := *flagMem0Cloud
	t.Cleanup(func() {
		*flagMem0Cloud = oldCloud
	})

	*flagMem0Cloud = true
	err := validateLongMemEvalAttributionBackends(
		[]string{"pgvector", "mem0"},
	)
	if err == nil || !strings.Contains(err.Error(), "self-hosted Mem0 OSS") {
		t.Fatalf("Mem0 Cloud attribution error = %v", err)
	}
	if err := validateLongMemEvalAttributionBackends(
		[]string{"pgvector"},
	); err != nil {
		t.Fatalf("pgvector attribution validation: %v", err)
	}

	*flagMem0Cloud = false
	if err := validateLongMemEvalAttributionBackends(
		[]string{"mem0"},
	); err != nil {
		t.Fatalf("self-hosted Mem0 attribution validation: %v", err)
	}
}

func TestPrepareLongMemEvalMem0Failures(t *testing.T) {
	oldHost := *flagMem0Host
	oldCloud := *flagMem0Cloud
	oldTemperature := *flagMem0LLMTemperature
	defer func() {
		*flagMem0Host = oldHost
		*flagMem0Cloud = oldCloud
		*flagMem0LLMTemperature = oldTemperature
	}()
	*flagMem0Cloud = false

	t.Run("unselected backend", func(t *testing.T) {
		config, err := prepareLongMemEvalMem0(context.Background(), []string{"pgvector"})
		if err != nil || config != nil {
			t.Fatalf("unselected mem0 returned config=%#v err=%v", config, err)
		}
	})

	t.Run("invalid host", func(t *testing.T) {
		*flagMem0Host = "://invalid"
		*flagMem0LLMTemperature = -1
		_, err := prepareLongMemEvalMem0(context.Background(), []string{"mem0"})
		if err == nil || !strings.Contains(err.Error(), "configuration read request") {
			t.Fatalf("expected request error, got %v", err)
		}
	})

	t.Run("cancelled read", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		defer server.Close()
		*flagMem0Host = server.URL
		*flagMem0LLMTemperature = -1
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := prepareLongMemEvalMem0(ctx, []string{"mem0"})
		if err == nil || !strings.Contains(err.Error(), "read mem0 configuration") {
			t.Fatalf("expected cancelled read error, got %v", err)
		}
	})

	t.Run("read status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}))
		defer server.Close()
		*flagMem0Host = server.URL
		*flagMem0LLMTemperature = -1
		_, err := prepareLongMemEvalMem0(context.Background(), []string{"mem0"})
		if err == nil || !strings.Contains(err.Error(), "status=503") {
			t.Fatalf("expected read status error, got %v", err)
		}
	})

	t.Run("invalid response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("not json"))
		}))
		defer server.Close()
		*flagMem0Host = server.URL
		*flagMem0LLMTemperature = -1
		_, err := prepareLongMemEvalMem0(context.Background(), []string{"mem0"})
		if err == nil || !strings.Contains(err.Error(), "decode mem0 configuration") {
			t.Fatalf("expected decode error, got %v", err)
		}
	})

	t.Run("configure status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "bad configuration", http.StatusBadRequest)
		}))
		defer server.Close()
		*flagMem0Host = server.URL
		*flagMem0LLMTemperature = 0
		_, err := prepareLongMemEvalMem0(context.Background(), []string{"mem0"})
		if err == nil || !strings.Contains(err.Error(), "status=400") {
			t.Fatalf("expected configure status error, got %v", err)
		}
	})
}

func TestRetryableMem0Response(t *testing.T) {
	t.Parallel()

	if !isRetryableMem0Response(http.StatusTooManyRequests, nil) {
		t.Fatal("status 429 should be retryable")
	}
	providerRateLimit := []byte(
		`{"detail":"Provider rate limit hit.","code":"provider_rate_limited"}`,
	)
	if !isRetryableMem0Response(http.StatusBadGateway, providerRateLimit) {
		t.Fatal("wrapped provider rate limit should be retryable")
	}
	for _, status := range []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusNotFound,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
	} {
		if isRetryableMem0Response(status, []byte(`{"code":"other"}`)) {
			t.Fatalf("status %d should not be retryable", status)
		}
	}
	if !isRetryableMem0Error(errors.New(
		`mem0 api request failed: status=502 body={"code":"provider_rate_limited"}`,
	)) {
		t.Fatal("wrapped provider rate limit error should be retryable")
	}
	if isRetryableMem0Error(errors.New(
		`mem0 api request failed: status=502 body={"code":"other"}`,
	)) {
		t.Fatal("unrelated gateway error should not be retryable")
	}
}

func TestMem0RequestRetryDelay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 0, want: 0},
		{attempt: 1, want: time.Second},
		{attempt: 2, want: 2 * time.Second},
		{attempt: 3, want: 4 * time.Second},
		{attempt: 4, want: 8 * time.Second},
		{attempt: 5, want: 16 * time.Second},
		{attempt: 6, want: 32 * time.Second},
		{attempt: 7, want: time.Minute},
		{attempt: 8, want: time.Minute},
	}
	for _, test := range tests {
		if got := mem0RequestRetryDelay(test.attempt); got != test.want {
			t.Fatalf("attempt %d delay = %s, want %s",
				test.attempt, got, test.want)
		}
	}
}

func TestLongMemEvalRuntimeError(t *testing.T) {
	t.Parallel()

	if err := longMemEvalRuntimeError(&runResult{Cases: []*caseResult{{
		QuestionID: "question",
		BackendResults: map[string]*backendResult{
			"pgvector": {Backend: "pgvector"},
		},
	}}}); err != nil {
		t.Fatalf("healthy run: %v", err)
	}

	err := longMemEvalRuntimeError(&runResult{Cases: []*caseResult{{
		QuestionID: "question",
		BackendResults: map[string]*backendResult{
			"mem0": {
				Backend:     "mem0",
				Error:       "ingest failed",
				AnswerError: "answer failed",
			},
		},
	}}})
	if err == nil || !strings.Contains(err.Error(), "question/mem0: ingest failed") ||
		!strings.Contains(err.Error(), "question/mem0 answer: answer failed") {
		t.Fatalf("runtime error = %v", err)
	}
}

func TestFilterCasesByQuestionIDs(t *testing.T) {
	oldID := *flagLMEQuestionID
	oldIDs := *flagLMEQuestionIDs
	oldTypes := *flagLMEQuestionTypes
	oldPerType := *flagLMEPerType
	oldAbstention := *flagLMEAbstentionCount
	oldMaxTasks := *flagMaxTasks
	defer func() {
		*flagLMEQuestionID = oldID
		*flagLMEQuestionIDs = oldIDs
		*flagLMEQuestionTypes = oldTypes
		*flagLMEPerType = oldPerType
		*flagLMEAbstentionCount = oldAbstention
		*flagMaxTasks = oldMaxTasks
	}()

	*flagLMEQuestionID = "q1"
	*flagLMEQuestionIDs = "q3, q2"
	*flagLMEQuestionTypes = ""
	*flagLMEPerType = 0
	*flagLMEAbstentionCount = 0
	*flagMaxTasks = 0

	instances := []*lmeInstance{
		{QuestionID: "q1", QuestionType: "single-session-user"},
		{QuestionID: "skip", QuestionType: "single-session-user"},
		{QuestionID: "q2", QuestionType: "multi-session"},
		{QuestionID: "q3", QuestionType: "temporal-reasoning"},
		nil,
	}
	got := filterCases(instances, []string{"q2", "skip"})
	want := []string{"q1", "q3"}
	if len(got) != len(want) {
		t.Fatalf("unexpected case count: got %d want %d", len(got), len(want))
	}
	for i, inst := range got {
		if inst.QuestionID != want[i] {
			t.Fatalf("case %d: got %q want %q", i, inst.QuestionID, want[i])
		}
	}
}

func TestBuildLongMemEvalAnswerPromptPreferenceGuidance(t *testing.T) {
	inst := &lmeInstance{
		QuestionID:   "q-pref",
		QuestionType: "single-session-preference",
		QuestionDate: "2023/05/27 (Sat) 09:00",
		Question:     "Can you recommend events this weekend?",
	}
	prompt := buildLongMemEvalAnswerPrompt(inst, []memoryHit{{
		Memory:       "Attended a language exchange event focused on French and Spanish practice.",
		AttributedTo: lmeAttributionUser,
		Topics:       []string{"language exchange", "French", "Spanish"},
		Score:        0.9876,
		Kind:         "episode",
		EventTime:    "2023-05-20",
		Participants: []string{"Alice"},
		Location:     "Community Center",
	}})

	if !strings.Contains(prompt, "Question type: single-session-preference") {
		t.Fatalf("missing question type: %s", prompt)
	}
	if strings.Contains(prompt, "score=") || strings.Contains(prompt, "0.9876") {
		t.Fatalf("backend-specific retrieval score leaked into answer prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "answer the user's question directly") {
		t.Fatalf("missing direct-answer guidance: %s", prompt)
	}
	if !strings.Contains(prompt, "do not say\n\"I don't know\"") {
		t.Fatalf("missing unknown-answer guard: %s", prompt)
	}
	if !strings.Contains(prompt, "When any retrieved memory is relevant to the preference topic") {
		t.Fatalf("missing relevant-memory guard: %s", prompt)
	}
	if !strings.Contains(prompt, "Do not describe\nthe user in the third person") {
		t.Fatalf("missing third-person guard: %s", prompt)
	}
	if !strings.Contains(prompt, "give actionable advice") {
		t.Fatalf("missing actionable-advice guidance: %s", prompt)
	}
	if !strings.Contains(prompt, "do not invent missing personal context") {
		t.Fatalf("missing unsupported-context guard: %s", prompt)
	}
	if !strings.Contains(prompt, "acknowledge it before suggesting complementary") ||
		!strings.Contains(prompt, "never reintroduce the remembered choice itself") {
		t.Fatalf("missing established-preference guidance: %s", prompt)
	}
	if !strings.Contains(prompt, "[attributed_to=user; kind=episode; event_time=2023-05-20; participants=Alice; location=Community Center]") {
		t.Fatalf("missing memory metadata: %s", prompt)
	}
	if strings.Contains(prompt, "Semantic category memberships:") {
		t.Fatalf("topic metadata changed the frozen answer protocol: %s", prompt)
	}
}

func TestBuildLongMemEvalAnswerPromptNonPreference(t *testing.T) {
	inst := &lmeInstance{
		QuestionID:   "q-fact",
		QuestionType: "single-session-assistant",
		Question:     "What was the fifth bottle?",
	}
	prompt := buildLongMemEvalAnswerPrompt(inst, nil)
	normalizedPrompt := strings.Join(strings.Fields(prompt), " ")

	if strings.Contains(prompt, "answer the user's question directly") {
		t.Fatalf("unexpected preference guidance: %s", prompt)
	}
	if !strings.Contains(prompt, "(no memories retrieved)") {
		t.Fatalf("missing empty-memory marker: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt, "shortest final span") {
		t.Fatalf("missing concise scalar guidance: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt,
		"ordered from most to least relevant") {
		t.Fatalf("missing retrieval-order guidance: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt,
		"lower-ranked memories discuss related entities or events") {
		t.Fatalf("missing lower-ranked distraction guidance: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt,
		"contradictory evidence about the same subject and time") {
		t.Fatalf("missing ranked-evidence conflict guard: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt, "first token must be part of the final answer") {
		t.Fatalf("missing no-reasoning output guard: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt, "comma-separated list") {
		t.Fatalf("missing list output guard: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt, "do not answer with the start date") {
		t.Fatalf("missing duration output guard: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt,
		"identify the same entity, event, action, or relationship") {
		t.Fatalf("missing subject-identity guard: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt,
		"descriptive context supplied by the question as identification context") {
		t.Fatalf("missing question-context guidance: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt,
		"memory need not repeat every organizer, category, location") {
		t.Fatalf("missing non-answer qualifier guidance: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt,
		"question identifies a named event and asks for its date") {
		t.Fatalf("missing named-event guidance: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt, "Related or nearby facts are not enough") {
		t.Fatalf("missing related-fact abstention guard: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt, "project type") {
		t.Fatalf("missing project-type guard: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt, "course work, thesis research") {
		t.Fatalf("missing distinct-event examples: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt, "compute the final value") {
		t.Fatalf("missing final-value guidance: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt, "product brand") ||
		!strings.Contains(normalizedPrompt, "private-label name") {
		t.Fatalf("missing brand/source guidance: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt, "not include markdown") {
		t.Fatalf("missing markdown/explanation guard: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt, `A memory marked "attributed_to=assistant"`) ||
		!strings.Contains(normalizedPrompt, "it is not a fact confirmed by the user") ||
		!strings.Contains(normalizedPrompt, "marker is authoritative") ||
		!strings.Contains(normalizedPrompt, "question explicitly asks what the assistant") ||
		!strings.Contains(normalizedPrompt, "arithmetic, comparison, or planning") ||
		!strings.Contains(normalizedPrompt, "Never use an unconfirmed assistant estimate") {
		t.Fatalf("missing assistant-result provenance guidance: %s", prompt)
	}
}

func TestBuildLongMemEvalAnswerPromptAssistantAttribution(t *testing.T) {
	t.Parallel()

	prompt := buildLongMemEvalAnswerPrompt(&lmeInstance{
		QuestionType: "single-session-assistant",
		Question:     "Which restaurant did the assistant recommend?",
	}, []memoryHit{{
		Memory:       "User was recommended Miss Bee Providore.",
		AttributedTo: lmeAttributionAssistant,
	}})

	if !strings.Contains(
		prompt,
		"User was recommended Miss Bee Providore. [attributed_to=assistant]",
	) {
		t.Fatalf("assistant attribution is missing from prompt: %s", prompt)
	}
}

func TestBuildLongMemEvalAnswerPromptKnowledgeUpdateTimeline(t *testing.T) {
	inst := &lmeInstance{
		QuestionID:   "q-update",
		QuestionType: "knowledge-update",
		Question:     "What was my previous goal before I updated it?",
	}
	prompt := buildLongMemEvalAnswerPrompt(inst, nil)
	normalizedPrompt := strings.Join(strings.Fields(prompt), " ")

	if !strings.Contains(normalizedPrompt, "asks for an earlier state or the latest state") {
		t.Fatalf("missing knowledge-update state selection guidance: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt, "previous") ||
		!strings.Contains(normalizedPrompt, "before") ||
		!strings.Contains(normalizedPrompt, "do not answer with the latest/current value") {
		t.Fatalf("missing previous-state guidance: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt, "current") ||
		!strings.Contains(normalizedPrompt, "latest") ||
		!strings.Contains(normalizedPrompt, "newest supported value") {
		t.Fatalf("missing current-state guidance: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt, "recent") ||
		!strings.Contains(normalizedPrompt, "after a relocation, update, or change") {
		t.Fatalf("missing recent-state guidance: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt,
		"compare their event_time metadata before retrieval rank") ||
		!strings.Contains(normalizedPrompt,
			"temporal rule overrides the general ranked-evidence rule") {
		t.Fatalf("missing event-time precedence guidance: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt, "later event_time must override") ||
		!strings.Contains(normalizedPrompt, "undated conflicting state") ||
		!strings.Contains(normalizedPrompt, `"moved back"`) {
		t.Fatalf("missing strict latest-state guidance: %s", prompt)
	}
}

func TestRestoreLongMemEvalRawAnswerRemovesLegacyPostprocessing(t *testing.T) {
	t.Parallel()

	cr := &caseResult{Answer: "8"}
	br := &backendResult{
		RawAnswer:    "5 tomato plants and 3 cucumber plants",
		Answer:       "8",
		ExactMatch:   true,
		F1:           1,
		BLEU:         1,
		FailureStage: "ok",
	}
	restoreLongMemEvalRawAnswer(cr, br)
	if br.Answer != br.RawAnswer || br.ExactMatch || br.F1 == 1 ||
		br.FailureStage != "evidence_or_answer_miss" {
		t.Fatalf("raw answer was not restored: %+v", br)
	}

	abstention := &backendResult{
		RawAnswer:    "The memories contain an answer.",
		Answer:       "I don't know",
		FailureStage: "ok_abstention",
	}
	restoreLongMemEvalRawAnswer(&caseResult{Answer: "The information is unavailable."}, abstention)
	if abstention.FailureStage != "abstention_answered" {
		t.Fatalf("abstention stage was not restored: %+v", abstention)
	}

	unchanged := &backendResult{Answer: "kept"}
	restoreLongMemEvalRawAnswer(cr, unchanged)
	if unchanged.Answer != "kept" {
		t.Fatalf("missing raw answer changed result: %+v", unchanged)
	}
	restoreLongMemEvalRawAnswer(nil, br)
}

func TestReplaceLongMemEvalAnswerUsage(t *testing.T) {
	t.Parallel()

	br := &backendResult{
		TokenUsage: &lmeTokenUsage{
			PromptTokens:     80,
			CompletionTokens: 20,
			TotalTokens:      100,
			CachedTokens:     40,
			LLMCalls:         2,
		},
		AnswerUsage: &lmeTokenUsage{
			PromptTokens:     15,
			CompletionTokens: 5,
			TotalTokens:      20,
			CachedTokens:     5,
			LLMCalls:         1,
		},
	}
	newUsage := lmeTokenUsage{
		PromptTokens:     7,
		CompletionTokens: 2,
		TotalTokens:      9,
		LLMCalls:         1,
	}
	replaceLongMemEvalAnswerUsage(br, newUsage)

	if br.TokenUsage == nil || br.TokenUsage.PromptTokens != 72 ||
		br.TokenUsage.CompletionTokens != 17 || br.TokenUsage.TotalTokens != 89 ||
		br.TokenUsage.CachedTokens != 35 || br.TokenUsage.LLMCalls != 2 {
		t.Fatalf("unexpected replacement total: %+v", br.TokenUsage)
	}
	if br.AnswerUsage == nil || br.AnswerUsage.TotalTokens != 9 || br.AnswerUsage.LLMCalls != 1 {
		t.Fatalf("unexpected replacement answer usage: %+v", br.AnswerUsage)
	}

	clamped := lmeTokenUsage{PromptTokens: 3, TotalTokens: 4, LLMCalls: 1}
	clamped.Sub(lmeTokenUsage{PromptTokens: 5, TotalTokens: 4, LLMCalls: 2})
	if !clamped.IsZero() {
		t.Fatalf("subtraction should clamp at zero: %+v", clamped)
	}
	replaceLongMemEvalAnswerUsage(nil, newUsage)
}

func TestReanswerLongMemEvalResult(t *testing.T) {
	t.Parallel()

	result := &runResult{
		Metadata: map[string]any{
			"judge_model": "old-judge",
			"judged_at":   "yesterday",
		},
		Cases: []*caseResult{{
			QuestionID:   "q-reanswer",
			QuestionType: "single-session-user",
			Question:     "Which option?",
			Answer:       "Option B",
			BackendResults: map[string]*backendResult{
				"pgvector": reanswerTestBackend("pgvector"),
				"mem0":     reanswerTestBackend("mem0"),
			},
		}},
	}
	llm := &queuedJudgeModel{
		responses: []string{"Option B", "Option A"},
		usage: &model.Usage{
			PromptTokens:     7,
			CompletionTokens: 2,
			TotalTokens:      9,
		},
	}
	outPath := filepath.Join(t.TempDir(), "reanswered_results.json")
	if err := reanswerLongMemEvalResult(
		context.Background(), result, llm, "answer-model", "glm", nil, true, outPath,
	); err != nil {
		t.Fatalf("re-answer result: %v", err)
	}
	if llm.calls != 2 {
		t.Fatalf("model calls = %d, want 2", llm.calls)
	}
	if len(llm.requests) != 2 || strings.Contains(llm.requests[0].Messages[0].Content, "score=") {
		t.Fatalf("unexpected answer requests: %#v", llm.requests)
	}

	got, err := loadLongMemEvalResults(outPath)
	if err != nil {
		t.Fatalf("load re-answered result: %v", err)
	}
	if _, ok := got.Metadata["judge_model"]; ok {
		t.Fatalf("stale judge metadata retained: %+v", got.Metadata)
	}
	if got.Metadata["reanswer_model"] != "answer-model" || got.Metadata["reanswer_model_variant"] != "glm" {
		t.Fatalf("missing re-answer metadata: %+v", got.Metadata)
	}
	if _, ok := got.Metadata["answer_generation"]; !ok {
		t.Fatalf("missing answer generation metadata: %+v", got.Metadata)
	}
	if got.Metadata["answer_prompt_version"] != lmeAnswerPromptVersion {
		t.Fatalf("stale answer prompt version: %+v", got.Metadata)
	}
	mem0 := got.Cases[0].BackendResults["mem0"]
	pgvector := got.Cases[0].BackendResults["pgvector"]
	if mem0.Answer != "Option B" || !mem0.ExactMatch || mem0.FailureStage != "ok" || mem0.Judge != nil {
		t.Fatalf("unexpected mem0 answer: %+v", mem0)
	}
	if mem0.Error != "" || mem0.AnswerError != "" {
		t.Fatalf("stale answer error retained: %+v", mem0)
	}
	if pgvector.Answer != "Option A" || pgvector.ExactMatch ||
		pgvector.FailureStage != "evidence_or_answer_miss" ||
		pgvector.Judge != nil {
		t.Fatalf("unexpected pgvector answer: %+v", pgvector)
	}
	if mem0.TokenUsage == nil || mem0.TokenUsage.TotalTokens != 89 ||
		mem0.AnswerUsage == nil || mem0.AnswerUsage.TotalTokens != 9 {
		t.Fatalf("unexpected re-answer usage: total=%+v answer=%+v", mem0.TokenUsage, mem0.AnswerUsage)
	}
	if len(mem0.AnswerModelCalls) != 1 || mem0.AnswerModelCalls[0].Content != "Option B" ||
		len(pgvector.AnswerModelCalls) != 1 || pgvector.AnswerModelCalls[0].Content != "Option A" {
		t.Fatalf("missing re-answer model traces: mem0=%+v pgvector=%+v",
			mem0.AnswerModelCalls, pgvector.AnswerModelCalls)
	}
	if got.Summary == nil || got.Summary.BackendSummaries["mem0"].ExactMatches != 1 {
		t.Fatalf("unexpected re-answer summary: %+v", got.Summary)
	}
	if err := reanswerLongMemEvalResult(
		context.Background(), nil, llm, "", "", nil, true, outPath,
	); err == nil {
		t.Fatal("nil result should fail")
	}
}

func reanswerTestBackend(name string) *backendResult {
	return &backendResult{
		Backend:      name,
		Answer:       "legacy answer",
		RawAnswer:    "legacy answer",
		FailureStage: "answer_miss",
		AnswerError:  "old structured answer failure",
		Error:        "answer: old legacy answer failure",
		Judge:        &lmeJudgeResult{Correct: true, Raw: "VERDICT: yes"},
		Retrieval:    []memoryHit{{Memory: "Option B was selected.", Score: 0.9}},
		TokenUsage: &lmeTokenUsage{
			PromptTokens:     80,
			CompletionTokens: 20,
			TotalTokens:      100,
			LLMCalls:         2,
		},
		AnswerUsage: &lmeTokenUsage{
			PromptTokens:     15,
			CompletionTokens: 5,
			TotalTokens:      20,
			LLMCalls:         1,
		},
	}
}

func TestStripLegacyLongMemEvalAnswerErrors(t *testing.T) {
	t.Parallel()

	value := "flush: failed; answer: truncated; re-answer: timed out; " +
		"refresh answer: empty; rerank answer: invalid; search: failed"
	if got, want := stripLegacyLongMemEvalAnswerErrors(value),
		"flush: failed; search: failed"; got != want {
		t.Fatalf("stripped error = %q, want %q", got, want)
	}
	if got := classifyFailure(&lmeInstance{}, &backendResult{
		AnswerError: "truncated",
	}); got != "answer_error" {
		t.Fatalf("answer failure stage = %q, want answer_error", got)
	}
}

func TestClassifyFailurePreservesEvidenceGranularity(t *testing.T) {
	inst := &lmeInstance{}
	tests := []struct {
		name      string
		evidence  evidenceMetrics
		exact     bool
		truncated bool
		want      string
		status    string
	}{
		{
			name: "answer turn extraction miss",
			evidence: evidenceMetrics{
				HasEvidenceLabels:   true,
				HasAnswerTurnLabels: true,
			},
			want:   "extraction_turn_miss",
			status: "extraction_turn_miss",
		},
		{
			name: "answer session extraction miss",
			evidence: evidenceMetrics{
				HasEvidenceLabels: true,
			},
			want:   "extraction_session_miss",
			status: "extraction_session_miss",
		},
		{
			name: "truncated extraction evidence",
			evidence: evidenceMetrics{
				HasEvidenceLabels:            true,
				HasExtractionTrace:           true,
				ExtractionTraceRecallAny:     true,
				HasAnswerTurnLabels:          true,
				ExtractionTraceTurnRecallAny: true,
			},
			truncated: true,
			want:      "extraction_snapshot_incomplete",
			status:    "persistence_turn_miss",
		},
		{
			name: "answer turn persistence miss",
			evidence: evidenceMetrics{
				HasEvidenceLabels:            true,
				HasExtractionTrace:           true,
				ExtractionTraceRecallAny:     true,
				HasAnswerTurnLabels:          true,
				ExtractionTraceTurnRecallAny: true,
			},
			want:   "persistence_turn_miss",
			status: "persistence_turn_miss",
		},
		{
			name: "answer session persistence miss",
			evidence: evidenceMetrics{
				HasEvidenceLabels:        true,
				HasExtractionTrace:       true,
				ExtractionTraceRecallAny: true,
			},
			want:   "persistence_session_miss",
			status: "persistence_session_miss",
		},
		{
			name: "answer turn retrieval miss",
			evidence: evidenceMetrics{
				HasEvidenceLabels:    true,
				HasAnswerTurnLabels:  true,
				ExtractTurnRecallAny: true,
				ExtractRecallAny:     true,
			},
			want:   "retrieval_turn_miss",
			status: "retrieval_turn_miss",
		},
		{
			name: "answer session retrieval miss",
			evidence: evidenceMetrics{
				HasEvidenceLabels: true,
				ExtractRecallAny:  true,
			},
			want:   "retrieval_session_miss",
			status: "retrieval_session_miss",
		},
		{
			name: "content or answer miss",
			evidence: evidenceMetrics{
				HasEvidenceLabels:      true,
				HasAnswerTurnLabels:    true,
				ExtractTurnRecallAny:   true,
				ExtractRecallAny:       true,
				RetrievalTurnRecallAny: true,
				RetrievalRecallAny:     true,
				RetrievalRecallAll:     true,
			},
			want:   "evidence_or_answer_miss",
			status: "full_retrieval",
		},
		{
			name: "correct",
			evidence: evidenceMetrics{
				HasEvidenceLabels:      true,
				HasAnswerTurnLabels:    true,
				ExtractTurnRecallAny:   true,
				ExtractRecallAny:       true,
				RetrievalTurnRecallAny: true,
				RetrievalRecallAny:     true,
				RetrievalRecallAll:     true,
			},
			exact:  true,
			want:   "ok",
			status: "full_retrieval",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := classifyFailure(inst, &backendResult{
				Evidence:          &test.evidence,
				ExactMatch:        test.exact,
				SnapshotTruncated: test.truncated,
			})
			if got != test.want {
				t.Fatalf("classifyFailure() = %q, want %q", got, test.want)
			}
			if got := evidenceStatus(&test.evidence); got != test.status {
				t.Fatalf("evidenceStatus() = %q, want %q", got, test.status)
			}
		})
	}
}

func TestExtractionTraceEvidenceDistinguishesPersistence(t *testing.T) {
	t.Parallel()

	inst := &lmeInstance{
		AnswerSessionIDs: []string{"answer-session"},
		HaystackSessions: [][]lmeTurn{{{HasAnswer: true}}},
	}
	br := &backendResult{
		Backend: "pgvector",
		IngestTraces: []ingestTrace{{
			SessionID: "answer-session",
			HasAnswer: true,
			Extraction: &extractionTrace{Operations: []extractionOperation{{
				Type: extractor.OperationAdd, Memory: "answer evidence",
			}}},
		}},
	}

	evidence := computeEvidenceMetrics(inst, br, 30)
	if !evidence.HasExtractionTrace || !evidence.ExtractionTraceRecallAny ||
		!evidence.ExtractionTraceTurnRecallAny ||
		!slices.Equal(evidence.ExtractionTraceSourceSessions, []string{"answer-session"}) {
		t.Fatalf("extraction trace evidence = %+v", evidence)
	}
	br.Evidence = evidence
	if got := classifyFailure(inst, br); got != "persistence_turn_miss" {
		t.Fatalf("failure stage = %q, want persistence_turn_miss", got)
	}

	br.IngestTraces[0].Extraction.Operations = []extractionOperation{{
		Type: extractor.OperationDelete, MemoryID: "unrelated",
	}}
	br.Evidence = computeEvidenceMetrics(inst, br, 30)
	if got := classifyFailure(inst, br); got != "extraction_turn_miss" {
		t.Fatalf("failure stage = %q, want extraction_turn_miss", got)
	}

	br.Backend = "mem0"
	br.IngestTraces[0].Extraction = nil
	br.Evidence = computeEvidenceMetrics(inst, br, 30)
	if br.Evidence.HasExtractionTrace {
		t.Fatalf("Mem0 evidence claimed unavailable extraction trace: %+v", br.Evidence)
	}
	if got := classifyFailure(inst, br); got != "extraction_turn_miss" {
		t.Fatalf("conservative Mem0 stage = %q, want extraction_turn_miss", got)
	}
}

func TestAnswerTurnMemoryRetention(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	enrichedAt := createdAt.Add(time.Second)
	br := &backendResult{
		IngestTraces: []ingestTrace{
			{
				HasAnswer: true,
				NewMemories: []memorySnapshot{
					{ID: "retained", Memory: "stable answer detail"},
					{
						ID:        "mutated-before",
						Memory:    "specific answer detail",
						CreatedAt: createdAt,
					},
					{ID: "missing", Memory: "temporary answer detail"},
					{
						ID:        "enriched-before",
						Memory:    "partial answer detail",
						CreatedAt: enrichedAt,
					},
				},
			},
			{
				HasAnswer: true,
				NewMemories: []memorySnapshot{{
					ID:        "enriched-after",
					Memory:    "complete answer detail",
					CreatedAt: enrichedAt,
				}},
			},
			{
				NewMemories: []memorySnapshot{{
					ID:     "unlabeled",
					Memory: "not from an answer-bearing turn",
				}},
			},
		},
		FinalMemories: []memorySnapshot{
			{ID: "retained", Memory: "stable answer detail"},
			{
				ID:        "mutated-after",
				Memory:    "generic summary",
				CreatedAt: createdAt,
			},
			{
				ID:        "enriched-final",
				Memory:    "complete answer detail",
				CreatedAt: enrichedAt,
			},
			{ID: "unlabeled", Memory: "not from an answer-bearing turn"},
		},
	}
	evidence := computeEvidenceMetrics(&lmeInstance{}, br, 30)
	if evidence.AnswerTurnOutputMemories != 5 ||
		evidence.AnswerTurnOutputRetained != 2 ||
		evidence.AnswerTurnOutputMutated != 2 ||
		evidence.AnswerTurnOutputMissing != 1 {
		t.Fatalf("answer-turn retention counts = %+v", evidence)
	}
	if !slices.Equal(evidence.AnswerTurnOutputRetainedIDs,
		[]string{"enriched-after", "retained"}) ||
		!slices.Equal(evidence.AnswerTurnOutputMutatedIDs,
			[]string{"enriched-before", "mutated-before"}) ||
		!slices.Equal(evidence.AnswerTurnOutputMissingIDs,
			[]string{"missing"}) {
		t.Fatalf("answer-turn retention IDs = %+v", evidence)
	}
}

func TestNormalizedFailureStageMigratesLegacyStages(t *testing.T) {
	tests := []struct {
		name   string
		result *backendResult
		want   string
	}{
		{
			name: "turn extraction miss",
			result: &backendResult{
				FailureStage: "extract_miss",
				Evidence: &evidenceMetrics{
					HasAnswerTurnLabels: true,
				},
			},
			want: "extraction_turn_miss",
		},
		{
			name: "session retrieval miss",
			result: &backendResult{
				FailureStage: "retrieval_miss",
				Evidence:     &evidenceMetrics{},
			},
			want: "retrieval_session_miss",
		},
		{
			name: "answer miss",
			result: &backendResult{
				FailureStage: "answer_miss",
			},
			want: "evidence_or_answer_miss",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizedFailureStage(test.result); got != test.want {
				t.Fatalf("normalizedFailureStage() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestExactAnswerMatchUsesWholeNormalizedAnswer(t *testing.T) {
	t.Parallel()

	if exactAnswerMatch("The final total is 8 plants.", "8") {
		t.Fatal("substring numeric match should not be exact")
	}
	if !exactAnswerMatch("I don't know.", "I don't know") {
		t.Fatal("punctuation-only difference should match")
	}
	if !exactAnswerMatch("United Airlines", "united airlines") {
		t.Fatal("case-only difference should match")
	}
}

func TestBuildLongMemEvalJudgePromptUsesOfficialTaskRules(t *testing.T) {
	t.Parallel()

	preference := &caseResult{
		QuestionID:   "pref-1",
		QuestionType: "single-session-preference",
		Question:     "Any dinner ideas?",
		Answer:       "The user would prefer garden vegetables.",
	}
	prompt := buildLongMemEvalJudgePrompt(preference, "Use tomatoes from the garden.")
	if !strings.Contains(prompt, "desired personalized response") {
		t.Fatalf("preference prompt should use rubric wording: %s", prompt)
	}

	abstention := &caseResult{
		QuestionID:   "abs-1_abs",
		QuestionType: "multi-session",
		Question:     "What did I buy?",
		Answer:       "The information provided is not enough.",
	}
	prompt = buildLongMemEvalJudgePrompt(abstention, "I don't know")
	if !strings.Contains(prompt, "unanswerable") {
		t.Fatalf("abstention prompt should use unanswerable wording: %s", prompt)
	}

	temporal := &caseResult{
		QuestionID:   "time-1",
		QuestionType: "temporal-reasoning",
		Question:     "How many days ago?",
		Answer:       "18 days",
	}
	prompt = buildLongMemEvalJudgePrompt(temporal, "19 days")
	if !strings.Contains(prompt, "off-by-one") {
		t.Fatalf("temporal prompt should allow off-by-one day counts: %s", prompt)
	}

	superset := &caseResult{
		QuestionID:   "superset-1",
		QuestionType: "single-session-user",
		Question:     "What birthday gift did I buy?",
		Answer:       "A yellow dress",
	}
	prompt = buildLongMemEvalJudgePrompt(
		superset,
		"A yellow dress and matching earrings.",
	)
	if !strings.Contains(prompt, "plus additional details as correct") ||
		!strings.Contains(prompt, "absent from the reference answer") {
		t.Fatalf("fact prompt should allow non-contradictory supersets: %s", prompt)
	}
}

func TestParseLongMemEvalJudge(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		raw  string
		want bool
	}{
		{name: "yes", raw: "Analysis.\nVERDICT: yes", want: true},
		{name: "no", raw: "Analysis.\nVERDICT: no.", want: false},
		{name: "case insensitive", raw: "VERDICT: YES", want: true},
		{name: "last explicit verdict", raw: "VERDICT: no\nCorrection.\nVERDICT: yes", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseLongMemEvalJudge(test.raw)
			if err != nil {
				t.Fatalf("parse verdict: %v", err)
			}
			if got != test.want {
				t.Fatalf("verdict = %v, want %v", got, test.want)
			}
		})
	}
	for _, raw := range []string{
		"Yes.",
		"The answer is yes.",
		"The response does not satisfy the rubric.",
		"VERDICT: maybe",
	} {
		if _, err := parseLongMemEvalJudge(raw); err == nil {
			t.Fatalf("malformed response should fail: %q", raw)
		}
	}
	for _, test := range []struct {
		raw  string
		want string
	}{
		{raw: `{"correct":true}`, want: "yes"},
		{raw: `{"correct":false}`, want: "no"},
		{raw: `Analysis omitted. {"correct":false}`, want: "no"},
	} {
		got, err := parseLongMemEvalJudgeRepair(test.raw)
		if err != nil || got != test.want {
			t.Fatalf("repair verdict = %q, %v; want %q", got, err, test.want)
		}
	}
	if _, err := parseLongMemEvalJudgeRepair(`{"answer":"yes"}`); err == nil {
		t.Fatal("verbose repair response should fail")
	}
}

func TestShouldReuseLongMemEvalJudge(t *testing.T) {
	t.Parallel()

	const cacheKey = "judge-cache-key"
	valid := &backendResult{Judge: &lmeJudgeResult{
		Model: "judge-model", Raw: "VERDICT: yes", Correct: true, CacheKey: cacheKey,
		RequestedRuns: 3, ValidRuns: 3,
		Attempts: []lmeJudgeAttempt{
			{Raw: "VERDICT: yes", Correct: true},
			{Raw: "VERDICT: no"},
			{Raw: "VERDICT: yes", Correct: true},
		},
	}}
	if !shouldReuseLongMemEvalJudge(valid, "judge-model", 3, cacheKey) {
		t.Fatal("valid verdict from the same model should be reused")
	}
	for _, result := range []*backendResult{
		nil,
		{},
		{Judge: &lmeJudgeResult{Model: "other-model", Raw: "VERDICT: yes", Correct: true}},
		{Judge: &lmeJudgeResult{Model: "judge-model", Raw: "VERDICT: yes", Correct: true, Error: "failed"}},
		{Judge: &lmeJudgeResult{Model: "judge-model", Raw: "VERDICT: yes", Correct: false}},
		{Judge: &lmeJudgeResult{Model: "judge-model", Raw: "VERDICT: yes", Correct: true, RequestedRuns: 1}},
		{Judge: &lmeJudgeResult{
			Model: "judge-model", Raw: "VERDICT: yes", Correct: true, CacheKey: cacheKey,
			RequestedRuns: 3, ValidRuns: 2,
			Attempts: []lmeJudgeAttempt{
				{Raw: "VERDICT: yes", Correct: true},
				{Raw: "VERDICT: yes", Correct: true},
				{Error: "timeout"},
			},
		}},
	} {
		if shouldReuseLongMemEvalJudge(result, "judge-model", 3, cacheKey) {
			t.Fatalf("invalid or incompatible judge was reused: %#v", result)
		}
	}
	legacy := &backendResult{Judge: &lmeJudgeResult{
		Model: "judge-model", Raw: "VERDICT: no",
	}}
	if shouldReuseLongMemEvalJudge(legacy, "judge-model", 1, cacheKey) {
		t.Fatal("legacy verdict without a content-addressed key must be rejudged")
	}
}

func TestResolveLongMemEvalJudgeDoesNotCacheIncompleteConsensus(t *testing.T) {
	t.Parallel()

	cache, err := openLongMemEvalJudgeCache("")
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	llm := &queuedJudgeModel{responses: []string{
		"VERDICT: yes",
		"VERDICT: yes",
		"missing verdict",
		`{"unexpected":true}`,
		"missing verdict",
		`{"unexpected":true}`,
		"missing verdict",
		`{"unexpected":true}`,
		"VERDICT: yes",
		"VERDICT: yes",
		"VERDICT: yes",
	}}
	cr := &caseResult{
		QuestionType: "single-session-user",
		Question:     "Which option?",
		Answer:       "Option B",
	}
	answer := &backendResult{Answer: "Option B"}
	first, source, err := resolveLongMemEvalJudge(
		context.Background(), llm, "judge-model", "glm", cr, answer, 3, cache,
	)
	if err != nil {
		t.Fatalf("resolve incomplete verdict: %v", err)
	}
	if source != lmeJudgeVerdictSourceModel || first.ValidRuns != 2 || cache.Len() != 0 {
		t.Fatalf("incomplete verdict was cached: source=%q judge=%#v cache=%d", source, first, cache.Len())
	}
	identity, key, err := longMemEvalJudgeCacheKey(cr, answer.Answer, "judge-model", "glm", 3)
	if err != nil {
		t.Fatalf("build legacy cache key: %v", err)
	}
	cache.file.Entries[key] = lmeJudgeCacheEntry{Identity: identity, Judge: *first}
	if _, _, ok := cache.Lookup(key); ok {
		t.Fatal("legacy incomplete cache entry was reused")
	}
	second, source, err := resolveLongMemEvalJudge(
		context.Background(), llm, "judge-model", "glm", cr, answer, 3, cache,
	)
	if err != nil {
		t.Fatalf("resolve replacement verdict: %v", err)
	}
	if source != lmeJudgeVerdictSourceModel || second.ValidRuns != 3 || cache.Len() != 1 {
		t.Fatalf("replacement verdict = source=%q judge=%#v cache=%d", source, second, cache.Len())
	}
	if cached := cache.file.Entries[key].Judge; cached.ValidRuns != 3 {
		t.Fatalf("legacy incomplete cache entry was not replaced: %#v", cached)
	}
	if llm.calls != 11 {
		t.Fatalf("model calls = %d, want 11", llm.calls)
	}
}

func TestJudgeLongMemEvalResultFailsAfterCheckpointingIncompleteConsensus(t *testing.T) {
	t.Parallel()

	cache, err := openLongMemEvalJudgeCache("")
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	result := &runResult{Cases: []*caseResult{{
		QuestionID:   "q-incomplete",
		QuestionType: "single-session-user",
		Question:     "Which option?",
		Answer:       "Option B",
		BackendResults: map[string]*backendResult{
			"pgvector": {Backend: "pgvector", Answer: "Option B"},
		},
	}}}
	llm := &queuedJudgeModel{responses: []string{
		"VERDICT: yes",
		"VERDICT: yes",
		"missing verdict",
		`{"unexpected":true}`,
		"missing verdict",
		`{"unexpected":true}`,
		"missing verdict",
		`{"unexpected":true}`,
	}}
	outPath := filepath.Join(t.TempDir(), "judged_results.json")
	err = judgeLongMemEvalResult(
		context.Background(), result, llm, "judge-model", "glm", 3, cache, outPath,
	)
	if err == nil || !strings.Contains(err.Error(), "q-incomplete/pgvector") {
		t.Fatalf("incomplete consensus error = %v", err)
	}
	loaded, loadErr := loadLongMemEvalResults(outPath)
	if loadErr != nil {
		t.Fatalf("load incomplete checkpoint: %v", loadErr)
	}
	judge := loaded.Cases[0].BackendResults["pgvector"].Judge
	if judge == nil || judge.ValidRuns != 2 || cache.Len() != 0 {
		t.Fatalf("incomplete checkpoint = judge=%#v cache=%d", judge, cache.Len())
	}
}

func TestResolveLongMemEvalJudgeDeduplicatesIdenticalInputs(t *testing.T) {
	t.Parallel()

	cache, err := openLongMemEvalJudgeCache("")
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	llm := &queuedJudgeModel{
		responses: []string{"VERDICT: yes"},
		usage: &model.Usage{
			PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12,
		},
	}
	cr := &caseResult{
		QuestionType: "single-session-user",
		Question:     "What did I wear?",
		Answer:       "A yellow dress",
	}
	first, source, err := resolveLongMemEvalJudge(
		context.Background(), llm, "judge-model", "glm", cr,
		&backendResult{Answer: "A yellow dress and matching earrings"}, 1, cache,
	)
	if err != nil {
		t.Fatalf("resolve first verdict: %v", err)
	}
	if source != lmeJudgeVerdictSourceModel || first.TokenUsage == nil {
		t.Fatalf("first verdict source or usage = %q, %#v", source, first.TokenUsage)
	}
	second, source, err := resolveLongMemEvalJudge(
		context.Background(), llm, "judge-model", "glm", cr,
		&backendResult{Answer: "A yellow dress and matching earrings"}, 1, cache,
	)
	if err != nil {
		t.Fatalf("resolve duplicate verdict: %v", err)
	}
	if llm.calls != 1 {
		t.Fatalf("model calls = %d, want one shared call", llm.calls)
	}
	if source != lmeJudgeVerdictSourceCurrentRun || second.VerdictSource != source {
		t.Fatalf("duplicate verdict source = %q, result=%q", source, second.VerdictSource)
	}
	if second.Correct != first.Correct || second.Raw != first.Raw ||
		second.CacheKey != first.CacheKey {
		t.Fatalf("duplicate verdict differs: first=%#v second=%#v", first, second)
	}
	if second.TokenUsage != nil || second.DurationMs != 0 ||
		len(second.Attempts) != 1 || second.Attempts[0].TokenUsage != nil ||
		second.LogicalTokenUsage == nil ||
		second.LogicalTokenUsage.TotalTokens != 12 ||
		!second.LogicalUsageComplete ||
		second.Attempts[0].LogicalTokenUsage == nil ||
		second.Attempts[0].LogicalTokenUsage.TotalTokens != 12 ||
		len(second.Attempts[0].ModelCalls) != 1 ||
		second.Attempts[0].ModelCalls[0].Source !=
			lmeJudgeVerdictSourceCurrentRun {
		t.Fatalf("cache reuse double-counted model work: %#v", second)
	}
}

func TestJudgeLongMemEvalResultSharesVerdictsAndCheckpoints(t *testing.T) {
	t.Parallel()

	cache, err := openLongMemEvalJudgeCache(
		filepath.Join(t.TempDir(), "judge-cache.json"),
	)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	result := &runResult{Cases: []*caseResult{{
		QuestionID:   "q-shared",
		QuestionType: "single-session-user",
		Question:     "What did I wear?",
		Answer:       "A yellow dress",
		BackendResults: map[string]*backendResult{
			"empty":    {Backend: "empty"},
			"mem0":     {Backend: "mem0", Answer: "A yellow dress and matching earrings"},
			"pgvector": {Backend: "pgvector", Answer: "A yellow dress and matching earrings"},
		},
	}}}
	llm := &queuedJudgeModel{
		responses: []string{"VERDICT: yes"},
		usage: &model.Usage{
			PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12,
		},
	}
	outPath := filepath.Join(t.TempDir(), "judged_results.json")
	if err := judgeLongMemEvalResult(
		context.Background(), result, llm, "judge-model", "glm", 1, cache, outPath,
	); err != nil {
		t.Fatalf("judge result: %v", err)
	}
	if llm.calls != 1 {
		t.Fatalf("model calls = %d, want one content-addressed verdict", llm.calls)
	}
	backends := result.Cases[0].BackendResults
	if backends["empty"].Judge == nil || backends["empty"].Judge.Error != "missing answer" {
		t.Fatalf("missing answer verdict = %#v", backends["empty"].Judge)
	}
	if backends["mem0"].Judge == nil || !backends["mem0"].Judge.Correct ||
		backends["mem0"].Judge.VerdictSource != lmeJudgeVerdictSourceModel {
		t.Fatalf("mem0 verdict = %#v", backends["mem0"].Judge)
	}
	if backends["pgvector"].Judge == nil || !backends["pgvector"].Judge.Correct ||
		backends["pgvector"].Judge.VerdictSource != lmeJudgeVerdictSourceCurrentRun ||
		backends["pgvector"].Judge.TokenUsage != nil {
		t.Fatalf("pgvector shared verdict = %#v", backends["pgvector"].Judge)
	}
	if result.Metadata["judge_protocol_version"] != lmeJudgeProtocolVersion ||
		result.Metadata["judge_cache_ledger_id"] != cache.LedgerID() ||
		result.Metadata["judge_cache_initial_entries"] != 0 ||
		result.Metadata["judge_cache_final_entries"] != 1 ||
		result.Metadata["judge_cache_hits"] != 1 {
		t.Fatalf("judge cache metadata = %#v", result.Metadata)
	}
	if result.Summary == nil || result.Summary.JudgeTokenUsage.LLMCalls != 1 ||
		result.Summary.JudgeTokenUsage.TotalTokens != 12 {
		t.Fatalf("judge summary double-counted cache reuse: %#v", result.Summary)
	}
	loaded, err := loadLongMemEvalResults(outPath)
	if err != nil {
		t.Fatalf("load judge checkpoint: %v", err)
	}
	if loaded.Metadata["judge_cache_ledger_id"] != cache.LedgerID() ||
		loaded.Cases[0].BackendResults["pgvector"].Judge.CacheKey == "" {
		t.Fatalf("judge checkpoint provenance = %#v", loaded)
	}
}

func TestJudgeLongMemEvalResultValidatesDependencies(t *testing.T) {
	t.Parallel()

	cache, err := openLongMemEvalJudgeCache("")
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	llm := &queuedJudgeModel{}
	outPath := filepath.Join(t.TempDir(), "judged_results.json")
	for _, test := range []struct {
		name      string
		result    *runResult
		llm       model.Model
		cache     *longMemEvalJudgeCache
		wantError string
	}{
		{name: "nil result", llm: llm, cache: cache, wantError: "result is nil"},
		{name: "nil model", result: &runResult{}, cache: cache, wantError: "model is nil"},
		{name: "nil cache", result: &runResult{}, llm: llm, wantError: "cache is nil"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := judgeLongMemEvalResult(
				context.Background(), test.result, test.llm,
				"judge-model", "glm", 1, test.cache, outPath,
			)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("dependency validation error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestLongMemEvalJudgeCachePersistsAndInvalidatesByAnswer(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "judge-cache.json")
	cache, err := openLongMemEvalJudgeCache(path)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	if cache.LedgerID() == "" {
		t.Fatal("persistent cache is missing a ledger id")
	}
	cr := &caseResult{
		QuestionType: "single-session-user",
		Question:     "Which option?",
		Answer:       "Option B",
	}
	usage := &model.Usage{
		PromptTokens: 20, CompletionTokens: 2, TotalTokens: 22,
	}
	firstLLM := &queuedJudgeModel{
		responses: []string{"VERDICT: yes"},
		usage:     usage,
	}
	first, _, err := resolveLongMemEvalJudge(
		context.Background(), firstLLM, "judge-model", "glm", cr,
		&backendResult{Answer: "Option B"}, 1, cache,
	)
	if err != nil {
		t.Fatalf("resolve persisted verdict: %v", err)
	}
	if first.TokenUsage == nil || first.TokenUsage.TotalTokens != 22 ||
		first.LogicalTokenUsage == nil ||
		first.LogicalTokenUsage.TotalTokens != 22 ||
		!first.LogicalUsageComplete ||
		len(first.Attempts) != 1 ||
		len(first.Attempts[0].ModelCalls) != 1 {
		t.Fatalf("first verdict usage = %#v", first)
	}
	loaded, err := openLongMemEvalJudgeCache(path)
	if err != nil {
		t.Fatalf("reload cache: %v", err)
	}
	if loaded.LedgerID() != cache.LedgerID() {
		t.Fatalf("ledger id changed after reload: got %q, want %q", loaded.LedgerID(), cache.LedgerID())
	}
	secondLLM := &queuedJudgeModel{responses: []string{"VERDICT: no"}}
	second, source, err := resolveLongMemEvalJudge(
		context.Background(), secondLLM, "judge-model", "glm", cr,
		&backendResult{Answer: "Option B"}, 1, loaded,
	)
	if err != nil {
		t.Fatalf("resolve loaded verdict: %v", err)
	}
	if source != lmeJudgeVerdictSourcePersistent || secondLLM.calls != 0 ||
		second.Correct != first.Correct {
		t.Fatalf("persistent cache miss: source=%q calls=%d result=%#v", source, secondLLM.calls, second)
	}
	if second.TokenUsage != nil || second.LogicalTokenUsage == nil ||
		second.LogicalTokenUsage.TotalTokens != 22 ||
		!second.LogicalUsageComplete ||
		len(second.Attempts) != 1 ||
		second.Attempts[0].TokenUsage != nil ||
		second.Attempts[0].LogicalTokenUsage == nil ||
		second.Attempts[0].LogicalTokenUsage.TotalTokens != 22 ||
		len(second.Attempts[0].ModelCalls) != 1 ||
		second.Attempts[0].ModelCalls[0].Source !=
			lmeJudgeVerdictSourcePersistent {
		t.Fatalf("cached verdict usage = %#v", second)
	}
	if loaded.logicalUsageHits != 1 ||
		loaded.logicalUsageMissingHits != 0 {
		t.Fatalf(
			"logical cache counters: hits=%d missing=%d",
			loaded.logicalUsageHits, loaded.logicalUsageMissingHits,
		)
	}
	third, source, err := resolveLongMemEvalJudge(
		context.Background(), secondLLM, "judge-model", "glm", cr,
		&backendResult{Answer: "Option A"}, 1, loaded,
	)
	if err != nil {
		t.Fatalf("resolve changed answer: %v", err)
	}
	if source != lmeJudgeVerdictSourceModel || secondLLM.calls != 1 || third.CacheKey == first.CacheKey {
		t.Fatalf("changed answer reused stale verdict: source=%q calls=%d result=%#v", source, secondLLM.calls, third)
	}
	entry := loaded.file.Entries[first.CacheKey]
	tamperedJudge := entry.Judge
	tamperedUsage := *tamperedJudge.LogicalTokenUsage
	tamperedUsage.TotalTokens++
	tamperedJudge.LogicalTokenUsage = &tamperedUsage
	entry.Judge = tamperedJudge
	if err := validateLongMemEvalJudgeCacheEntry(
		first.CacheKey, entry,
	); err == nil ||
		!strings.Contains(err.Error(), "does not match attempts") {
		t.Fatalf("tampered logical usage error = %v", err)
	}
}

func TestResolveLongMemEvalJudgeReusesExistingKeyedVerdict(t *testing.T) {
	t.Parallel()

	cr := &caseResult{
		QuestionType: "single-session-user",
		Question:     "Which option?",
		Answer:       "Option B",
	}
	_, key, err := longMemEvalJudgeCacheKey(cr, "Option B", "judge-model", "glm", 1)
	if err != nil {
		t.Fatalf("build cache key: %v", err)
	}
	br := &backendResult{
		Answer: "Option B",
		Judge: &lmeJudgeResult{
			Model:         "judge-model",
			Correct:       true,
			Raw:           "VERDICT: yes",
			CacheKey:      key,
			RequestedRuns: 1,
			ValidRuns:     1,
		},
	}
	cache, err := openLongMemEvalJudgeCache("")
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	llm := &queuedJudgeModel{responses: []string{"VERDICT: no"}}
	judge, source, err := resolveLongMemEvalJudge(
		context.Background(), llm, "judge-model", "glm", cr, br, 1, cache,
	)
	if err != nil {
		t.Fatalf("resolve existing verdict: %v", err)
	}
	if source != lmeJudgeVerdictSourceExisting || llm.calls != 0 || judge != br.Judge {
		t.Fatalf("existing verdict was not reused: source=%q calls=%d judge=%#v", source, llm.calls, judge)
	}
	if judge.VerdictSource != lmeJudgeVerdictSourceExisting || cache.Len() != 1 {
		t.Fatalf("existing verdict was not recorded in cache: judge=%#v entries=%d", judge, cache.Len())
	}
	identity, _, err := longMemEvalJudgeCacheKey(cr, "Option B", "judge-model", "glm", 1)
	if err != nil {
		t.Fatalf("rebuild cache identity: %v", err)
	}
	if err := cache.Put(key, identity, judge); err != nil || cache.Len() != 1 {
		t.Fatalf("duplicate cache put = %v, entries=%d", err, cache.Len())
	}
}

func TestOpenLongMemEvalJudgeCacheRejectsInvalidFiles(t *testing.T) {
	t.Parallel()

	write := func(t *testing.T, name string, value any) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), name)
		var data []byte
		if raw, ok := value.([]byte); ok {
			data = raw
		} else {
			var err error
			data, err = json.Marshal(value)
			if err != nil {
				t.Fatalf("marshal cache fixture: %v", err)
			}
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatalf("write cache fixture: %v", err)
		}
		return path
	}

	for _, test := range []struct {
		name      string
		value     any
		wantError string
	}{
		{
			name:      "invalid json",
			value:     []byte("{"),
			wantError: "parse LongMemEval judge cache",
		},
		{
			name: "legacy version",
			value: lmeJudgeCacheFile{
				Version:  "lme-judge-cache-v1",
				LedgerID: "ledger",
				Entries:  map[string]lmeJudgeCacheEntry{},
			},
			wantError: "unsupported LongMemEval judge cache version",
		},
		{
			name: "unsupported version",
			value: lmeJudgeCacheFile{
				Version:  "future-version",
				LedgerID: "ledger",
				Entries:  map[string]lmeJudgeCacheEntry{},
			},
			wantError: "unsupported LongMemEval judge cache version",
		},
		{
			name: "missing ledger id",
			value: lmeJudgeCacheFile{
				Version: lmeJudgeCacheFormatVersion,
				Entries: map[string]lmeJudgeCacheEntry{},
			},
			wantError: "missing ledger_id",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := write(t, "judge-cache.json", test.value)
			_, err := openLongMemEvalJudgeCache(path)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("open invalid cache error = %v, want %q", err, test.wantError)
			}
		})
	}

	path := filepath.Join(t.TempDir(), "judge-cache.json")
	cache, err := openLongMemEvalJudgeCache(path)
	if err != nil {
		t.Fatalf("create valid cache: %v", err)
	}
	cr := &caseResult{
		QuestionType: "single-session-user",
		Question:     "Which option?",
		Answer:       "Option B",
	}
	if _, _, err := resolveLongMemEvalJudge(
		context.Background(),
		&queuedJudgeModel{responses: []string{"VERDICT: yes"}},
		"judge-model",
		"glm",
		cr,
		&backendResult{Answer: "Option B"},
		1,
		cache,
	); err != nil {
		t.Fatalf("populate valid cache: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read valid cache: %v", err)
	}
	var file lmeJudgeCacheFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("parse valid cache: %v", err)
	}
	for key, entry := range file.Entries {
		delete(file.Entries, key)
		file.Entries["tampered-key"] = entry
		break
	}
	tampered := write(t, "tampered-cache.json", file)
	if _, err := openLongMemEvalJudgeCache(tampered); err == nil ||
		!strings.Contains(err.Error(), "key mismatch") {
		t.Fatalf("tampered cache error = %v", err)
	}
}

func TestLongMemEvalJudgeCacheNilHelpers(t *testing.T) {
	t.Parallel()

	var cache *longMemEvalJudgeCache
	if cache.Persistent() || cache.LedgerID() != "" || cache.Len() != 0 || cache.Hits() != 0 {
		t.Fatalf("nil cache helpers returned non-zero values")
	}
	if judge, source, ok := cache.Lookup("missing"); ok || judge != nil || source != "" {
		t.Fatalf("nil cache lookup = %#v, %q, %v", judge, source, ok)
	}
	if err := cache.Put("", lmeJudgeCacheIdentity{}, nil); err != nil {
		t.Fatalf("nil cache put: %v", err)
	}
	metadata := map[string]any{}
	updateLongMemEvalJudgeCacheMetadata(metadata, cache)
	if metadata["judge_cache_final_entries"] != 0 || metadata["judge_cache_hits"] != 0 {
		t.Fatalf("nil cache metadata = %#v", metadata)
	}
	updateLongMemEvalJudgeCacheMetadata(nil, cache)
}

func TestClearLongMemEvalJudgeRunMetadata(t *testing.T) {
	t.Parallel()

	metadata := map[string]any{
		"judge_model":                            "judge-model",
		"judge_cache_format_version":             lmeJudgeCacheFormatVersion,
		"judge_cache_shared":                     true,
		"judge_cache_ledger_id":                  "ledger",
		"judge_cache_initial_entries":            1,
		"judge_cache_final_entries":              2,
		"judge_cache_hits":                       1,
		"judge_cache_logical_usage_hits":         1,
		"judge_cache_logical_usage_missing_hits": 0,
		"judged_at":                              "now",
		"judge_prompt_version":                   lmeJudgePromptVersion,
		"judge_protocol_version":                 lmeJudgeProtocolVersion,
	}
	clearLongMemEvalJudgeRunMetadata(metadata)
	for _, key := range []string{
		"judge_model",
		"judge_cache_format_version",
		"judge_cache_shared",
		"judge_cache_ledger_id",
		"judge_cache_initial_entries",
		"judge_cache_final_entries",
		"judge_cache_hits",
		"judge_cache_logical_usage_hits",
		"judge_cache_logical_usage_missing_hits",
		"judged_at",
	} {
		if _, ok := metadata[key]; ok {
			t.Fatalf("stale judge run metadata %q was retained: %#v", key, metadata)
		}
	}
	if metadata["judge_prompt_version"] != lmeJudgePromptVersion ||
		metadata["judge_protocol_version"] != lmeJudgeProtocolVersion {
		t.Fatalf("judge contract metadata was cleared: %#v", metadata)
	}
}

func TestJudgeLongMemEvalConsensusMajority(t *testing.T) {
	t.Parallel()

	llm := &queuedJudgeModel{
		responses: []string{"VERDICT: yes", "VERDICT: no", "VERDICT: yes"},
		usage: &model.Usage{
			PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12,
		},
	}
	judge := judgeLongMemEvalConsensus(
		context.Background(), llm, "judge-model", &caseResult{
			QuestionType: "single-session-user",
			Question:     "Which option?",
			Answer:       "Option B",
		}, "Option B", 3,
	)
	if judge.Error != "" || !judge.Correct || judge.ValidRuns != 3 {
		t.Fatalf("unexpected consensus: %#v", judge)
	}
	if judge.RequestedRuns != 3 || judge.MaxAttempts != 5 || len(judge.Attempts) != 3 {
		t.Fatalf("unexpected attempts: %#v", judge)
	}
	if judge.TokenUsage == nil || judge.TokenUsage.LLMCalls != 3 ||
		judge.TokenUsage.TotalTokens != 36 {
		t.Fatalf("unexpected usage: %#v", judge.TokenUsage)
	}
}

func TestJudgeLongMemEvalConsensusRequiresStrictMajority(t *testing.T) {
	t.Parallel()

	llm := &queuedJudgeModel{responses: []string{
		"VERDICT: yes",
		"VERDICT: no",
		"missing verdict",
		`{"unexpected":true}`,
	}}
	judge := judgeLongMemEvalConsensus(
		context.Background(), llm, "judge-model", &caseResult{
			QuestionType: "single-session-user",
			Question:     "Which option?",
			Answer:       "Option B",
		}, "Option B", 3,
	)
	if judge.Error == "" || judge.ValidRuns != 2 {
		t.Fatalf("expected no strict majority: %#v", judge)
	}
	if len(judge.Attempts) != 5 || judge.Attempts[2].Error == "" {
		t.Fatalf("expected failed vote to be retained: %#v", judge.Attempts)
	}
}

func TestJudgeLongMemEvalConsensusRetriesUntilRequestedValidVotes(t *testing.T) {
	t.Parallel()

	llm := &queuedJudgeModel{responses: []string{
		"VERDICT: yes",
		"missing verdict",
		`{"unexpected":true}`,
		"VERDICT: yes",
		"VERDICT: yes",
	}}
	judge := judgeLongMemEvalConsensus(
		context.Background(), llm, "judge-model", &caseResult{
			QuestionType: "single-session-user",
			Question:     "Which option?",
			Answer:       "Option B",
		}, "Option B", 3,
	)
	if judge.Error != "" || !judge.Correct || judge.ValidRuns != 3 {
		t.Fatalf("retried consensus = %#v", judge)
	}
	if judge.MaxAttempts != 5 || len(judge.Attempts) != 4 ||
		judge.Attempts[1].Error == "" || llm.calls != 5 {
		t.Fatalf("retry attempts = judge=%#v calls=%d", judge, llm.calls)
	}
}

func TestJudgeRetryPolicyIsExecutionProvenance(t *testing.T) {
	t.Parallel()

	protocolGeneration := currentLongMemEvalProtocolJudgeGeneration()
	executionGeneration := currentLongMemEvalJudgeGeneration()
	if protocolGeneration.MaxExtraAttempts != 0 {
		t.Fatalf("replay protocol records execution retry policy: %#v", protocolGeneration)
	}
	if executionGeneration.MaxExtraAttempts != lmeJudgeMaxExtraAttempts {
		t.Fatalf("judge execution retry policy = %#v", executionGeneration)
	}
	protocolGeneration.MaxExtraAttempts = lmeJudgeMaxExtraAttempts
	if protocolGeneration != executionGeneration {
		t.Fatalf("judge generations differ beyond retry policy: protocol=%#v execution=%#v",
			protocolGeneration, executionGeneration)
	}
}

func TestLongMemEvalJudgeCorrectRequiresValidatedRaw(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		result    *backendResult
		want      bool
		available bool
	}{
		{name: "missing judge", result: &backendResult{}},
		{name: "judge error", result: &backendResult{Judge: &lmeJudgeResult{Raw: "VERDICT: yes", Correct: true, Error: "failed"}}},
		{name: "legacy heuristic", result: &backendResult{Judge: &lmeJudgeResult{Raw: "The response is correct.", Correct: true}}},
		{name: "mismatched saved value", result: &backendResult{Judge: &lmeJudgeResult{Raw: "VERDICT: yes", Correct: false}}},
		{name: "valid yes", result: &backendResult{Judge: &lmeJudgeResult{Raw: "VERDICT: yes", Correct: true}}, want: true, available: true},
		{name: "valid no", result: &backendResult{Judge: &lmeJudgeResult{Raw: "VERDICT: no", Correct: false}}, available: true},
		{name: "incomplete consensus", result: &backendResult{Judge: &lmeJudgeResult{
			Raw: "VERDICT: yes", Correct: true, RequestedRuns: 3, ValidRuns: 2,
			Attempts: []lmeJudgeAttempt{{Raw: "VERDICT: yes", Correct: true}},
		}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, available := longMemEvalJudgeCorrect(test.result)
			if got != test.want || available != test.available {
				t.Fatalf("longMemEvalJudgeCorrect() = (%v, %v), want (%v, %v)", got, available, test.want, test.available)
			}
		})
	}
}

func TestJudgeLongMemEvalAnswerRepairsMissingVerdict(t *testing.T) {
	t.Parallel()

	llm := &queuedJudgeModel{responses: []string{
		"The answer matches the reference, but the final line is missing.",
		`{"correct":true}`,
	}}
	raw, err := judgeLongMemEvalAnswer(context.Background(), llm, &caseResult{
		QuestionType: "single-session-user",
		Question:     "Which option?",
		Answer:       "Option B",
	}, "Option B")
	if err != nil {
		t.Fatalf("judge answer: %v", err)
	}
	if llm.calls != 2 {
		t.Fatalf("model calls = %d, want 2", llm.calls)
	}
	if len(llm.requests) != 2 || llm.requests[1].StructuredOutput == nil {
		t.Fatalf("repair request should require structured output: %#v", llm.requests)
	}
	if llm.requests[0].MaxTokens == nil || *llm.requests[0].MaxTokens != lmeJudgePrimaryMaxTokens {
		t.Fatalf("primary judge max tokens = %v, want %d", llm.requests[0].MaxTokens, lmeJudgePrimaryMaxTokens)
	}
	if llm.requests[1].MaxTokens == nil || *llm.requests[1].MaxTokens != lmeJudgeRepairMaxTokens {
		t.Fatalf("repair judge max tokens = %v, want %d", llm.requests[1].MaxTokens, lmeJudgeRepairMaxTokens)
	}
	responseFormat, ok := llm.requests[1].ExtraFields["response_format"].(map[string]string)
	if !ok || responseFormat["type"] != "json_object" {
		t.Fatalf("repair response format = %#v", llm.requests[1].ExtraFields)
	}
	if got := llm.requests[1].Messages[1].Content; !strings.Contains(got, `{"correct":true}`) {
		t.Fatalf("repair request does not require compact JSON: %q", got)
	}
	correct, err := parseLongMemEvalJudge(raw)
	if err != nil || !correct {
		t.Fatalf("repaired verdict = %v, %v; raw=%q", correct, err, raw)
	}
}

func TestBuildLongMemEvalSummaryIncludesJudgeMetrics(t *testing.T) {
	t.Parallel()

	result := buildLongMemEvalSummary([]*caseResult{{
		BackendResults: map[string]*backendResult{
			"pgvector": {
				Backend:      "pgvector",
				AnswerSource: lmeAnswerSourceModel,
				AnswerUsage: &lmeTokenUsage{
					LLMCalls:    1,
					TotalTokens: 7,
				},
				AnswerLogicalUsage: &lmeTokenUsage{
					LLMCalls:    1,
					TotalTokens: 7,
				},
				Judge: &lmeJudgeResult{
					Correct: true,
					Raw:     "VERDICT: yes",
					TokenUsage: &lmeTokenUsage{
						LLMCalls:    1,
						TotalTokens: 11,
					},
					LogicalTokenUsage: &lmeTokenUsage{
						LLMCalls:    1,
						TotalTokens: 11,
					},
					LogicalUsageComplete: true,
				},
			},
			"mem0": {
				Backend:      "mem0",
				AnswerSource: lmeAnswerSourcePersistent,
				AnswerLogicalUsage: &lmeTokenUsage{
					LLMCalls:    1,
					TotalTokens: 8,
				},
				Judge: &lmeJudgeResult{
					Correct: false,
					Raw:     "VERDICT: no",
					TokenUsage: &lmeTokenUsage{
						LLMCalls:    1,
						TotalTokens: 9,
					},
					LogicalTokenUsage: &lmeTokenUsage{
						LLMCalls:    1,
						TotalTokens: 9,
					},
					LogicalUsageComplete: true,
				},
			},
		},
	}})
	if result.BackendSummaries["pgvector"].JudgedCases != 1 ||
		result.BackendSummaries["pgvector"].JudgeCorrect != 1 {
		t.Fatalf("unexpected pgvector judge summary: %+v", result.BackendSummaries["pgvector"])
	}
	if result.BackendSummaries["mem0"].JudgedCases != 1 ||
		result.BackendSummaries["mem0"].JudgeCorrect != 0 {
		t.Fatalf("unexpected mem0 judge summary: %+v", result.BackendSummaries["mem0"])
	}
	if result.JudgeTokenUsage.LLMCalls != 2 || result.JudgeTokenUsage.TotalTokens != 20 {
		t.Fatalf("unexpected judge token usage: %+v", result.JudgeTokenUsage)
	}
	if result.AnswerTokenUsage.TotalTokens != 7 ||
		result.AnswerLogicalTokenUsage.TotalTokens != 15 ||
		result.AnswerLogicalUsageCases != 2 ||
		result.AnswerLogicalUsageMissingCases != 0 {
		t.Fatalf("unexpected answer usage summary: %+v", result)
	}
	if result.JudgeLogicalTokenUsage.TotalTokens != 20 ||
		result.JudgeLogicalUsageCases != 2 ||
		result.JudgeLogicalUsageMissingCases != 0 {
		t.Fatalf("unexpected judge logical usage summary: %+v", result)
	}
}

func TestHitsFromEntriesIncludesEpisodicMetadata(t *testing.T) {
	t.Parallel()

	eventTime := time.Date(2023, 3, 4, 0, 0, 0, 0, time.UTC)
	entries := []*memory.Entry{{
		ID: "mem-1",
		Memory: &memory.Memory{
			Memory:       "Visited the Natural History Museum on 2023-03-04.",
			Topics:       []string{"museum", "family outing"},
			Kind:         memory.KindEpisode,
			EventTime:    &eventTime,
			Participants: []string{"niece"},
			Location:     "Natural History Museum",
		},
		Score: 0.7,
	}}

	hits := hitsFromEntries(entries)
	if len(hits) != 1 {
		t.Fatalf("unexpected hit count: got %d", len(hits))
	}
	hit := hits[0]
	if hit.Kind != "episode" {
		t.Fatalf("missing kind: %+v", hit)
	}
	if hit.EventTime != "2023-03-04" {
		t.Fatalf("missing event_time: %+v", hit)
	}
	if strings.Join(hit.Topics, ",") != "museum,family outing" {
		t.Fatalf("missing topics: %+v", hit)
	}
	if strings.Join(hit.Participants, ",") != "niece" {
		t.Fatalf("missing participants: %+v", hit)
	}
	if hit.Location != "Natural History Museum" {
		t.Fatalf("missing location: %+v", hit)
	}
	if hit.AttributedTo != "" {
		t.Fatalf("unexpected generic attribution: %+v", hit)
	}

	attributed := defaultHitAttribution(
		[]memoryHit{
			hit,
			{
				Memory: "Assistant result: Recommended the museum.",
				AttributedTo: attributionFromMemoryText(
					"Assistant result: Recommended the museum.",
				),
			},
		},
		lmeAttributionUser,
	)
	if attributed[0].AttributedTo != lmeAttributionUser ||
		attributed[1].AttributedTo != lmeAttributionAssistant {
		t.Fatalf("default attribution = %+v", attributed)
	}
}

func TestUsageMissingCaseCountsAlwaysMarshal(t *testing.T) {
	t.Parallel()

	for name, value := range map[string]any{
		"run summary":     runSummary{},
		"backend summary": backendSummary{},
	} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("%s: marshal: %v", name, err)
		}
		got := string(encoded)
		for _, field := range []string{
			`"answer_logical_usage_missing_cases":0`,
			`"judge_logical_usage_missing_cases":0`,
		} {
			if !strings.Contains(got, field) {
				t.Fatalf("%s: missing explicit zero field %s in %s",
					name, field, got)
			}
		}
	}
}

func TestDiffSnapshotsIncludesMetadataOnlyChanges(t *testing.T) {
	t.Parallel()

	before := []memorySnapshot{{
		ID: "mem-1", Memory: "Had Domino's Pizza three times.",
		Topics: []string{"pizza"}, Kind: "episode",
	}}
	after := append([]memorySnapshot(nil), before...)
	after[0].Topics = []string{"pizza", "food delivery"}

	got := diffSnapshots(before, after)
	if len(got) != 1 || len(got[0].Topics) != 2 {
		t.Fatalf("metadata-only change was not captured: %+v", got)
	}
}

func TestRunCaseBackendKeepsAnswerProvenanceOnProducingPair(t *testing.T) {
	oldAnswer := *flagLMEAnswer
	oldIngestWait := *flagLMEIngestWait
	oldMaxSessions := *flagLMEMaxSessions
	oldMaxPairs := *flagLMEMaxPairs
	oldTopK := *flagVectorTopK
	*flagLMEAnswer = false
	*flagLMEIngestWait = 0
	*flagLMEMaxSessions = 0
	*flagLMEMaxPairs = 0
	*flagVectorTopK = 30
	t.Cleanup(func() {
		*flagLMEAnswer = oldAnswer
		*flagLMEIngestWait = oldIngestWait
		*flagLMEMaxSessions = oldMaxSessions
		*flagLMEMaxPairs = oldMaxPairs
		*flagVectorTopK = oldTopK
	})

	instance := &lmeInstance{
		QuestionID:       "question-1",
		QuestionType:     "single-session-user",
		Question:         "What fact was retained?",
		Answer:           flexString("answer-bearing first-pair fact"),
		AnswerSessionIDs: []string{"answer-session"},
		HaystackDates:    []string{"2026/07/20 (Mon) 12:00"},
		HaystackSessionIDs: []string{
			"answer-session",
		},
		HaystackSessions: [][]lmeTurn{{
			{Role: "user", Content: "answer-bearing first-pair fact", HasAnswer: true},
			{Role: "assistant", Content: "acknowledged"},
			{Role: "user", Content: "unrelated second-pair fact"},
			{Role: "assistant", Content: "acknowledged"},
		}},
	}
	backend := &lmePairProvenanceBackend{}
	result := runCaseBackend(
		context.Background(), nil, &lmeTokenTracker{}, backend,
		instance, "run-1", "paired-scope", "glm52", "glm", nil,
	)

	if result.Error != "" {
		t.Fatalf("runCaseBackend() error = %q", result.Error)
	}
	if result.UserID != "pair-provenance-question-1-paired-scope" {
		t.Fatalf("runCaseBackend() user ID = %q", result.UserID)
	}
	if len(result.IngestTraces) != 2 ||
		len(result.IngestTraces[0].NewMemories) != 0 ||
		len(result.IngestTraces[1].NewMemories) != 1 {
		t.Fatalf("unexpected ingest traces: %+v", result.IngestTraces)
	}
	if len(result.FinalMemories) != 1 || len(result.Retrieval) != 1 {
		t.Fatalf("unexpected final state: memories=%+v retrieval=%+v",
			result.FinalMemories, result.Retrieval)
	}
	for name, item := range map[string]memorySnapshot{
		"second-pair trace": result.IngestTraces[1].NewMemories[0],
		"final snapshot":    result.FinalMemories[0],
	} {
		if item.SourceHasAnswer {
			t.Fatalf("%s inherited answer provenance: %+v", name, item)
		}
		if !slices.Equal(item.SourceSessions, []string{"answer-session"}) {
			t.Fatalf("%s has unexpected source sessions: %+v", name, item)
		}
	}
	if result.Retrieval[0].SourceHasAnswer {
		t.Fatalf("retrieval inherited answer provenance: %+v", result.Retrieval)
	}
}

func TestInheritUpdateProvenance(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 7, 17, 20, 17, 38, 0, time.UTC)
	before := []memorySnapshot{{
		ID: "old-id", Memory: "old", CreatedAt: createdAt,
	}}
	after := []memorySnapshot{{
		ID: "new-id", Memory: "new", CreatedAt: createdAt,
	}}
	provenance := map[string]map[string]bool{
		"old-id": {"answer-session": true},
	}
	answerProvenance := map[string]bool{"old-id": true}
	inheritUpdateProvenance(before, after, provenance, answerProvenance)

	annotated := annotateSnapshots(after, provenance, answerProvenance)
	if len(annotated) != 1 ||
		!annotated[0].SourceHasAnswer ||
		len(annotated[0].SourceSessions) != 1 ||
		annotated[0].SourceSessions[0] != "answer-session" {
		t.Fatalf("stable snapshot provenance was not inherited: %+v", annotated)
	}
}

func TestApplySnapshotProvenancePreservesUpdatedLineage(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 7, 17, 20, 17, 38, 0, time.UTC)
	before := []memorySnapshot{{
		ID: "old-id", Memory: "old", CreatedAt: createdAt,
	}}
	after := []memorySnapshot{{
		ID: "new-id", Memory: "new", CreatedAt: createdAt,
	}}
	provenance := map[string]map[string]bool{
		"old-id": {"answer-session": true},
	}
	answerProvenance := map[string]bool{"old-id": true}

	annotated, changed := applySnapshotProvenance(
		before, after, provenance, answerProvenance,
		[]string{"update-session"}, false,
	)
	for name, snapshots := range map[string][]memorySnapshot{
		"final": annotated,
		"trace": changed,
	} {
		if len(snapshots) != 1 || !snapshots[0].SourceHasAnswer ||
			!slices.Equal(
				snapshots[0].SourceSessions,
				[]string{"answer-session", "update-session"},
			) {
			t.Fatalf("%s snapshots lost updated lineage: %+v", name, snapshots)
		}
	}
}

func TestInheritUpdateProvenanceRequiresUniqueTarget(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 7, 17, 20, 17, 38, 0, time.UTC)
	before := []memorySnapshot{{
		ID: "old-id", Memory: "old", CreatedAt: createdAt,
	}}
	after := []memorySnapshot{
		{ID: "new-1", Memory: "same", CreatedAt: createdAt},
		{ID: "new-2", Memory: "same", CreatedAt: createdAt},
	}
	provenance := map[string]map[string]bool{
		"old-id": {"source": true},
	}
	answerProvenance := map[string]bool{"old-id": true}
	inheritUpdateProvenance(before, after, provenance, answerProvenance)

	for _, mem := range after {
		key := memoryIdentity(mem)
		if len(provenance[key]) != 0 || answerProvenance[key] {
			t.Fatalf("ambiguous target inherited provenance: %s", key)
		}
	}
}

func TestNewLongMemEvalAnswerRequestDisablesThinking(t *testing.T) {
	req := newLongMemEvalAnswerRequest("answer this")
	if len(req.Messages) != 1 || req.Messages[0].Content != "answer this" {
		t.Fatalf("unexpected messages: %+v", req.Messages)
	}
	if req.MaxTokens == nil || *req.MaxTokens != lmeAnswerPrimaryMaxTokens {
		t.Fatalf("unexpected max tokens: %v", req.MaxTokens)
	}
	if req.Temperature == nil || *req.Temperature != 0 {
		t.Fatalf("unexpected temperature: %v", req.Temperature)
	}
	if req.ThinkingEnabled == nil || *req.ThinkingEnabled {
		t.Fatalf("thinking should be explicitly disabled: %v", req.ThinkingEnabled)
	}
	if req.ReasoningEffort == nil || *req.ReasoningEffort != "low" {
		t.Fatalf("unexpected reasoning effort: %v", req.ReasoningEffort)
	}
}

func TestAnswerFromMemoriesRetriesTruncatedResponse(t *testing.T) {
	t.Parallel()

	length := "length"
	toolCalls := "tool_calls"
	llm := &queuedAnswerModel{responses: []*model.Response{
		{Choices: []model.Choice{{
			Message:      model.NewAssistantMessage("reasoning without a final answer"),
			FinishReason: &length,
		}}},
		{Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					Function: model.FunctionDefinitionParam{
						Name:      lmeAnswerRepairToolName,
						Arguments: []byte(`{"answer":"3"}`),
					},
				}},
			},
			FinishReason: &toolCalls,
		}}},
	}}
	answer, err := answerFromMemories(
		context.Background(),
		llm,
		&lmeInstance{Question: "How many tanks?"},
		[]memoryHit{{Memory: "There are three tanks."}},
	)
	if err != nil || answer != "3" {
		t.Fatalf("answer = %q, err = %v", answer, err)
	}
	if len(llm.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(llm.requests))
	}
	if llm.requests[0].MaxTokens == nil ||
		*llm.requests[0].MaxTokens != lmeAnswerPrimaryMaxTokens {
		t.Fatalf("primary max tokens = %v", llm.requests[0].MaxTokens)
	}
	if llm.requests[1].MaxTokens == nil ||
		*llm.requests[1].MaxTokens != lmeAnswerRetryMaxTokens {
		t.Fatalf("retry max tokens = %v", llm.requests[1].MaxTokens)
	}
	if llm.requests[1].StructuredOutput != nil {
		t.Fatalf("retry request should use a forced tool: %#v",
			llm.requests[1].StructuredOutput)
	}
	answerTool, ok := llm.requests[1].Tools[lmeAnswerRepairToolName]
	if !ok || answerTool.Declaration().InputSchema == nil ||
		!slices.Equal(answerTool.Declaration().InputSchema.Required,
			[]string{"answer"}) {
		t.Fatalf("retry answer tool = %#v", llm.requests[1].Tools)
	}
	toolChoice, ok := llm.requests[1].
		ExtraFields["tool_choice"].(map[string]any)
	if !ok || toolChoice["type"] != "function" {
		t.Fatalf("retry tool choice = %#v", llm.requests[1].ExtraFields)
	}
	if len(llm.requests[1].Messages) != 2 ||
		llm.requests[1].Messages[0].Role != model.RoleSystem ||
		!strings.Contains(llm.requests[1].Messages[0].Content,
			lmeAnswerRepairToolName) ||
		!strings.Contains(llm.requests[1].Messages[1].Content,
			"How many tanks?") ||
		!strings.Contains(llm.requests[1].Messages[1].Content,
			"reasoning without a final answer") ||
		!strings.Contains(llm.requests[1].Messages[1].Content,
			"at most 128 words") ||
		strings.Contains(llm.requests[1].Messages[1].Content,
			"There are three tanks.") {
		t.Fatalf("retry prompt does not enforce a concise final answer: %+v",
			llm.requests[1].Messages)
	}
}

func TestAnswerFromMemoriesRejectsMalformedStructuredRepair(t *testing.T) {
	t.Parallel()

	length := "length"
	toolCalls := "tool_calls"
	llm := &queuedAnswerModel{responses: []*model.Response{
		{Choices: []model.Choice{{
			Message:      model.NewAssistantMessage("partial"),
			FinishReason: &length,
		}}},
		{Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					Function: model.FunctionDefinitionParam{
						Name:      lmeAnswerRepairToolName,
						Arguments: []byte(`{"answer":""}`),
					},
				}},
			},
			FinishReason: &toolCalls,
		}}},
	}}
	answer, err := answerFromMemories(
		context.Background(), llm,
		&lmeInstance{Question: "Which option?"}, nil,
	)
	if answer != `{"answer":""}` ||
		!errors.Is(err, errLongMemEvalAnswerRepair) {
		t.Fatalf("answer = %q, err = %v", answer, err)
	}
	if len(llm.requests) != lmeAnswerMaxAttempts {
		t.Fatalf("requests = %d, want %d",
			len(llm.requests), lmeAnswerMaxAttempts)
	}
}

func TestAnswerFromMemoriesReturnsDeterministicTruncationError(t *testing.T) {
	t.Parallel()

	length := "length"
	llm := &queuedAnswerModel{responses: []*model.Response{
		{Choices: []model.Choice{{
			Message:      model.NewAssistantMessage("partial one"),
			FinishReason: &length,
		}}},
		{Choices: []model.Choice{{
			Message:      model.NewAssistantMessage("partial two"),
			FinishReason: &length,
		}}},
	}}
	answer, err := answerFromMemories(
		context.Background(), llm,
		&lmeInstance{Question: "Which option?"}, nil,
	)
	if answer != "partial two" ||
		!errors.Is(err, errLongMemEvalAnswerTruncated) {
		t.Fatalf("answer = %q, err = %v", answer, err)
	}
	if len(llm.requests) != lmeAnswerMaxAttempts {
		t.Fatalf("requests = %d, want %d", len(llm.requests), lmeAnswerMaxAttempts)
	}
}

func TestAnalyzeLongMemEvalResults(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	resultsPath := filepath.Join(dir, "results.json")
	result := &runResult{
		Summary: &runSummary{TotalCases: 1},
		Cases: []*caseResult{{
			QuestionID:   "q1",
			QuestionType: "single-session-preference",
			Question:     "Where did I meet Sophia?",
			Answer:       "The user would prefer coffee shops with quiet seating. They would not prefer crowded restaurants.",
			BackendResults: map[string]*backendResult{
				"pgvector": {
					Backend:      "pgvector",
					FailureStage: "answer_miss",
					Answer:       "The user likes coffee.",
					F1:           0,
					BLEU:         0,
					Evidence: &evidenceMetrics{
						HasEvidenceLabels:  true,
						ExtractRecallAny:   true,
						RetrievalRecallAny: true,
						RetrievalRecallAll: true,
					},
				},
			},
		}, {
			QuestionID:   "q2",
			QuestionType: "single-session-assistant",
			Question:     "Which beer did you recommend?",
			Answer:       "I recommended using a Pilsner or Lager.",
			BackendResults: map[string]*backendResult{
				"pgvector": {
					Backend:      "pgvector",
					FailureStage: "answer_miss",
					Answer:       "Pilsner or Lager",
					F1:           0.5,
					Judge: &lmeJudgeResult{
						Correct: true,
						Raw:     "VERDICT: yes",
					},
				},
			},
		}},
	}
	rows := longMemEvalAnalysisRows(result)
	if len(rows) != 2 {
		t.Fatalf("analysis rows = %d, want 2", len(rows))
	}
	for _, row := range rows {
		if row.QuestionID == "q2" &&
			(row.Stage != "ok" || row.RawStage != "evidence_or_answer_miss") {
			t.Fatalf("judge-aware stage not applied: %+v", row)
		}
	}
	saveLongMemEvalResults(dir, result)
	if err := analyzeLongMemEvalResults(resultsPath, dir); err != nil {
		t.Fatalf("analyze results: %v", err)
	}
	for _, name := range []string{"analysis.md", "bad_cases.tsv"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(data), "q1") {
			t.Fatalf("%s missing question id: %s", name, data)
		}
		if !strings.Contains(string(data), "missing=") {
			t.Fatalf("%s missing answer gap diagnosis: %s", name, data)
		}
		if !strings.Contains(string(data), "negative_preference") {
			t.Fatalf("%s missing preference slot diagnosis: %s", name, data)
		}
	}
	badCases, err := os.ReadFile(filepath.Join(dir, "bad_cases.tsv"))
	if err != nil {
		t.Fatalf("read bad cases: %v", err)
	}
	if strings.Contains(string(badCases), "q2") {
		t.Fatalf("judge-correct answer should not be a bad case: %s", badCases)
	}
	analysis, err := os.ReadFile(filepath.Join(dir, "analysis.md"))
	if err != nil {
		t.Fatalf("read analysis: %v", err)
	}
	if !strings.Contains(string(analysis), "1/1") {
		t.Fatalf("analysis missing judge summary: %s", analysis)
	}
}

func TestEvaluatedFailureStage(t *testing.T) {
	t.Parallel()

	abstention := &backendResult{Evidence: &evidenceMetrics{IsAbstention: true}}
	regular := &backendResult{Evidence: &evidenceMetrics{}}
	backendError := &backendResult{Error: "failed"}
	answerError := &backendResult{AnswerError: "truncated"}
	tests := []struct {
		name           string
		result         *backendResult
		raw            string
		judgeCorrect   bool
		judgeAvailable bool
		want           string
	}{
		{name: "no judge", result: regular, raw: "answer_miss", want: "answer_miss"},
		{name: "backend error", result: backendError, raw: "backend_error", judgeCorrect: true, judgeAvailable: true, want: "backend_error"},
		{name: "answer error", result: answerError, raw: "answer_error", judgeCorrect: true, judgeAvailable: true, want: "answer_error"},
		{name: "correct answer", result: regular, raw: "answer_miss", judgeCorrect: true, judgeAvailable: true, want: "ok"},
		{name: "incorrect answer", result: regular, raw: "ok", judgeAvailable: true, want: "evidence_or_answer_miss"},
		{name: "correct abstention", result: abstention, raw: "abstention_answered", judgeCorrect: true, judgeAvailable: true, want: "ok_abstention"},
		{name: "incorrect abstention", result: abstention, raw: "ok_abstention", judgeAvailable: true, want: "abstention_answered"},
		{name: "pipeline stage", result: regular, raw: "retrieval_miss", judgeCorrect: true, judgeAvailable: true, want: "retrieval_miss"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := evaluatedFailureStage(
				test.result,
				test.raw,
				test.judgeCorrect,
				test.judgeAvailable,
			)
			if got != test.want {
				t.Fatalf("evaluatedFailureStage() = %q, want %q", got, test.want)
			}
		})
	}
	cell := disagreementCell(&backendResult{
		FailureStage: "answer_miss",
		Judge:        &lmeJudgeResult{Raw: "VERDICT: yes", Correct: true},
	})
	if !strings.Contains(cell, "stage=ok") {
		t.Fatalf("disagreement cell did not use judge-aware stage: %s", cell)
	}
}

func TestCompareLongMemEvalResults(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	candidateDir := t.TempDir()
	outputDir := t.TempDir()
	basePath := filepath.Join(baseDir, "results.json")
	candidatePath := filepath.Join(candidateDir, "results.json")
	base := &runResult{
		Metadata: testLongMemEvalComparisonMetadata("upstream-main"),
		Summary:  &runSummary{TotalCases: 2},
		Cases: []*caseResult{{
			QuestionID:   "q1",
			QuestionType: "single-session-assistant",
			Question:     "What was the fifth bottle?",
			Answer:       "Absinthe",
			BackendResults: map[string]*backendResult{
				"mem0": {
					Backend:      "mem0",
					FailureStage: "ok",
					ExactMatch:   true,
					Answer:       "Absinthe",
					F1:           1,
					BLEU:         1,
				},
				"pgvector": {
					Backend:      "pgvector",
					FailureStage: "answer_miss",
					Answer:       "I don't know.",
					F1:           0,
					BLEU:         0,
				},
			},
		}, {
			QuestionID:   "q2",
			QuestionType: "single-session-assistant",
			Question:     "Which option was recommended?",
			Answer:       "Option B",
			BackendResults: map[string]*backendResult{
				"mem0": {
					Backend:      "mem0",
					FailureStage: "answer_miss",
					Answer:       "I don't know.",
				},
				"pgvector": {
					Backend:      "pgvector",
					FailureStage: "answer_miss",
					Answer:       "The recommendation was Option B.",
					F1:           0.5,
					Judge:        &lmeJudgeResult{Correct: true, Raw: "VERDICT: yes"},
				},
			},
		}},
	}
	candidate := &runResult{
		Metadata: testLongMemEvalComparisonMetadata("candidate-2196"),
		Summary:  &runSummary{TotalCases: 2},
		Cases: []*caseResult{{
			QuestionID:   "q1",
			QuestionType: "single-session-assistant",
			Question:     "What was the fifth bottle?",
			Answer:       "Absinthe",
			BackendResults: map[string]*backendResult{
				"pgvector": {
					Backend:      "pgvector",
					FailureStage: "ok",
					ExactMatch:   true,
					Answer:       "Absinthe",
					F1:           1,
					BLEU:         1,
				},
			},
		}, {
			QuestionID:   "q2",
			QuestionType: "single-session-assistant",
			Question:     "Which option was recommended?",
			Answer:       "Option B",
			BackendResults: map[string]*backendResult{
				"pgvector": {
					Backend:      "pgvector",
					FailureStage: "ok",
					ExactMatch:   true,
					Answer:       "Option B",
					F1:           1,
					BLEU:         1,
					Judge:        &lmeJudgeResult{Correct: false, Raw: "VERDICT: no"},
				},
			},
		}},
	}
	saveLongMemEvalResults(baseDir, base)
	saveLongMemEvalResults(candidateDir, candidate)

	if err := compareLongMemEvalResults(basePath, candidatePath, outputDir); err != nil {
		t.Fatalf("compare results: %v", err)
	}
	for _, name := range []string{
		"comparison.md",
		"comparison.tsv",
		"mem0_comparison.tsv",
	} {
		data, err := os.ReadFile(filepath.Join(outputDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(data)
		if !strings.Contains(text, "q1") {
			t.Fatalf("%s missing question id: %s", name, text)
		}
		if !strings.Contains(text, "+1.0000") {
			t.Fatalf("%s missing delta: %s", name, text)
		}
		if name == "comparison.md" &&
			(!strings.Contains(text, "Three-Arm Summary") ||
				!strings.Contains(text, "mem0-oss-test-revision")) {
			t.Fatalf("%s missing three-arm summary: %s", name, text)
		}
	}
	rows := compareLongMemEvalRows(
		longMemEvalAnalysisRows(base),
		longMemEvalAnalysisRows(candidate),
	)
	if len(rows) != 2 {
		t.Fatalf("comparison rows = %d, want only shared question/backend pairs", len(rows))
	}
	if summary := summarizeLongMemEvalCompareRows(rows)["mem0"]; summary != nil {
		t.Fatalf("comparison included backend missing from candidate: %#v", summary)
	}
	summary := summarizeLongMemEvalCompareRows(rows)["pgvector"]
	if summary == nil || summary.Improved != 1 || summary.Regressed != 1 {
		t.Fatalf("semantic comparison summary = %#v, want one improvement and one regression", summary)
	}
	for _, row := range rows {
		if row.QuestionID == "q2" && (!row.BaselineCorrect || row.CandidateCorrect) {
			t.Fatalf("semantic judge was not preferred for q2: %#v", row)
		}
	}
}

func TestCompareLongMemEvalResultsSupportsSingleBaselineArm(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name          string
		backend       string
		wantFile      string
		unwantedFile  string
		wantSection   string
		absentSection string
	}{
		{
			name:          "pgvector",
			backend:       "pgvector",
			wantFile:      "comparison.tsv",
			unwantedFile:  "mem0_comparison.tsv",
			wantSection:   "Upstream Pgvector vs Candidate",
			absentSection: "Mem0 vs Candidate Pgvector",
		},
		{
			name:          "mem0",
			backend:       "mem0",
			wantFile:      "mem0_comparison.tsv",
			unwantedFile:  "comparison.tsv",
			wantSection:   "Mem0 vs Candidate Pgvector",
			absentSection: "Upstream Pgvector vs Candidate",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			baseDir := t.TempDir()
			candidateDir := t.TempDir()
			outputDir := t.TempDir()
			basePath := filepath.Join(baseDir, "results.json")
			candidatePath := filepath.Join(candidateDir, "results.json")
			baseMetadata := testLongMemEvalComparisonMetadata("baseline-" + test.backend)
			if test.backend == "pgvector" {
				delete(baseMetadata, "mem0_implementation")
			}
			base := &runResult{
				Metadata: baseMetadata,
				Cases: []*caseResult{{
					QuestionID:   "q1",
					QuestionType: "single-session-assistant",
					Question:     "Which option was recommended?",
					Answer:       "Option B",
					BackendResults: map[string]*backendResult{
						test.backend: {
							Backend:      test.backend,
							FailureStage: "answer_miss",
							Answer:       "I don't know.",
						},
					},
				}},
			}
			candidate := &runResult{
				Metadata: testLongMemEvalComparisonMetadata("candidate-2196"),
				Cases: []*caseResult{{
					QuestionID:   "q1",
					QuestionType: "single-session-assistant",
					Question:     "Which option was recommended?",
					Answer:       "Option B",
					BackendResults: map[string]*backendResult{
						"pgvector": {
							Backend:      "pgvector",
							FailureStage: "ok",
							ExactMatch:   true,
							Answer:       "Option B",
							F1:           1,
							BLEU:         1,
						},
					},
				}},
			}
			saveLongMemEvalResults(baseDir, base)
			saveLongMemEvalResults(candidateDir, candidate)
			if err := os.WriteFile(
				filepath.Join(outputDir, test.unwantedFile),
				[]byte("stale"),
				0644,
			); err != nil {
				t.Fatalf("write stale %s: %v", test.unwantedFile, err)
			}

			if err := compareLongMemEvalResults(basePath, candidatePath, outputDir); err != nil {
				t.Fatalf("compare single baseline arm: %v", err)
			}
			if _, err := os.Stat(filepath.Join(outputDir, test.wantFile)); err != nil {
				t.Fatalf("stat %s: %v", test.wantFile, err)
			}
			if _, err := os.Stat(filepath.Join(outputDir, test.unwantedFile)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("stat %s error = %v, want not exist", test.unwantedFile, err)
			}
			report, err := os.ReadFile(filepath.Join(outputDir, "comparison.md"))
			if err != nil {
				t.Fatalf("read comparison.md: %v", err)
			}
			text := string(report)
			if !strings.Contains(text, "Two-Arm Summary") ||
				!strings.Contains(text, test.wantSection) {
				t.Fatalf("comparison.md missing single-arm sections: %s", text)
			}
			if strings.Contains(text, test.absentSection) {
				t.Fatalf("comparison.md contains absent section %q: %s", test.absentSection, text)
			}
		})
	}
}

func testLongMemEvalComparisonMetadata(implementation string) map[string]any {
	return map[string]any{
		"implementation":             implementation,
		"mem0_implementation":        "mem0-oss-test-revision",
		"build":                      testLongMemEvalBuildProvenance("runner-revision"),
		"judge_build":                testLongMemEvalBuildProvenance("judge-revision"),
		"dataset_sha256":             "dataset-digest",
		"selection_sha256":           "selection-digest",
		"protocol_version":           lmeProtocolVersion,
		"protocol_sha256":            "protocol-digest",
		"model":                      "answer-model",
		"model_variant":              "glm",
		"model_temperature":          0,
		"embedding_model":            "embedding-model",
		"memory_attribution_version": lmeAttributionProtocolVersion,
		"answer_prompt_version":      lmeAnswerPromptVersion,
		"answer_generation":          currentLongMemEvalAnswerGeneration(),
		"judge_prompt_version":       lmeJudgePromptVersion,
		"judge_protocol_version":     lmeJudgeProtocolVersion,
		"judge_generation":           currentLongMemEvalJudgeGeneration(),
		"judge_runs":                 3,
		"judge_cache_format_version": lmeJudgeCacheFormatVersion,
		"judge_cache_shared":         true,
		"judge_cache_ledger_id":      "shared-test-ledger",
	}
}

func testLongMemEvalBuildProvenance(revision string) lmeBuildProvenance {
	return lmeBuildProvenance{
		GoVersion:            "go-test",
		Revision:             revision,
		BuildProfile:         "candidate",
		ModuleManifestSHA256: "manifest-sha",
		ModuleSumSHA256:      "sum-sha",
		Modules: map[string]lmeModuleProvenance{
			lmeAgentModulePath: {
				Version: "v1.0.0",
			},
			lmePGVectorModulePath: {
				Version: "v1.0.0",
			},
		},
	}
}

func TestValidateLongMemEvalComparisonRequiresPinnedBuilds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		mutate    func(map[string]any)
		wantError string
	}{
		{
			name: "missing runner revision",
			mutate: func(metadata map[string]any) {
				metadata["build"] = testLongMemEvalBuildProvenance("")
			},
			wantError: "missing benchmark_revision",
		},
		{
			name: "modified runner",
			mutate: func(metadata map[string]any) {
				build := testLongMemEvalBuildProvenance("runner-revision")
				build.Modified = true
				metadata["build"] = build
			},
			wantError: "modified benchmark build",
		},
		{
			name: "local agent replacement",
			mutate: func(metadata map[string]any) {
				build := testLongMemEvalBuildProvenance("runner-revision")
				module := build.Modules[lmeAgentModulePath]
				module.LocalReplacement = true
				build.Modules[lmeAgentModulePath] = module
				metadata["build"] = build
			},
			wantError: "unpinned local replacement",
		},
		{
			name: "missing module manifest digest",
			mutate: func(metadata map[string]any) {
				build := testLongMemEvalBuildProvenance("runner-revision")
				build.ModuleManifestSHA256 = ""
				metadata["build"] = build
			},
			wantError: "missing module_manifest_sha256",
		},
		{
			name: "missing module checksum digest",
			mutate: func(metadata map[string]any) {
				build := testLongMemEvalBuildProvenance("runner-revision")
				build.ModuleSumSHA256 = ""
				metadata["build"] = build
			},
			wantError: "missing module_sum_sha256",
		},
		{
			name: "missing build profile",
			mutate: func(metadata map[string]any) {
				build := testLongMemEvalBuildProvenance("runner-revision")
				build.BuildProfile = ""
				metadata["build"] = build
			},
			wantError: "missing or unsupported build_profile",
		},
		{
			name: "missing judge revision",
			mutate: func(metadata map[string]any) {
				metadata["judge_build"] = testLongMemEvalBuildProvenance("")
			},
			wantError: "missing benchmark_revision",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseline := &runResult{Metadata: testLongMemEvalComparisonMetadata("upstream-main")}
			candidate := &runResult{Metadata: testLongMemEvalComparisonMetadata("candidate-2196")}
			test.mutate(candidate.Metadata)
			err := validateLongMemEvalComparison(baseline, candidate)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("build provenance error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestValidateLongMemEvalComparisonRequiresPinnedReanswerBuild(t *testing.T) {
	t.Parallel()

	baseline := &runResult{Metadata: testLongMemEvalComparisonMetadata("upstream-main")}
	candidate := &runResult{Metadata: testLongMemEvalComparisonMetadata("candidate-2196")}
	for _, metadata := range []map[string]any{baseline.Metadata, candidate.Metadata} {
		metadata["reanswer_model"] = "answer-model"
		metadata["reanswer_model_variant"] = "glm"
		metadata["reanswer_build"] = testLongMemEvalBuildProvenance("reanswer-revision")
	}
	candidate.Metadata["reanswer_build"] = testLongMemEvalBuildProvenance("")
	err := validateLongMemEvalComparison(baseline, candidate)
	if err == nil || !strings.Contains(err.Error(), "reanswer_build provenance is missing benchmark_revision") {
		t.Fatalf("reanswer build provenance error = %v", err)
	}
}

func TestValidateLongMemEvalComparisonRequiresSharedJudgeLedger(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		mutate    func(map[string]any)
		wantError string
	}{
		{
			name: "different ledger",
			mutate: func(metadata map[string]any) {
				metadata["judge_cache_ledger_id"] = "different-ledger"
			},
			wantError: "judge_cache_ledger_id",
		},
		{
			name: "ephemeral cache",
			mutate: func(metadata map[string]any) {
				metadata["judge_cache_shared"] = false
			},
			wantError: "judge_cache_shared",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			baseline := &runResult{Metadata: testLongMemEvalComparisonMetadata("upstream-main")}
			candidate := &runResult{Metadata: testLongMemEvalComparisonMetadata("candidate-2196")}
			test.mutate(candidate.Metadata)
			err := validateLongMemEvalComparison(baseline, candidate)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("shared judge ledger error = %v, want %q", err, test.wantError)
			}
		})
	}

	baseline := &runResult{Metadata: testLongMemEvalComparisonMetadata("upstream-main")}
	candidate := &runResult{Metadata: testLongMemEvalComparisonMetadata("candidate-2196")}
	baseline.Metadata["judge_cache_shared"] = false
	candidate.Metadata["judge_cache_shared"] = false
	err := validateLongMemEvalComparison(baseline, candidate)
	if err == nil || !strings.Contains(err.Error(), "requires a shared judge cache") {
		t.Fatalf("unshared judge cache error = %v", err)
	}
}

func TestValidateLongMemEvalComparisonRequiresSharedAnswerLedger(t *testing.T) {
	t.Parallel()

	newResult := func(implementation string) *runResult {
		metadata := testLongMemEvalComparisonMetadata(implementation)
		metadata["answer_cache_format_version"] = lmeAnswerCacheFormatVersion
		metadata["answer_cache_shared"] = true
		metadata["answer_cache_ledger_id"] = "shared-answer-ledger"
		return &runResult{Metadata: metadata}
	}
	for _, test := range []struct {
		name      string
		mutate    func(map[string]any)
		wantError string
	}{
		{
			name: "different ledger",
			mutate: func(metadata map[string]any) {
				metadata["answer_cache_ledger_id"] = "different-ledger"
			},
			wantError: "answer_cache_ledger_id",
		},
		{
			name: "missing cache provenance",
			mutate: func(metadata map[string]any) {
				delete(metadata, "answer_cache_format_version")
				delete(metadata, "answer_cache_shared")
				delete(metadata, "answer_cache_ledger_id")
			},
			wantError: "answer_cache_format_version",
		},
		{
			name: "ephemeral cache",
			mutate: func(metadata map[string]any) {
				metadata["answer_cache_shared"] = false
			},
			wantError: "answer_cache_shared",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			baseline := newResult("upstream-main")
			candidate := newResult("candidate-2196")
			test.mutate(candidate.Metadata)
			err := validateLongMemEvalComparison(baseline, candidate)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("shared answer ledger error = %v, want %q", err, test.wantError)
			}
		})
	}

	baseline := newResult("upstream-main")
	candidate := newResult("candidate-2196")
	baseline.Metadata["answer_cache_shared"] = false
	candidate.Metadata["answer_cache_shared"] = false
	err := validateLongMemEvalComparison(baseline, candidate)
	if err == nil || !strings.Contains(err.Error(), "requires a shared answer cache") {
		t.Fatalf("unshared answer cache error = %v", err)
	}
}

func TestValidateLongMemEvalComparisonRejectsRerankDrift(t *testing.T) {
	t.Parallel()

	baseline := &runResult{Metadata: testLongMemEvalComparisonMetadata("upstream-main")}
	candidate := &runResult{Metadata: testLongMemEvalComparisonMetadata("candidate-2196")}
	for _, metadata := range []map[string]any{baseline.Metadata, candidate.Metadata} {
		metadata["rerank_model"] = "answer-model"
		metadata["rerank_model_variant"] = "glm"
		metadata["rerank_prompt_version"] = lmeRerankPromptVersion
		metadata["rerank_generation"] = currentLongMemEvalRerankGeneration()
		metadata["rerank_top_n"] = 12
		metadata["rerank_build"] = testLongMemEvalBuildProvenance("rerank-revision")
	}
	candidate.Metadata["rerank_top_n"] = 8
	err := validateLongMemEvalComparison(baseline, candidate)
	if err == nil || !strings.Contains(err.Error(), "rerank_top_n") {
		t.Fatalf("rerank protocol mismatch error = %v", err)
	}
}

func TestValidateLongMemEvalComparisonRequiresMem0Implementation(t *testing.T) {
	t.Parallel()

	baseline := &runResult{
		Metadata: testLongMemEvalComparisonMetadata("upstream-main"),
		Cases: []*caseResult{{
			QuestionID: "q1",
			BackendResults: map[string]*backendResult{
				"mem0": {Backend: "mem0"},
			},
		}},
	}
	delete(baseline.Metadata, "mem0_implementation")
	candidate := &runResult{
		Metadata: testLongMemEvalComparisonMetadata("candidate-2196"),
		Cases: []*caseResult{{
			QuestionID: "q1",
			BackendResults: map[string]*backendResult{
				"pgvector": {Backend: "pgvector"},
			},
		}},
	}
	err := validateLongMemEvalComparison(baseline, candidate)
	if err == nil || !strings.Contains(err.Error(), "Mem0 implementation") {
		t.Fatalf("missing Mem0 implementation error = %v", err)
	}
}

func TestValidateLongMemEvalComparisonRejectsInvalidBaselineArms(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		baseline  []*caseResult
		candidate []*caseResult
		wantError string
	}{
		{
			name:      "no cases",
			wantError: "contains no LongMemEval cases",
		},
		{
			name: "no supported arm",
			baseline: []*caseResult{{
				QuestionID:     "q1",
				BackendResults: map[string]*backendResult{},
			}},
			candidate: []*caseResult{{
				QuestionID: "q1",
				BackendResults: map[string]*backendResult{
					"pgvector": {Backend: "pgvector"},
				},
			}},
			wantError: "must contain a pgvector or mem0 arm",
		},
		{
			name: "inconsistent arm set",
			baseline: []*caseResult{
				{
					QuestionID: "q1",
					BackendResults: map[string]*backendResult{
						"pgvector": {Backend: "pgvector"},
					},
				},
				{
					QuestionID: "q2",
					BackendResults: map[string]*backendResult{
						"mem0": {Backend: "mem0"},
					},
				},
			},
			candidate: []*caseResult{
				{
					QuestionID: "q1",
					BackendResults: map[string]*backendResult{
						"pgvector": {Backend: "pgvector"},
					},
				},
				{
					QuestionID: "q2",
					BackendResults: map[string]*backendResult{
						"pgvector": {Backend: "pgvector"},
					},
				},
			},
			wantError: "inconsistent backend arm set",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			baseline := &runResult{
				Metadata: testLongMemEvalComparisonMetadata("baseline"),
				Cases:    test.baseline,
			}
			candidate := &runResult{
				Metadata: testLongMemEvalComparisonMetadata("candidate"),
				Cases:    test.candidate,
			}
			err := validateLongMemEvalComparison(baseline, candidate)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("baseline arm validation error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestValidateLongMemEvalComparisonRejectsProtocolDrift(t *testing.T) {
	t.Parallel()

	baseline := &runResult{
		Metadata: testLongMemEvalComparisonMetadata("upstream-main"),
		Cases: []*caseResult{{
			QuestionID: "q1",
			BackendResults: map[string]*backendResult{
				"pgvector": {Backend: "pgvector"},
				"mem0":     {Backend: "mem0"},
			},
		}},
	}
	candidate := &runResult{
		Metadata: testLongMemEvalComparisonMetadata("candidate-2196"),
		Cases: []*caseResult{{
			QuestionID: "q1",
			BackendResults: map[string]*backendResult{
				"pgvector": {Backend: "pgvector"},
			},
		}},
	}
	candidate.Metadata["protocol_sha256"] = "different-protocol"
	err := validateLongMemEvalComparison(baseline, candidate)
	if err == nil || !strings.Contains(err.Error(), "protocol_sha256") {
		t.Fatalf("protocol mismatch error = %v", err)
	}
}

func TestValidateLongMemEvalComparisonRejectsAttributionDrift(t *testing.T) {
	t.Parallel()

	baseline := &runResult{
		Metadata: testLongMemEvalComparisonMetadata("upstream-main"),
	}
	candidate := &runResult{
		Metadata: testLongMemEvalComparisonMetadata("candidate-2196"),
	}
	candidate.Metadata["memory_attribution_version"] = "different-attribution"

	err := validateLongMemEvalComparison(baseline, candidate)
	if err == nil || !strings.Contains(
		err.Error(), "memory_attribution_version",
	) {
		t.Fatalf("memory attribution mismatch error = %v", err)
	}
}

func TestValidateLongMemEvalComparisonRejectsJudgeRunDrift(t *testing.T) {
	t.Parallel()

	baseline := &runResult{
		Metadata: testLongMemEvalComparisonMetadata("upstream-main"),
		Cases: []*caseResult{{
			QuestionID: "q1",
			BackendResults: map[string]*backendResult{
				"pgvector": {Backend: "pgvector"},
				"mem0":     {Backend: "mem0"},
			},
		}},
	}
	candidate := &runResult{
		Metadata: testLongMemEvalComparisonMetadata("candidate-2196"),
		Cases: []*caseResult{{
			QuestionID: "q1",
			BackendResults: map[string]*backendResult{
				"pgvector": {Backend: "pgvector"},
			},
		}},
	}
	candidate.Metadata["judge_runs"] = 1
	err := validateLongMemEvalComparison(baseline, candidate)
	if err == nil || !strings.Contains(err.Error(), "judge_runs") {
		t.Fatalf("judge run mismatch error = %v", err)
	}
}

func TestParseLongMemEvalComparePaths(t *testing.T) {
	base, candidate, err := parseLongMemEvalComparePaths("base.json, candidate.json")
	if err != nil {
		t.Fatalf("parse compare paths: %v", err)
	}
	if base != "base.json" || candidate != "candidate.json" {
		t.Fatalf("unexpected paths: %q %q", base, candidate)
	}
	if _, _, err := parseLongMemEvalComparePaths("only-one.json"); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestCompareLongMemEvalRowsIgnoresJudgeDriftForSameAnswer(t *testing.T) {
	baseline := []lmeAnalysisRow{{
		QuestionID:       "q1",
		Question:         "Which gift?",
		Reference:        "A yellow dress",
		Backend:          "pgvector",
		Answer:           "A yellow dress and earrings.",
		JudgeAvailable:   true,
		EvaluatedCorrect: true,
	}}
	candidate := []lmeAnalysisRow{{
		QuestionID:       "q1",
		Question:         "  which GIFT? ",
		Reference:        "a YELLOW dress",
		Backend:          "pgvector",
		Answer:           "A yellow dress   and earrings",
		JudgeAvailable:   true,
		EvaluatedCorrect: false,
	}}

	rows := compareLongMemEvalRows(baseline, candidate)
	if len(rows) != 1 {
		t.Fatalf("comparison rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if !row.BaselineCorrect || !row.CandidateCorrect || !row.JudgeDriftIgnored {
		t.Fatalf("judge drift was not ignored: %#v", row)
	}
	summary := summarizeLongMemEvalCompareRows(rows)["pgvector"]
	if summary == nil || summary.Regressed != 0 || summary.Unchanged != 1 ||
		summary.JudgeDriftIgnored != 1 {
		t.Fatalf("comparison summary = %#v, want one unchanged judge drift", summary)
	}
	if !strings.Contains(formatLongMemEvalComparisonTSV(rows), "judge_drift_ignored") {
		t.Fatal("comparison TSV is missing the judge drift column")
	}
}

func TestMissingReferenceKeywords(t *testing.T) {
	got := missingReferenceKeywords(
		"The user would prefer cultural events.",
		"The user would prefer cultural events with Spanish language practice and learning resources.",
		4,
	)
	want := []string{"language", "learning", "practice", "resources"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected missing keywords: got %v want %v", got, want)
	}
}

func TestLongMemEvalTrackingModelTimeout(t *testing.T) {
	t.Parallel()

	wrapped := &lmeTrackingModel{
		base:    blockingModel{},
		tracker: &lmeTokenTracker{},
		timeout: 10 * time.Millisecond,
	}
	start := time.Now()
	ch, err := wrapped.GenerateContent(context.Background(), &model.Request{})
	if err != nil {
		t.Fatalf("generate content: %v", err)
	}
	var responses []*model.Response
	for resp := range ch {
		responses = append(responses, resp)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("timeout did not close response channel promptly: %v", elapsed)
	}
	if len(responses) != 1 {
		t.Fatalf("responses = %d, want 1", len(responses))
	}
	if responses[0] == nil || responses[0].Error == nil {
		t.Fatalf("missing timeout response error: %#v", responses[0])
	}
	if responses[0].Error.Type != model.ErrorTypeCancelled {
		t.Fatalf("error type = %q, want %q", responses[0].Error.Type, model.ErrorTypeCancelled)
	}
	if !strings.Contains(responses[0].Error.Message, "timed out") {
		t.Fatalf("error message missing timeout detail: %q", responses[0].Error.Message)
	}
}

func TestLongMemEvalTrackingModelDefaultsTemperatureToZero(t *testing.T) {
	t.Parallel()

	base := &capturingModel{}
	wrapped := &lmeTrackingModel{base: base, tracker: &lmeTokenTracker{}}
	original := &model.Request{}
	ch, err := wrapped.GenerateContent(context.Background(), original)
	if err != nil {
		t.Fatalf("generate content: %v", err)
	}
	for range ch {
	}
	if original.Temperature != nil {
		t.Fatal("tracking model mutated the caller's request")
	}
	if base.request == nil || base.request.Temperature == nil || *base.request.Temperature != 0 {
		t.Fatalf("captured temperature = %v, want 0", base.request)
	}

	explicit := 0.3
	ch, err = wrapped.GenerateContent(context.Background(), &model.Request{
		GenerationConfig: model.GenerationConfig{Temperature: &explicit},
	})
	if err != nil {
		t.Fatalf("generate content with explicit temperature: %v", err)
	}
	for range ch {
	}
	if base.request == nil || base.request.Temperature == nil || *base.request.Temperature != explicit {
		t.Fatalf("explicit temperature was not preserved: %v", base.request)
	}
}

func TestLongMemEvalTrackingModelRecordsResponseAndToolCalls(t *testing.T) {
	t.Parallel()

	tracker := &lmeTokenTracker{}
	finishReason := "length"
	base := &queuedResponseModel{response: &model.Response{Choices: []model.Choice{{
		Message: model.Message{
			Role:    model.RoleAssistant,
			Content: "I will store this.",
			ToolCalls: []model.ToolCall{{Function: model.FunctionDefinitionParam{
				Name:      "memory_add",
				Arguments: []byte(`{"memory":"Likes tea"}`),
			}}},
		},
		FinishReason: &finishReason,
	}}}}
	wrapped := &lmeTrackingModel{base: base, tracker: tracker}

	ch, err := wrapped.GenerateContent(context.Background(), &model.Request{})
	if err != nil {
		t.Fatalf("generate content: %v", err)
	}
	for range ch {
	}
	calls := tracker.SnapshotCalls()
	if len(calls) != 1 {
		t.Fatalf("model calls = %d, want 1", len(calls))
	}
	if calls[0].Content != "I will store this." || len(calls[0].ToolCalls) != 1 ||
		calls[0].FinishReason != finishReason {
		t.Fatalf("unexpected model call trace: %#v", calls[0])
	}
	if calls[0].ToolCalls[0].Name != "memory_add" ||
		!strings.Contains(calls[0].ToolCalls[0].Arguments, "Likes tea") {
		t.Fatalf("unexpected tool call trace: %#v", calls[0].ToolCalls[0])
	}
	if got := tracker.SnapshotCalls(); len(got) != 0 {
		t.Fatalf("second snapshot returned %d calls", len(got))
	}
}

type capturingModel struct {
	request *model.Request
}

type queuedResponseModel struct {
	response *model.Response
}

type queuedAnswerModel struct {
	responses []*model.Response
	requests  []*model.Request
}

func (m *queuedAnswerModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.requests = append(m.requests, req)
	var response *model.Response
	if len(m.responses) > 0 {
		response = m.responses[0]
		m.responses = m.responses[1:]
	}
	ch := make(chan *model.Response, 1)
	if response != nil {
		ch <- response
	}
	close(ch)
	return ch, nil
}

func (*queuedAnswerModel) Info() model.Info { return model.Info{} }

func (m *queuedResponseModel) GenerateContent(
	_ context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- m.response
	close(ch)
	return ch, nil
}

func (*queuedResponseModel) Info() model.Info { return model.Info{} }

func (m *capturingModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.request = req
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (*capturingModel) Info() model.Info { return model.Info{} }

type queuedJudgeModel struct {
	responses []string
	calls     int
	requests  []*model.Request
	usage     *model.Usage
}

func (m *queuedJudgeModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	response := ""
	if m.calls < len(m.responses) {
		response = m.responses[m.calls]
	}
	m.calls++
	m.requests = append(m.requests, req)
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{Choices: []model.Choice{{
		Message: model.NewAssistantMessage(response),
	}}, Usage: m.usage}
	close(ch)
	return ch, nil
}

func (*queuedJudgeModel) Info() model.Info { return model.Info{} }

type blockingModel struct{}

func (blockingModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	go func() {
		defer close(ch)
		<-ctx.Done()
	}()
	return ch, nil
}

func (blockingModel) Info() model.Info { return model.Info{} }
