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
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAuditLongMemEvalResultsWritesValidAudit(t *testing.T) {
	instance, result := validLongMemEvalAuditFixture()
	dir := t.TempDir()
	datasetPath := filepath.Join(dir, "longmemeval.json")
	resultsPath := filepath.Join(dir, "results.json")
	outputDir := filepath.Join(dir, "audit")

	datasetData, err := json.Marshal([]*lmeInstance{instance})
	if err != nil {
		t.Fatalf("marshal dataset: %v", err)
	}
	if err := os.WriteFile(datasetPath, datasetData, 0600); err != nil {
		t.Fatalf("write dataset: %v", err)
	}
	datasetSHA256, err := longMemEvalFileSHA256(datasetPath)
	if err != nil {
		t.Fatalf("hash dataset: %v", err)
	}
	result.Metadata["dataset_sha256"] = datasetSHA256
	if err := writeLongMemEvalResults(resultsPath, result); err != nil {
		t.Fatalf("write results: %v", err)
	}

	if err := auditLongMemEvalResults(
		resultsPath,
		datasetPath,
		outputDir,
	); err != nil {
		t.Fatalf("audit results: %v", err)
	}

	data, err := os.ReadFile(
		filepath.Join(outputDir, longMemEvalIntegrityAuditFile),
	)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	var audit longMemEvalIntegrityAudit
	if err := json.Unmarshal(data, &audit); err != nil {
		t.Fatalf("decode audit: %v", err)
	}
	if audit.Status != "valid" || audit.IssueCount != 0 {
		t.Fatalf("audit status=%q issues=%v", audit.Status, audit.Issues)
	}
	if audit.Counts.Cases != 1 ||
		audit.Counts.BackendResults != 2 ||
		audit.Counts.IngestTraces != 4 ||
		audit.Counts.Mem0ExtractionLLMCalls != 2 ||
		audit.Counts.Mem0MalformedRetryCalls != 0 {
		t.Fatalf("unexpected audit counts: %+v", audit.Counts)
	}
}

