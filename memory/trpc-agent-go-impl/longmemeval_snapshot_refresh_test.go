//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type snapshotRefreshTestBackend struct {
	stored     []memorySnapshot
	provenance map[string]lmeSnapshotProvenance
	truncated  bool
}

func (*snapshotRefreshTestBackend) Name() string { return "mem0" }

func (*snapshotRefreshTestBackend) Flush(context.Context) error { return nil }

func (*snapshotRefreshTestBackend) IngestPair(
	context.Context,
	*session.Session,
	ingestMeta,
) (*extractionTrace, error) {
	return nil, nil
}

func (*snapshotRefreshTestBackend) Search(
	context.Context,
	memory.UserKey,
	string,
	int,
) ([]memoryHit, error) {
	return nil, nil
}

func (b *snapshotRefreshTestBackend) Read(
	context.Context,
	memory.UserKey,
) ([]memorySnapshot, bool, error) {
	return append([]memorySnapshot(nil), b.stored...), b.truncated, nil
}

func (*snapshotRefreshTestBackend) SnapshotProviderUsage() lmeProviderUsage {
	return lmeProviderUsage{}
}

func (*snapshotRefreshTestBackend) Close() error { return nil }

func (b *snapshotRefreshTestBackend) ReadSnapshotProvenance(
	context.Context,
	memory.UserKey,
) (map[string]lmeSnapshotProvenance, bool, error) {
	return b.provenance, b.truncated, nil
}

func TestRefreshLongMemEvalMemorySnapshotResultRepairsFinalSnapshot(t *testing.T) {
	oldMemory := memorySnapshot{
		ID: "old", Memory: "unrelated", SourceSessions: []string{"session-1"},
	}
	answerMemory := memorySnapshot{ID: "answer", Memory: "answer evidence"}
	judge := &lmeJudgeResult{Model: "judge", Correct: true, Raw: "true"}
	usage := &lmeTokenUsage{LLMCalls: 3, TotalTokens: 30}
	result := &runResult{
		Metadata: map[string]any{"implementation": "source"},
		Cases: []*caseResult{{
			QuestionID:       "question",
			QuestionType:     "single-session-user",
			Question:         "What happened?",
			Answer:           "answer evidence",
			AnswerSessionIDs: []string{"session-2"},
			BackendResults: map[string]*backendResult{
				"mem0": {
					Backend:       "mem0",
					UserID:        "mem0-question-run",
					FinalMemories: []memorySnapshot{oldMemory},
					Retrieval:     []memoryHit{{ID: "answer", Memory: "answer evidence"}},
					Answer:        "answer evidence",
					Evidence:      &evidenceMetrics{HasAnswerTurnLabels: true},
					Judge:         judge,
					TokenUsage:    usage,
				},
			},
		}},
	}
	backend := &snapshotRefreshTestBackend{
		stored: []memorySnapshot{oldMemory, answerMemory},
		provenance: map[string]lmeSnapshotProvenance{
			"old": {
				SourceSessions: []string{"session-1"},
			},
			"answer": {
				SourceSessions:  []string{"session-2"},
				SourceHasAnswer: true,
			},
		},
	}
	outPath := filepath.Join(t.TempDir(), "snapshot.json")
	if err := refreshLongMemEvalMemorySnapshotResult(
		context.Background(),
		result,
		[]string{"mem0"},
		map[string]memoryBackend{"mem0": backend},
		"source-digest",
		outPath,
	); err != nil {
		t.Fatalf("refresh snapshot: %v", err)
	}

	br := result.Cases[0].BackendResults["mem0"]
	if len(br.FinalMemories) != 2 {
		t.Fatalf("final memory count = %d, want 2", len(br.FinalMemories))
	}
	if br.Evidence == nil || !br.Evidence.ExtractRecallAny || !br.Evidence.RetrievalRecallAny {
		t.Fatalf("evidence was not recomputed from repaired provenance: %+v", br.Evidence)
	}
	if !br.Evidence.HasAnswerTurnLabels || !br.Evidence.ExtractTurnRecallAny ||
		!br.Evidence.RetrievalTurnRecallAny {
		t.Fatalf("answer-turn evidence was not preserved and recomputed: %+v", br.Evidence)
	}
	if br.Judge != judge || br.TokenUsage != usage || br.Answer != "answer evidence" {
		t.Fatalf("answer, judge, or usage was not preserved: %+v", br)
	}
	if got := br.Retrieval[0].SourceSessions; len(got) != 1 || got[0] != "session-2" {
		t.Fatalf("retrieval provenance = %v, want [session-2]", got)
	}
	if _, err := loadLongMemEvalResults(outPath); err != nil {
		t.Fatalf("load refreshed checkpoint: %v", err)
	}
	refresh, ok := result.Metadata["snapshot_refresh"].(map[string]any)
	if !ok || refresh["model_calls"] != 0 {
		t.Fatalf("snapshot refresh metadata = %#v", result.Metadata["snapshot_refresh"])
	}
}

func TestVerifyLongMemEvalPersistedMemoriesSubset(t *testing.T) {
	want := []memorySnapshot{{ID: "one", Memory: "one"}}
	if err := verifyLongMemEvalPersistedMemoriesSubset(want, append(want,
		memorySnapshot{ID: "two", Memory: "two"})); err != nil {
		t.Fatalf("verify subset: %v", err)
	}
	if err := verifyLongMemEvalPersistedMemoriesSubset(want,
		[]memorySnapshot{{ID: "one", Memory: "changed"}}); err == nil {
		t.Fatal("verify subset succeeded for a changed memory")
	}
}

func TestMem0OSSReadSnapshotProvenance(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/memories" {
			t.Errorf("request = %s %s, want GET /memories", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("user_id"); got != "user" {
			t.Errorf("user_id = %q, want user", got)
		}
		if got := r.URL.Query().Get("top_k"); got != "1000" {
			t.Errorf("top_k = %q, want 1000", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{
					"id": "keep",
					"metadata": map[string]any{
						"trpc_app_name":  "lme-memory",
						"source_session": "session-1",
						"has_answer":     true,
					},
				},
				{
					"id": "other-app",
					"metadata": map[string]any{
						"trpc_app_name":  "other",
						"source_session": "session-2",
					},
				},
			},
		})
	}))
	defer server.Close()

	backend := &mem0Backend{
		host:       server.URL,
		selfHosted: true,
		httpClient: server.Client(),
	}
	provenance, truncated, err := backend.ReadSnapshotProvenance(
		context.Background(), memory.UserKey{AppName: lmeAppName, UserID: "user"},
	)
	if err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	if truncated {
		t.Fatal("two-record response was reported as truncated")
	}
	if len(provenance) != 1 {
		t.Fatalf("provenance count = %d, want 1", len(provenance))
	}
	item := provenance["keep"]
	if !item.SourceHasAnswer || strings.Join(item.SourceSessions, ",") != "session-1" {
		t.Fatalf("provenance = %+v", item)
	}
}
