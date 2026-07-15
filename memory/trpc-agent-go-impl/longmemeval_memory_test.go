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
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	embeddingopenai "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
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
	if empty.GoVersion != "" || empty.Revision != "" || empty.Modified || empty.Modules != nil {
		t.Fatalf("unexpected unavailable build provenance: %+v", empty)
	}
	injected := applyLongMemEvalInjectedProvenance(
		empty,
		" injected-sha ",
		"true",
		" manifest-sha ",
		" sum-sha ",
	)
	if injected.Revision != "injected-sha" || !injected.Modified ||
		injected.ModuleManifestSHA256 != "manifest-sha" ||
		injected.ModuleSumSHA256 != "sum-sha" {
		t.Fatalf("injected provenance was not applied: %+v", injected)
	}
	native := applyLongMemEvalInjectedProvenance(
		lmeBuildProvenance{
			Revision:             "native-sha",
			Modified:             true,
			ModuleManifestSHA256: "native-manifest",
			ModuleSumSHA256:      "native-sum",
		},
		"injected-sha",
		"false",
		"injected-manifest",
		"injected-sum",
	)
	if native.Revision != "native-sha" || native.Modified ||
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
	)
	if invalid.Revision != "" || invalid.Modified {
		t.Fatalf("invalid injected provenance changed result: %+v", invalid)
	}
}