func TestBuildLongMemEvalIntegrityAuditRejectsInvalidEvidence(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*runResult)
		checkFail func(longMemEvalIntegrityAuditChecks) bool
	}{
		{
			name: "replay message changed",
			mutate: func(result *runResult) {
				result.Cases[0].BackendResults["pgvector"].
					IngestTraces[0].Messages[0].Content = "different"
			},
			checkFail: func(checks longMemEvalIntegrityAuditChecks) bool {
				return !checks.Replay
			},
		},
		{
			name: "attribution missing",
			mutate: func(result *runResult) {
				result.Cases[0].BackendResults["mem0"].
					FinalMemories[0].AttributedTo = ""
			},
			checkFail: func(checks longMemEvalIntegrityAuditChecks) bool {
				return !checks.Attribution
			},
		},
		{
			name: "mem0 provider usage missing",
			mutate: func(result *runResult) {
				result.Cases[0].BackendResults["mem0"].
					IngestTraces[0].ProviderUsageReported = false
			},
			checkFail: func(checks longMemEvalIntegrityAuditChecks) bool {
				return !checks.ProviderUsage
			},
		},
		{
			name: "summary stale",
			mutate: func(result *runResult) {
				result.Summary.TotalCases++
			},
			checkFail: func(checks longMemEvalIntegrityAuditChecks) bool {
				return !checks.Summary
			},
		},
		{
			name: "scope not explicit",
			mutate: func(result *runResult) {
				result.Metadata["user_scope_explicit"] = false
			},
			checkFail: func(checks longMemEvalIntegrityAuditChecks) bool {
				return !checks.BackendIsolation
			},
		},
		{
			name: "provenance fields malformed",
			mutate: func(result *runResult) {
				result.Metadata["backends"] = []string{
					"pgvector",
					"pgvector",
					"",
				}
				result.Metadata["top_k"] = 0
				result.Metadata["max_sessions"] = -1
				result.Metadata["max_pairs"] = "invalid"
				result.Metadata["user_scope"] = ""
				result.Metadata["selected_question_ids"] = "invalid"
				delete(result.Metadata, "mem0_implementation")
			},
			checkFail: func(checks longMemEvalIntegrityAuditChecks) bool {
				return !checks.Provenance
			},
		},
		{
			name: "case fields changed",
			mutate: func(result *runResult) {
				result.Cases[0].Question = "different question"
				result.Cases[0].QuestionType = "different type"
			},
			checkFail: func(checks longMemEvalIntegrityAuditChecks) bool {
				return !checks.CaseIdentity
			},
		},
		{
			name: "backend identity and set changed",
			mutate: func(result *runResult) {
				pgvector := result.Cases[0].BackendResults["pgvector"]
				pgvector.Backend = "wrong"
				pgvector.UserID = result.Cases[0].
					BackendResults["mem0"].UserID
				delete(result.Cases[0].BackendResults, "mem0")
			},
			checkFail: func(checks longMemEvalIntegrityAuditChecks) bool {
				return !checks.BackendIsolation && !checks.CaseIdentity
			},
		},
		{
			name: "backend and judge errors",
			mutate: func(result *runResult) {
				br := result.Cases[0].BackendResults["pgvector"]
				br.Error = "backend failed"
				br.RerankError = "rerank failed"
				br.Judge = &lmeJudgeResult{
					RequestedRuns: 2,
					ValidRuns:     1,
					Error:         "judge failed",
				}
			},
			checkFail: func(checks longMemEvalIntegrityAuditChecks) bool {
				return !checks.ErrorFree
			},
		},
		{
			name: "snapshots truncated",
			mutate: func(result *runResult) {
				br := result.Cases[0].BackendResults["pgvector"]
				br.SnapshotTruncated = true
				br.IngestTraces[0].SnapshotTruncated = true
			},
			checkFail: func(checks longMemEvalIntegrityAuditChecks) bool {
				return !checks.CompleteSnapshots
			},
		},
		{
			name: "replay count incomplete",
			mutate: func(result *runResult) {
				br := result.Cases[0].BackendResults["pgvector"]
				br.IngestTraces = br.IngestTraces[:1]
			},
			checkFail: func(checks longMemEvalIntegrityAuditChecks) bool {
				return !checks.Replay
			},
		},
		{
			name: "replay trace internally invalid",
			mutate: func(result *runResult) {
				trace := &result.Cases[0].BackendResults["pgvector"].
					IngestTraces[0]
				trace.Date = "not-a-date"
				trace.Error = "trace failed"
				trace.ProviderUsageError = "usage failed"
				trace.MemoryCount = 0
				trace.NewMemories = append(
					trace.NewMemories,
					memorySnapshot{
						Memory:       "extra",
						AttributedTo: lmeAttributionUser,
					},
				)
			},
			checkFail: func(checks longMemEvalIntegrityAuditChecks) bool {
				return !checks.Replay && !checks.ErrorFree
			},
		},
		{
			name: "rerank evidence invalid",
			mutate: func(result *runResult) {
				br := result.Cases[0].BackendResults["pgvector"]
				br.PreRerankRetrieval = []memoryHit{
					{
						Memory:         "one",
						AttributedTo:   "",
						SourceSessions: []string{"unknown"},
					},
					{Memory: "two", AttributedTo: lmeAttributionUser},
					{Memory: "three", AttributedTo: lmeAttributionUser},
				}
				br.Evidence.TopK = 1
				br.FinalMemories[0].SourceSessions = []string{"unknown"}
			},
			checkFail: func(checks longMemEvalIntegrityAuditChecks) bool {
				return !checks.Attribution && !checks.Retrieval
			},
		},
		{
			name: "retrieval exceeds top k",
			mutate: func(result *runResult) {
				br := result.Cases[0].BackendResults["pgvector"]
				br.Retrieval = append(
					br.Retrieval,
					memoryHit{
						Memory:       "second",
						AttributedTo: lmeAttributionUser,
					},
					memoryHit{
						Memory:       "third",
						AttributedTo: lmeAttributionUser,
					},
				)
			},
			checkFail: func(checks longMemEvalIntegrityAuditChecks) bool {
				return !checks.Retrieval
			},
		},
		{
			name: "summary missing",
			mutate: func(result *runResult) {
				result.Summary = nil
			},
			checkFail: func(checks longMemEvalIntegrityAuditChecks) bool {
				return !checks.Summary
			},
		},
		{
			name: "case missing",
			mutate: func(result *runResult) {
				result.Cases[0] = nil
			},
			checkFail: func(checks longMemEvalIntegrityAuditChecks) bool {
				return !checks.CaseIdentity
			},
		},
		{
			name: "no cases",
			mutate: func(result *runResult) {
				result.Cases = nil
				result.Metadata["selected_question_ids"] = []string{}
			},
			checkFail: func(checks longMemEvalIntegrityAuditChecks) bool {
				return !checks.CaseIdentity
			},
		},
		{
			name: "unknown case",
			mutate: func(result *runResult) {
				result.Cases[0].QuestionID = "unknown"
				result.Metadata["selected_question_ids"] =
					[]string{"unknown"}
			},
			checkFail: func(checks longMemEvalIntegrityAuditChecks) bool {
				return !checks.CaseIdentity
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance, result := validLongMemEvalAuditFixture()
			tt.mutate(result)
			audit := buildLongMemEvalIntegrityAudit(
				jsonRoundTripRunResult(t, result),
				[]*lmeInstance{instance},
				"results-sha",
				"dataset-sha",
			)
			if audit.Status != "invalid" || audit.IssueCount == 0 {
				t.Fatalf(
					"audit status=%q issue_count=%d",
					audit.Status,
					audit.IssueCount,
				)
			}
			if !tt.checkFail(audit.Checks) {
				t.Fatalf("expected target check to fail: %+v", audit.Checks)
			}
		})
	}
}