func TestLongMemEvalBuildProvenanceIssue(t *testing.T) {
	t.Parallel()
	pinned := lmeBuildProvenance{
		GoVersion:            "go-test",
		Revision:             "benchmark-sha",
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

func TestAssistantResultUpdatePolicy(t *testing.T) {
	t.Parallel()

	if got := assistantResultUpdatePolicy(
		extractor.UpdatePolicyReconcile, false,
	); got != "" {
		t.Fatalf("disabled policy = %q, want empty", got)
	}
	if got := assistantResultUpdatePolicy(
		extractor.UpdatePolicyReconcile, true,
	); got != extractor.UpdatePolicyHistoryPreserving {
		t.Fatalf("reconcile result policy = %q", got)
	}
	if got := assistantResultUpdatePolicy(
		extractor.UpdatePolicyHistoryPreserving, true,
	); got != extractor.UpdatePolicyHistoryPreserving {
		t.Fatalf("history result policy = %q", got)
	}
	if got := assistantResultUpdatePolicy(
		extractor.UpdatePolicyAddOnly, true,
	); got != extractor.UpdatePolicyAddOnly {
		t.Fatalf("add-only result policy = %q", got)
	}
}

func TestMem0OSSIngestRetriesTransientStatus(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)
		if r.URL.Path != "/memories" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if attempt == 1 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
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
	if attempts.Load() == 0 {
		t.Fatal("expected at least one request attempt")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("request timeout took too long: %v", elapsed)
	}
}

func TestMem0UsageTransportRecordsHeader(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(lmeMem0UsageHeader, `{
  "llm":{"prompt_tokens":120,"completion_tokens":8,"total_tokens":128,"cached_tokens":32,"llm_calls":2},
  "embedding":{"prompt_tokens":16,"total_tokens":16,"calls":3}
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
	if usage.Embedding.TotalTokens != 16 || usage.Embedding.Calls != 3 {
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

func TestRetryableMem0Status(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway} {
		if !isRetryableMem0Status(status) {
			t.Fatalf("status %d should be retryable", status)
		}
	}
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound} {
		if isRetryableMem0Status(status) {
			t.Fatalf("status %d should not be retryable", status)
		}
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
	got := filterCases(instances)
	want := []string{"q1", "q2", "q3"}
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
	if !strings.Contains(prompt, "[kind=episode; event_time=2023-05-20; participants=Alice; location=Community Center]") {
		t.Fatalf("missing memory metadata: %s", prompt)
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
	if !strings.Contains(normalizedPrompt, "first token must be part of the final answer") {
		t.Fatalf("missing no-reasoning output guard: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt, "comma-separated list") {
		t.Fatalf("missing list output guard: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt, "do not answer with the start date") {
		t.Fatalf("missing duration output guard: %s", prompt)
	}
	if !strings.Contains(normalizedPrompt, "support every entity") {
		t.Fatalf("missing full-question support guard: %s", prompt)
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
	if br.Answer != br.RawAnswer || br.ExactMatch || br.F1 == 1 || br.FailureStage != "answer_miss" {
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
		context.Background(), result, llm, "answer-model", "glm", outPath,
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
	if pgvector.Answer != "Option A" || pgvector.ExactMatch || pgvector.FailureStage != "answer_miss" || pgvector.Judge != nil {
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
	if err := reanswerLongMemEvalResult(context.Background(), nil, llm, "", "", outPath); err == nil {
		t.Fatal("nil result should fail")
	}
}

func reanswerTestBackend(name string) *backendResult {
	return &backendResult{
		Backend:      name,
		Answer:       "legacy answer",
		RawAnswer:    "legacy answer",
		FailureStage: "answer_miss",
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

	valid := &backendResult{Judge: &lmeJudgeResult{
		Model: "judge-model", Raw: "VERDICT: yes", Correct: true,
		RequestedRuns: 3, ValidRuns: 3,
		Attempts: []lmeJudgeAttempt{
			{Raw: "VERDICT: yes", Correct: true},
			{Raw: "VERDICT: no"},
			{Raw: "VERDICT: yes", Correct: true},
		},
	}}
	if !shouldReuseLongMemEvalJudge(valid, "judge-model", 3) {
		t.Fatal("valid verdict from the same model should be reused")
	}
	for _, result := range []*backendResult{
		nil,
		{},
		{Judge: &lmeJudgeResult{Model: "other-model", Raw: "VERDICT: yes", Correct: true}},
		{Judge: &lmeJudgeResult{Model: "judge-model", Raw: "VERDICT: yes", Correct: true, Error: "failed"}},
		{Judge: &lmeJudgeResult{Model: "judge-model", Raw: "VERDICT: yes", Correct: false}},
		{Judge: &lmeJudgeResult{Model: "judge-model", Raw: "VERDICT: yes", Correct: true, RequestedRuns: 1}},
	} {
		if shouldReuseLongMemEvalJudge(result, "judge-model", 3) {
			t.Fatalf("invalid or incompatible judge was reused: %#v", result)
		}
	}
	legacy := &backendResult{Judge: &lmeJudgeResult{
		Model: "judge-model", Raw: "VERDICT: no",
	}}
	if !shouldReuseLongMemEvalJudge(legacy, "judge-model", 1) {
		t.Fatal("legacy single-run verdict should be reused for one requested run")
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
	if judge.RequestedRuns != 3 || len(judge.Attempts) != 3 {
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
	if len(judge.Attempts) != 3 || judge.Attempts[2].Error == "" {
		t.Fatalf("expected failed vote to be retained: %#v", judge.Attempts)
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
				Backend: "pgvector",
				Judge: &lmeJudgeResult{
					Correct: true,
					Raw:     "VERDICT: yes",
					TokenUsage: &lmeTokenUsage{
						LLMCalls:    1,
						TotalTokens: 11,
					},
				},
			},
			"mem0": {
				Backend: "mem0",
				Judge: &lmeJudgeResult{
					Correct: false,
					Raw:     "VERDICT: no",
					TokenUsage: &lmeTokenUsage{
						LLMCalls:    1,
						TotalTokens: 9,
					},
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
}

func TestHitsFromEntriesIncludesEpisodicMetadata(t *testing.T) {
	t.Parallel()

	eventTime := time.Date(2023, 3, 4, 0, 0, 0, 0, time.UTC)
	entries := []*memory.Entry{{
		ID: "mem-1",
		Memory: &memory.Memory{
			Memory:       "Visited the Natural History Museum on 2023-03-04.",
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
	if strings.Join(hit.Participants, ",") != "niece" {
		t.Fatalf("missing participants: %+v", hit)
	}
	if hit.Location != "Natural History Museum" {
		t.Fatalf("missing location: %+v", hit)
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
	stop := "stop"
	llm := &queuedAnswerModel{responses: []*model.Response{
		{Choices: []model.Choice{{
			Message:      model.NewAssistantMessage("reasoning without a final answer"),
			FinishReason: &length,
		}}},
		{Choices: []model.Choice{{
			Message:      model.NewAssistantMessage("3"),
			FinishReason: &stop,
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
}

func TestOpenAIModelOptionsForVariant(t *testing.T) {
	for _, variant := range []string{"", "openai", "deepseek", "hunyuan", "qwen", "glm", " GLM "} {
		if _, err := openAIModelOptionsForVariant(variant); err != nil {
			t.Fatalf("variant %q returned error: %v", variant, err)
		}
	}
	if _, err := openAIModelOptionsForVariant("unknown"); err == nil {
		t.Fatal("expected error for unsupported variant")
	}
}

func TestCurrentLongMemEvalPGVectorExtractionConfig(t *testing.T) {
	oldPolicy := *flagLMEUpdatePolicy
	oldAssistantResults := *flagLMEAssistantResultExtraction
	defer func() {
		*flagLMEUpdatePolicy = oldPolicy
		*flagLMEAssistantResultExtraction = oldAssistantResults
	}()

	for _, tt := range []struct {
		input string
		want  extractor.UpdatePolicy
	}{
		{input: "", want: extractor.UpdatePolicyReconcile},
		{input: " RECONCILE ", want: extractor.UpdatePolicyReconcile},
		{input: "history-preserving", want: extractor.UpdatePolicyHistoryPreserving},
		{input: "ADD-ONLY", want: extractor.UpdatePolicyAddOnly},
	} {
		*flagLMEUpdatePolicy = tt.input
		*flagLMEAssistantResultExtraction = true
		got, err := currentLongMemEvalPGVectorExtractionConfig()
		if err != nil {
			t.Fatalf("policy %q returned error: %v", tt.input, err)
		}
		if got.UpdatePolicy != tt.want {
			t.Fatalf("policy %q = %q, want %q", tt.input, got.UpdatePolicy, tt.want)
		}
		if !got.AssistantResultExtraction {
			t.Fatalf("policy %q lost assistant-result setting", tt.input)
		}
	}

	*flagLMEUpdatePolicy = "custom"
	if _, err := currentLongMemEvalPGVectorExtractionConfig(); err == nil {
		t.Fatal("expected unsupported update policy error")
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
		if row.QuestionID == "q2" && (row.Stage != "ok" || row.RawStage != "answer_miss") {
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
		{name: "correct answer", result: regular, raw: "answer_miss", judgeCorrect: true, judgeAvailable: true, want: "ok"},
		{name: "incorrect answer", result: regular, raw: "ok", judgeAvailable: true, want: "answer_miss"},
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
	for _, name := range []string{"comparison.md", "comparison.tsv"} {
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

func testLongMemEvalComparisonMetadata(implementation string) map[string]any {
	return map[string]any{
		"implementation":        implementation,
		"mem0_implementation":   "mem0-oss-test-revision",
		"build":                 testLongMemEvalBuildProvenance("runner-revision"),
		"judge_build":           testLongMemEvalBuildProvenance("judge-revision"),
		"dataset_sha256":        "dataset-digest",
		"selection_sha256":      "selection-digest",
		"protocol_version":      lmeProtocolVersion,
		"protocol_sha256":       "protocol-digest",
		"model":                 "answer-model",
		"model_variant":         "glm",
		"model_temperature":     0,
		"embedding_model":       "embedding-model",
		"answer_prompt_version": lmeAnswerPromptVersion,
		"answer_generation":     currentLongMemEvalAnswerGeneration(),
		"judge_prompt_version":  lmeJudgePromptVersion,
		"judge_generation":      currentLongMemEvalJudgeGeneration(),
		"judge_runs":            3,
	}
}

func testLongMemEvalBuildProvenance(revision string) lmeBuildProvenance {
	return lmeBuildProvenance{
		GoVersion:            "go-test",
		Revision:             revision,
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
	}
	delete(baseline.Metadata, "mem0_implementation")
	candidate := &runResult{
		Metadata: testLongMemEvalComparisonMetadata("candidate-2196"),
	}
	err := validateLongMemEvalComparison(baseline, candidate)
	if err == nil || !strings.Contains(err.Error(), "Mem0 implementation") {
		t.Fatalf("missing Mem0 implementation error = %v", err)
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
		Answer:           "A yellow dress and earrings",
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