func TestBuildLongMemEvalIntegrityAuditRejectsMissingResult(t *testing.T) {
	audit := buildLongMemEvalIntegrityAudit(
		nil,
		nil,
		"results-sha",
		"dataset-sha",
	)
	if audit.Status != "invalid" || audit.Checks.CaseIdentity {
		t.Fatalf("unexpected audit: %+v", audit)
	}
}

func TestBuildLongMemEvalIntegrityAuditRejectsDuplicateDatasetID(t *testing.T) {
	instance, result := validLongMemEvalAuditFixture()
	audit := buildLongMemEvalIntegrityAudit(
		result,
		[]*lmeInstance{instance, instance},
		"results-sha",
		"dataset-sha",
	)
	if audit.Status != "invalid" || audit.Checks.CaseIdentity {
		t.Fatalf("unexpected audit: %+v", audit)
	}
}

func TestBuildLongMemEvalIntegrityAuditCountsMem0Retries(t *testing.T) {
	instance, result := validLongMemEvalAuditFixture()
	mem0 := result.Cases[0].BackendResults["mem0"]
	mem0.IngestTraces[0].TokenUsage.LLMCalls = 3
	mem0.TokenUsage.LLMCalls = 4
	result.Summary = buildLongMemEvalSummary(result.Cases)

	audit := buildLongMemEvalIntegrityAudit(
		jsonRoundTripRunResult(t, result),
		[]*lmeInstance{instance},
		"results-sha",
		"dataset-sha",
	)
	if audit.Status != "valid" {
		t.Fatalf("audit failed: %v", audit.Issues)
	}
	if audit.Counts.Mem0ExtractionLLMCalls != 4 ||
		audit.Counts.Mem0MalformedRetryCalls != 2 {
		t.Fatalf("unexpected retry counts: %+v", audit.Counts)
	}
}

func validLongMemEvalAuditFixture() (*lmeInstance, *runResult) {
	instance := &lmeInstance{
		QuestionID:       "question-1",
		QuestionType:     "single-session-user",
		Question:         "What did the user choose?",
		QuestionDate:     "2023/05/03 (Wed) 09:00",
		Answer:           "Tea",
		AnswerSessionIDs: []string{"session-early"},
		HaystackDates: []string{
			"2023/05/02 (Tue) 10:00",
			"2023/05/01 (Mon) 10:00",
		},
		HaystackSessionIDs: []string{
			"session-late",
			"session-early",
		},
		HaystackSessions: [][]lmeTurn{
			{
				{Role: "user", Content: "I also like coffee."},
				{Role: "assistant", Content: "Noted."},
			},
			{
				{Role: "user", Content: "I chose tea.", HasAnswer: true},
				{Role: "assistant", Content: "Tea is recorded."},
			},
		},
	}
	expected := expectedLongMemEvalReplay(instance, 0, 0)
	backends := make(map[string]*backendResult, 2)
	for _, backendName := range []string{"pgvector", "mem0"} {
		traces := make([]ingestTrace, 0, len(expected))
		for index, replay := range expected {
			trace := ingestTrace{
				SessionIndex: replay.SessionIndex,
				SessionID:    replay.SessionID,
				Date:         replay.Date,
				PairIndex:    replay.PairIndex,
				HasAnswer:    replay.HasAnswer,
				Messages:     replay.Messages,
				MemoryCount:  index + 1,
				NewMemories: []memorySnapshot{
					{
						ID:             backendName + "-memory",
						Memory:         "User chose tea.",
						AttributedTo:   lmeAttributionUser,
						SourceSessions: []string{replay.SessionID},
					},
				},
			}
			if backendName == "mem0" {
				trace.ProviderUsageReported = true
				trace.TokenUsage = &lmeTokenUsage{
					PromptTokens:     10,
					CompletionTokens: 2,
					TotalTokens:      12,
					LLMCalls:         1,
				}
				if index == 0 {
					trace.EmbeddingUsage = &lmeEmbeddingUsage{
						PromptTokens: 2,
						TotalTokens:  2,
						Calls:        1,
					}
				}
			}
			traces = append(traces, trace)
		}
		br := &backendResult{
			Backend:       backendName,
			UserID:        backendName + "-question-1-scope-1",
			SessionID:     backendName + "-question-1",
			IngestedPairs: len(expected),
			IngestTraces:  traces,
			FinalMemories: []memorySnapshot{
				{
					ID:              backendName + "-memory",
					Memory:          "User chose tea.",
					AttributedTo:    lmeAttributionUser,
					SourceSessions:  []string{"session-early"},
					SourceHasAnswer: true,
				},
			},
			Retrieval: []memoryHit{
				{
					ID:              backendName + "-memory",
					Memory:          "User chose tea.",
					AttributedTo:    lmeAttributionUser,
					SourceSessions:  []string{"session-early"},
					SourceHasAnswer: true,
				},
			},
			Answer:    "Tea",
			RawAnswer: "Tea",
			Evidence: &evidenceMetrics{
				TopK: 2,
			},
		}
		if backendName == "mem0" {
			br.ProviderUsageReported = true
			br.TokenUsage = &lmeTokenUsage{
				PromptTokens:     20,
				CompletionTokens: 4,
				TotalTokens:      24,
				LLMCalls:         2,
			}
			br.EmbeddingUsage = &lmeEmbeddingUsage{
				PromptTokens: 2,
				TotalTokens:  2,
				Calls:        1,
			}
		}
		backends[backendName] = br
	}
	resultCase := &caseResult{
		QuestionID:       instance.QuestionID,
		QuestionType:     instance.QuestionType,
		Question:         instance.Question,
		QuestionDate:     instance.QuestionDate,
		Answer:           instance.Answer.String(),
		AnswerSessionIDs: append([]string(nil), instance.AnswerSessionIDs...),
		NumSessions:      len(instance.HaystackSessions),
		BackendResults:   backends,
	}
	result := &runResult{
		Metadata: map[string]any{
			"benchmark":                  "longmemeval-memory",
			"implementation":             "test-implementation",
			"dataset_sha256":             "dataset-sha",
			"protocol_version":           "lme-memory-v4",
			"protocol_sha256":            "protocol-sha",
			"memory_attribution_version": "lme-memory-attribution-v2",
			"backends":                   []string{"pgvector", "mem0"},
			"top_k":                      2,
			"max_sessions":               0,
			"max_pairs":                  0,
			"user_scope":                 "scope-1",
			"user_scope_explicit":        true,
			"selected_question_ids":      []string{instance.QuestionID},
			"mem0_implementation":        "mem0-test-runtime",
			"build":                      validLongMemEvalAuditBuild(),
		},
		Cases: []*caseResult{resultCase},
	}
	result.Summary = buildLongMemEvalSummary(result.Cases)
	return instance, result
}

func validLongMemEvalAuditBuild() lmeBuildProvenance {
	return lmeBuildProvenance{
		Revision:             "benchmark-revision",
		BuildProfile:         "candidate",
		ModuleManifestSHA256: "module-manifest-sha",
		ModuleSumSHA256:      "module-sum-sha",
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

func jsonRoundTripRunResult(t *testing.T, result *runResult) *runResult {
	t.Helper()
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var decoded runResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	return &decoded
}
