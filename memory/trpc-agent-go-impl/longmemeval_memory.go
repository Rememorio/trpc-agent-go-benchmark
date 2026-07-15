//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// LongMemEval support runs an ingest/search/answer memory benchmark over
// per-question haystacks.
package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go-benchmark/memory/trpc-agent-go-impl/evaluation/metrics"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	memorymem0 "trpc.group/trpc-go/trpc-agent-go/memory/mem0"
	memorypgvector "trpc.group/trpc-go/trpc-agent-go/memory/pgvector"
	"trpc.group/trpc-go/trpc-agent-go/model"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	defaultLongMemEvalDataset = "../../summary/data/longmemeval-cleaned/longmemeval_oracle.json"
	lmeAppName                = "lme-memory"
	lmeAgentName              = "lme-memory-agent"

	lmePGVectorTableBase = "lme_memory_eval"
	defaultMem0Host      = "http://localhost:8888"
	lmeMem0IngestRetries = 2
	lmeAutoMemoryPoll    = 20 * time.Millisecond
	lmeAutoMemoryGrace   = time.Second
	lmeAutoMemoryTimeout = 10 * time.Minute

	// This diagnostic state is optional: upstream main does not write it,
	// while candidate builds can surface asynchronous extraction failures.
	// Keep the key local so the same benchmark source builds against both.
	lmeAutoMemoryLastErrorStateKey = "memory:last_extract_error"
)

type lmeTurn struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	HasAnswer bool   `json:"has_answer,omitempty"`
}

type lmeInstance struct {
	QuestionID         string      `json:"question_id"`
	QuestionType       string      `json:"question_type"`
	Question           string      `json:"question"`
	QuestionDate       string      `json:"question_date"`
	Answer             flexString  `json:"answer"`
	AnswerSessionIDs   []string    `json:"answer_session_ids"`
	HaystackDates      []string    `json:"haystack_dates"`
	HaystackSessionIDs []string    `json:"haystack_session_ids"`
	HaystackSessions   [][]lmeTurn `json:"haystack_sessions"`
}

type flexString string

func (s *flexString) UnmarshalJSON(data []byte) error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	switch x := v.(type) {
	case string:
		*s = flexString(x)
	case float64:
		*s = flexString(fmt.Sprintf("%v", x))
	case bool:
		*s = flexString(fmt.Sprintf("%t", x))
	case nil:
		*s = ""
	default:
		*s = flexString(fmt.Sprintf("%v", x))
	}
	return nil
}

func (s flexString) String() string { return string(s) }

type memoryBackend interface {
	Name() string
	IngestPair(ctx context.Context, sess *session.Session, meta ingestMeta) (*extractionTrace, error)
	Flush(ctx context.Context) error
	Search(ctx context.Context, userKey memory.UserKey, query string, topK int) ([]memoryHit, error)
	Read(ctx context.Context, userKey memory.UserKey, limit int) ([]memorySnapshot, error)
	SnapshotProviderUsage() lmeProviderUsage
	Close() error
}

type ingestMeta struct {
	QuestionID string
	SessionID  string
	SessionIdx int
	PairIdx    int
	HasAnswer  bool
	Date       string
	RunID      string
}

type memoryHit struct {
	ID              string    `json:"id,omitempty"`
	Memory          string    `json:"memory"`
	Score           float64   `json:"score,omitempty"`
	SourceSessions  []string  `json:"source_sessions,omitempty"`
	SourceHasAnswer bool      `json:"source_has_answer,omitempty"`
	Kind            string    `json:"kind,omitempty"`
	EventTime       string    `json:"event_time,omitempty"`
	Participants    []string  `json:"participants,omitempty"`
	Location        string    `json:"location,omitempty"`
	CreatedAt       time.Time `json:"created_at,omitempty"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
}

type memorySnapshot struct {
	ID              string    `json:"id,omitempty"`
	Memory          string    `json:"memory"`
	Score           float64   `json:"score,omitempty"`
	SourceSessions  []string  `json:"source_sessions,omitempty"`
	SourceHasAnswer bool      `json:"source_has_answer,omitempty"`
	Kind            string    `json:"kind,omitempty"`
	EventTime       string    `json:"event_time,omitempty"`
	Participants    []string  `json:"participants,omitempty"`
	Location        string    `json:"location,omitempty"`
	CreatedAt       time.Time `json:"created_at,omitempty"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
}

type ingestTrace struct {
	SessionIndex          int                `json:"session_index"`
	SessionID             string             `json:"session_id"`
	Date                  string             `json:"date,omitempty"`
	PairIndex             int                `json:"pair_index"`
	HasAnswer             bool               `json:"has_answer,omitempty"`
	Messages              []traceMessage     `json:"messages"`
	Extraction            *extractionTrace   `json:"extraction,omitempty"`
	NewMemories           []memorySnapshot   `json:"new_memories,omitempty"`
	MemoryCount           int                `json:"memory_count"`
	TokenUsage            *lmeTokenUsage     `json:"token_usage,omitempty"`
	EmbeddingUsage        *lmeEmbeddingUsage `json:"embedding_usage,omitempty"`
	ProviderUsageReported bool               `json:"provider_usage_reported,omitempty"`
	ProviderUsageError    string             `json:"provider_usage_error,omitempty"`
	Error                 string             `json:"error,omitempty"`
	DurationMs            int64              `json:"duration_ms"`
}

type extractionTrace struct {
	ExistingMemoryCount int                   `json:"existing_memory_count"`
	Operations          []extractionOperation `json:"operations,omitempty"`
	ModelCalls          []lmeModelCallTrace   `json:"model_calls,omitempty"`
	Error               string                `json:"error,omitempty"`
}

type lmeModelCallTrace struct {
	Content      string             `json:"content,omitempty"`
	ToolCalls    []lmeToolCallTrace `json:"tool_calls,omitempty"`
	FinishReason string             `json:"finish_reason,omitempty"`
	Error        string             `json:"error,omitempty"`
}

type lmeToolCallTrace struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
}

type extractionOperation struct {
	Stage        string                  `json:"stage,omitempty"`
	Type         extractor.OperationType `json:"type"`
	Memory       string                  `json:"memory,omitempty"`
	MemoryID     string                  `json:"memory_id,omitempty"`
	Topics       []string                `json:"topics,omitempty"`
	MemoryKind   memory.Kind             `json:"memory_kind,omitempty"`
	EventTime    string                  `json:"event_time,omitempty"`
	Participants []string                `json:"participants,omitempty"`
	Location     string                  `json:"location,omitempty"`
}

type lmePGVectorExtractionConfig struct {
	UpdatePolicy                extractor.UpdatePolicy `json:"update_policy"`
	AssistantResultExtraction   bool                   `json:"assistant_result_extraction"`
	AssistantResultUpdatePolicy extractor.UpdatePolicy `json:"assistant_result_update_policy,omitempty"`
}

type traceMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type lmePair struct {
	Messages  []model.Message
	HasAnswer bool
}

type backendResult struct {
	Backend               string              `json:"backend"`
	UserID                string              `json:"user_id"`
	SessionID             string              `json:"session_id"`
	IngestedPairs         int                 `json:"ingested_pairs"`
	IngestTraces          []ingestTrace       `json:"ingest_traces"`
	FinalMemories         []memorySnapshot    `json:"final_memories"`
	Retrieval             []memoryHit         `json:"retrieval"`
	PreRerankRetrieval    []memoryHit         `json:"pre_rerank_retrieval,omitempty"`
	RerankModelCalls      []lmeModelCallTrace `json:"rerank_model_calls,omitempty"`
	RerankUsage           *lmeTokenUsage      `json:"rerank_token_usage,omitempty"`
	RerankDuration        int64               `json:"rerank_duration_ms,omitempty"`
	RerankRaw             string              `json:"rerank_raw,omitempty"`
	RerankError           string              `json:"rerank_error,omitempty"`
	Answer                string              `json:"answer,omitempty"`
	RawAnswer             string              `json:"raw_answer,omitempty"`
	AnswerModelCalls      []lmeModelCallTrace `json:"answer_model_calls,omitempty"`
	TokenUsage            *lmeTokenUsage      `json:"token_usage,omitempty"`
	EmbeddingUsage        *lmeEmbeddingUsage  `json:"embedding_usage,omitempty"`
	AnswerUsage           *lmeTokenUsage      `json:"answer_token_usage,omitempty"`
	ProviderUsageReported bool                `json:"provider_usage_reported,omitempty"`
	ProviderUsageError    string              `json:"provider_usage_error,omitempty"`
	Evidence              *evidenceMetrics    `json:"evidence,omitempty"`
	FailureStage          string              `json:"failure_stage,omitempty"`
	Judge                 *lmeJudgeResult     `json:"judge,omitempty"`
	ExactMatch            bool                `json:"exact_match"`
	F1                    float64             `json:"f1"`
	BLEU                  float64             `json:"bleu"`
	IngestDuration        int64               `json:"ingest_duration_ms"`
	SearchDuration        int64               `json:"search_duration_ms"`
	AnswerDuration        int64               `json:"answer_duration_ms,omitempty"`
	Error                 string              `json:"error,omitempty"`
}

type lmeJudgeResult struct {
	Model         string            `json:"model"`
	Correct       bool              `json:"correct"`
	Raw           string            `json:"raw"`
	RequestedRuns int               `json:"requested_runs,omitempty"`
	ValidRuns     int               `json:"valid_runs,omitempty"`
	Attempts      []lmeJudgeAttempt `json:"attempts,omitempty"`
	TokenUsage    *lmeTokenUsage    `json:"token_usage,omitempty"`
	DurationMs    int64             `json:"duration_ms,omitempty"`
	Error         string            `json:"error,omitempty"`
}

type lmeJudgeAttempt struct {
	Correct    bool                `json:"correct"`
	Raw        string              `json:"raw"`
	ModelCalls []lmeModelCallTrace `json:"model_calls,omitempty"`
	TokenUsage *lmeTokenUsage      `json:"token_usage,omitempty"`
	DurationMs int64               `json:"duration_ms,omitempty"`
	Error      string              `json:"error,omitempty"`
}

type caseResult struct {
	QuestionID       string                    `json:"question_id"`
	QuestionType     string                    `json:"question_type"`
	Question         string                    `json:"question"`
	QuestionDate     string                    `json:"question_date,omitempty"`
	Answer           string                    `json:"answer"`
	AnswerSessionIDs []string                  `json:"answer_session_ids,omitempty"`
	NumSessions      int                       `json:"num_sessions"`
	BackendResults   map[string]*backendResult `json:"backend_results"`
}

type runResult struct {
	Metadata map[string]any `json:"metadata"`
	Summary  *runSummary    `json:"summary,omitempty"`
	Cases    []*caseResult  `json:"cases"`
}

type runSummary struct {
	TotalCases       int                        `json:"total_cases"`
	BackendSummaries map[string]*backendSummary `json:"backend_summaries"`
	TokenUsage       lmeTokenUsage              `json:"token_usage"`
	EmbeddingUsage   lmeEmbeddingUsage          `json:"embedding_usage"`
	JudgeTokenUsage  lmeTokenUsage              `json:"judge_token_usage,omitempty"`
}

type backendSummary struct {
	Cases              int               `json:"cases"`
	ExactMatches       int               `json:"exact_matches"`
	JudgedCases        int               `json:"judged_cases,omitempty"`
	JudgeCorrect       int               `json:"judge_correct,omitempty"`
	TotalPairs         int               `json:"total_pairs"`
	TotalMemories      int               `json:"total_memories"`
	TotalHits          int               `json:"total_hits"`
	EvidenceCases      int               `json:"evidence_cases"`
	ExtractRecallAny   int               `json:"extract_recall_any"`
	RetrievalRecallAny int               `json:"retrieval_recall_any"`
	RetrievalRecallAll int               `json:"retrieval_recall_all"`
	TurnEvidenceCases  int               `json:"turn_evidence_cases"`
	ExtractTurnAny     int               `json:"extract_turn_recall_any"`
	RetrievalTurnAny   int               `json:"retrieval_turn_recall_any"`
	AvgF1              float64           `json:"avg_f1"`
	AvgBLEU            float64           `json:"avg_bleu"`
	TokenUsage         lmeTokenUsage     `json:"token_usage"`
	EmbeddingUsage     lmeEmbeddingUsage `json:"embedding_usage"`
	ProviderUsageCases int               `json:"provider_usage_cases"`
	JudgeTokenUsage    lmeTokenUsage     `json:"judge_token_usage,omitempty"`
}

type evidenceMetrics struct {
	HasEvidenceLabels       bool     `json:"has_evidence_labels"`
	IsAbstention            bool     `json:"is_abstention"`
	TopK                    int      `json:"top_k"`
	AnswerSessionIDs        []string `json:"answer_session_ids,omitempty"`
	ExtractedSourceSessions []string `json:"extracted_source_sessions,omitempty"`
	RetrievedSourceSessions []string `json:"retrieved_source_sessions,omitempty"`
	ExtractRecallAny        bool     `json:"extract_recall_any"`
	RetrievalRecallAny      bool     `json:"retrieval_recall_any"`
	RetrievalRecallAll      bool     `json:"retrieval_recall_all"`
	HasAnswerTurnLabels     bool     `json:"has_answer_turn_labels"`
	ExtractTurnRecallAny    bool     `json:"extract_turn_recall_any"`
	RetrievalTurnRecallAny  bool     `json:"retrieval_turn_recall_any"`
}

type pgvectorBackend struct {
	svc      memory.Service
	ext      *lmeTracingExtractor
	embedder *lmeTrackingEmbedder
}

type lmeTracingExtractor struct {
	extractor.MemoryExtractor
	mu    sync.Mutex
	trace *extractionTrace
}

type lmeStagedOperationExtractor interface {
	ExtractOperationStages(
		ctx context.Context,
		messages []model.Message,
		existing []*memory.Entry,
	) (primary, assistantResults []*extractor.Operation, err error)
}

func (e *lmeTracingExtractor) Extract(
	ctx context.Context,
	messages []model.Message,
	existing []*memory.Entry,
) ([]*extractor.Operation, error) {
	ops, err := e.MemoryExtractor.Extract(ctx, messages, existing)
	e.recordExtraction(existing, err, extractionStage{ops: ops})
	return ops, err
}

func (e *lmeTracingExtractor) ExtractOperationStages(
	ctx context.Context,
	messages []model.Message,
	existing []*memory.Entry,
) ([]*extractor.Operation, []*extractor.Operation, error) {
	staged, ok := e.MemoryExtractor.(lmeStagedOperationExtractor)
	if !ok {
		ops, err := e.MemoryExtractor.Extract(ctx, messages, existing)
		e.recordExtraction(existing, err, extractionStage{
			name: "primary",
			ops:  ops,
		})
		return ops, nil, err
	}
	primary, assistantResults, err := staged.ExtractOperationStages(
		ctx, messages, existing,
	)
	e.recordExtraction(
		existing,
		err,
		extractionStage{name: "primary", ops: primary},
		extractionStage{name: "assistant_result", ops: assistantResults},
	)
	return primary, assistantResults, err
}

type extractionStage struct {
	name string
	ops  []*extractor.Operation
}

func (e *lmeTracingExtractor) recordExtraction(
	existing []*memory.Entry,
	err error,
	stages ...extractionStage,
) {
	trace := &extractionTrace{ExistingMemoryCount: len(existing)}
	if err != nil {
		trace.Error = err.Error()
	}
	for _, stage := range stages {
		for _, op := range stage.ops {
			if op == nil {
				continue
			}
			item := extractionOperation{
				Stage:        stage.name,
				Type:         op.Type,
				Memory:       op.Memory,
				MemoryID:     op.MemoryID,
				Topics:       append([]string(nil), op.Topics...),
				MemoryKind:   op.MemoryKind,
				Participants: append([]string(nil), op.Participants...),
				Location:     op.Location,
			}
			if op.EventTime != nil {
				item.EventTime = op.EventTime.UTC().Format(time.RFC3339Nano)
			}
			trace.Operations = append(trace.Operations, item)
		}
	}
	e.mu.Lock()
	e.trace = trace
	e.mu.Unlock()
}

func (e *lmeTracingExtractor) Reset() {
	e.mu.Lock()
	e.trace = nil
	e.mu.Unlock()
}

func (e *lmeTracingExtractor) Snapshot() *extractionTrace {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.trace
}

func (b *pgvectorBackend) Name() string { return "pgvector" }

func (b *pgvectorBackend) Flush(ctx context.Context) error { return nil }

func (b *pgvectorBackend) IngestPair(
	ctx context.Context,
	sess *session.Session,
	meta ingestMeta,
) (*extractionTrace, error) {
	b.ext.Reset()
	latest, ok := latestSessionMessageTimestamp(sess)
	if !ok {
		return nil, nil
	}
	if t, ok := parseLMEDate(meta.Date); ok {
		ctx = extractor.WithReferenceDate(ctx, t)
	}
	if err := b.svc.EnqueueAutoMemoryJob(ctx, sess); err != nil {
		return b.ext.Snapshot(), fmt.Errorf("enqueue auto memory: %w", err)
	}
	err := waitForAutoMemory(ctx, sess, latest, longMemEvalMemoryJobTimeout()+lmeAutoMemoryGrace)
	return b.ext.Snapshot(), err
}

func (b *pgvectorBackend) Search(ctx context.Context, userKey memory.UserKey, query string, topK int) ([]memoryHit, error) {
	entries, err := b.svc.SearchMemories(ctx, userKey, query, memory.WithSearchOptions(memory.SearchOptions{
		Query:               query,
		MaxResults:          topK,
		HybridSearch:        true,
		Deduplicate:         true,
		KindFallback:        true,
		SimilarityThreshold: 0,
	}))
	if err != nil {
		return nil, err
	}
	return hitsFromEntries(entries), nil
}

func (b *pgvectorBackend) Read(ctx context.Context, userKey memory.UserKey, limit int) ([]memorySnapshot, error) {
	entries, err := b.svc.ReadMemories(ctx, userKey, limit)
	if err != nil {
		return nil, err
	}
	return snapshotsFromEntries(entries), nil
}

func (b *pgvectorBackend) SnapshotProviderUsage() lmeProviderUsage {
	return lmeProviderUsage{
		Embedding: b.embedder.Snapshot(),
		Reported:  true,
	}
}

func (b *pgvectorBackend) Close() error { return b.svc.Close() }

type mem0Backend struct {
	svc        *memorymem0.Service
	host       string
	selfHosted bool
	httpClient *http.Client
	usage      *lmeProviderUsageTracker
}

type mem0RuntimeConfiguration struct {
	Version             string   `json:"version,omitempty"`
	LLMProvider         string   `json:"llm_provider,omitempty"`
	LLMModel            string   `json:"llm_model,omitempty"`
	LLMTemperature      *float64 `json:"llm_temperature,omitempty"`
	EmbedderProvider    string   `json:"embedder_provider,omitempty"`
	EmbedderModel       string   `json:"embedder_model,omitempty"`
	EmbeddingDimensions int      `json:"embedding_dimensions,omitempty"`
	VectorStoreProvider string   `json:"vector_store_provider,omitempty"`
}

func (b *mem0Backend) Name() string { return "mem0" }

func (b *mem0Backend) Flush(ctx context.Context) error { return b.svc.Close() }

func (b *mem0Backend) IngestPair(
	ctx context.Context,
	sess *session.Session,
	meta ingestMeta,
) (*extractionTrace, error) {
	if b.selfHosted {
		return nil, b.ingestPairOSS(ctx, sess, meta)
	}
	pairSess := session.NewSession(sess.AppName, sess.UserID, fmt.Sprintf("%s-%s-%04d", sess.ID, meta.SessionID, meta.PairIdx))
	appendMessages(pairSess, latestPairMessages(sess), meta.SessionID, meta.PairIdx)
	metadata := map[string]any{
		"benchmark":      "longmemeval",
		"question_id":    meta.QuestionID,
		"source_session": meta.SessionID,
		"session_index":  meta.SessionIdx,
		"pair_index":     meta.PairIdx,
		"has_answer":     meta.HasAnswer,
		"session_date":   meta.Date,
	}
	if ts, ok := lmeUnixTimestamp(meta.Date); ok {
		metadata["timestamp"] = ts
	}
	return nil, b.svc.IngestSession(ctx, pairSess,
		session.WithIngestAgentID(lmeAgentName),
		session.WithIngestRunID(meta.RunID),
		session.WithIngestMetadata(metadata),
	)
}

func (b *mem0Backend) ingestPairOSS(ctx context.Context, sess *session.Session, meta ingestMeta) error {
	messages := latestPairMessages(sess)
	apiMsgs := make([]map[string]string, 0, len(messages))
	for _, msg := range messages {
		if strings.TrimSpace(msg.Content) == "" {
			continue
		}
		apiMsgs = append(apiMsgs, map[string]string{
			"role":    msg.Role.String(),
			"content": msg.Content,
		})
	}
	if len(apiMsgs) == 0 {
		return nil
	}
	metadata := map[string]any{
		"trpc_app_name":     sess.AppName,
		"benchmark":         "longmemeval",
		"question_id":       meta.QuestionID,
		"source_session":    meta.SessionID,
		"session_index":     meta.SessionIdx,
		"pair_index":        meta.PairIdx,
		"has_answer":        meta.HasAnswer,
		"session_date":      meta.Date,
		"session_timestamp": nil,
	}
	if ts, ok := lmeUnixTimestamp(meta.Date); ok {
		metadata["session_timestamp"] = ts
	} else {
		delete(metadata, "session_timestamp")
	}
	if observationDate, ok := lmeObservationDate(meta.Date); ok {
		metadata["observation_date"] = observationDate
	}
	payload := map[string]any{
		"messages": apiMsgs,
		"user_id":  sess.UserID,
		"agent_id": lmeAgentName,
		"run_id":   meta.RunID,
		"infer":    true,
		"metadata": metadata,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	client := b.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := strings.TrimRight(b.host, "/") + "/memories"
	var lastErr error
	for attempt := 0; attempt <= lmeMem0IngestRetries; attempt++ {
		if attempt > 0 {
			if err := sleepWithContext(ctx, time.Duration(attempt)*time.Second); err != nil {
				return err
			}
		}
		reqCtx, cancel := contextWithOptionalTimeout(ctx, longMemEvalMem0OSSRequestTimeout())
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(data))
		if err != nil {
			cancel()
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			cancel()
			lastErr = err
			if isRetryableMem0Error(err) {
				continue
			}
			return err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		cancel()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		lastErr = fmt.Errorf("mem0 OSS ingest failed: status=%d body=%s",
			resp.StatusCode, strings.TrimSpace(string(body)))
		if !isRetryableMem0Status(resp.StatusCode) {
			return lastErr
		}
	}
	return lastErr
}

func (b *mem0Backend) Search(ctx context.Context, userKey memory.UserKey, query string, topK int) ([]memoryHit, error) {
	entries, err := b.svc.SearchMemories(ctx, userKey, query, memory.WithSearchOptions(memory.SearchOptions{
		Query:      query,
		MaxResults: topK,
	}))
	if err != nil {
		return nil, err
	}
	return hitsFromEntries(entries), nil
}

func (b *mem0Backend) Read(ctx context.Context, userKey memory.UserKey, limit int) ([]memorySnapshot, error) {
	entries, err := b.svc.ReadMemories(ctx, userKey, limit)
	if err != nil {
		return nil, err
	}
	return snapshotsFromEntries(entries), nil
}

func (b *mem0Backend) SnapshotProviderUsage() lmeProviderUsage {
	return b.usage.Snapshot()
}

func (b *mem0Backend) Close() error { return b.svc.Close() }

func getMem0Host() string {
	host := strings.TrimSpace(*flagMem0Host)
	if host == "" {
		host = strings.TrimSpace(os.Getenv("MEM0_HOST"))
	}
	if host == "" {
		host = defaultMem0Host
	}
	return strings.TrimRight(host, "/")
}

func prepareLongMemEvalMem0(
	ctx context.Context,
	backends []string,
) (*mem0RuntimeConfiguration, error) {
	if *flagMem0Cloud || !containsString(backends, "mem0") {
		return nil, nil
	}
	host := getMem0Host()
	client := &http.Client{Timeout: longMemEvalMem0OSSRequestTimeout()}
	endpoint := host + "/configure"
	if *flagMem0LLMTemperature >= 0 {
		body, err := json.Marshal(map[string]any{
			"llm": map[string]any{
				"config": map[string]any{"temperature": *flagMem0LLMTemperature},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("encode mem0 temperature configuration: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create mem0 configuration request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("configure mem0 temperature: %w", err)
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read mem0 configuration response: %w", readErr)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("configure mem0 temperature: status=%d body=%s",
				resp.StatusCode, strings.TrimSpace(string(responseBody)))
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create mem0 configuration read request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("read mem0 configuration: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("read mem0 configuration: status=%d body=%s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var raw struct {
		Version string `json:"version"`
		LLM     struct {
			Provider string `json:"provider"`
			Config   struct {
				Model       string   `json:"model"`
				Temperature *float64 `json:"temperature"`
			} `json:"config"`
		} `json:"llm"`
		Embedder struct {
			Provider string `json:"provider"`
			Config   struct {
				Model string `json:"model"`
			} `json:"config"`
		} `json:"embedder"`
		VectorStore struct {
			Provider string `json:"provider"`
			Config   struct {
				Dimensions int `json:"embedding_model_dims"`
			} `json:"config"`
		} `json:"vector_store"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode mem0 configuration: %w", err)
	}
	return &mem0RuntimeConfiguration{
		Version:             raw.Version,
		LLMProvider:         raw.LLM.Provider,
		LLMModel:            raw.LLM.Config.Model,
		LLMTemperature:      raw.LLM.Config.Temperature,
		EmbedderProvider:    raw.Embedder.Provider,
		EmbedderModel:       raw.Embedder.Config.Model,
		EmbeddingDimensions: raw.VectorStore.Config.Dimensions,
		VectorStoreProvider: raw.VectorStore.Provider,
	}, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func newLongMemEvalModel(modelName, variant string) (model.Model, error) {
	opts, err := openAIModelOptionsForVariant(variant)
	if err != nil {
		return nil, err
	}
	return openaimodel.New(modelName, opts...), nil
}

func openAIModelOptionsForVariant(variant string) ([]openaimodel.Option, error) {
	switch strings.ToLower(strings.TrimSpace(variant)) {
	case "":
		return nil, nil
	case "openai":
		return []openaimodel.Option{openaimodel.WithVariant(openaimodel.VariantOpenAI)}, nil
	case "deepseek":
		return []openaimodel.Option{openaimodel.WithVariant(openaimodel.VariantDeepSeek)}, nil
	case "hunyuan":
		return []openaimodel.Option{openaimodel.WithVariant(openaimodel.VariantHunyuan)}, nil
	case "qwen":
		return []openaimodel.Option{openaimodel.WithVariant(openaimodel.VariantQwen)}, nil
	case "glm":
		return []openaimodel.Option{openaimodel.WithVariant(openaimodel.VariantGLM)}, nil
	default:
		return nil, fmt.Errorf("unsupported model variant %q", variant)
	}
}

func currentLongMemEvalPGVectorExtractionConfig() (
	lmePGVectorExtractionConfig,
	error,
) {
	var policy extractor.UpdatePolicy
	switch strings.ToLower(strings.TrimSpace(*flagLMEUpdatePolicy)) {
	case "", string(extractor.UpdatePolicyReconcile):
		policy = extractor.UpdatePolicyReconcile
	case string(extractor.UpdatePolicyHistoryPreserving):
		policy = extractor.UpdatePolicyHistoryPreserving
	case string(extractor.UpdatePolicyAddOnly):
		policy = extractor.UpdatePolicyAddOnly
	default:
		return lmePGVectorExtractionConfig{}, fmt.Errorf(
			"unsupported lme-update-policy %q: expected reconcile, history-preserving, or add-only",
			*flagLMEUpdatePolicy,
		)
	}
	return lmePGVectorExtractionConfig{
		UpdatePolicy:              policy,
		AssistantResultExtraction: *flagLMEAssistantResultExtraction,
		AssistantResultUpdatePolicy: assistantResultUpdatePolicy(
			policy, *flagLMEAssistantResultExtraction,
		),
	}, nil
}

func assistantResultUpdatePolicy(
	policy extractor.UpdatePolicy,
	enabled bool,
) extractor.UpdatePolicy {
	if !enabled {
		return ""
	}
	if policy == extractor.UpdatePolicyAddOnly {
		return extractor.UpdatePolicyAddOnly
	}
	return extractor.UpdatePolicyHistoryPreserving
}

func runLongMemEvalMemory(ctx context.Context) error {
	if strings.TrimSpace(*flagLMECompareResults) == "" &&
		strings.TrimSpace(*flagLMEAnalyzeResults) == "" {
		if issue := longMemEvalBuildProvenanceIssue(
			currentLongMemEvalBuildProvenance(),
		); issue != "" {
			log.Printf(
				"WARNING: LongMemEval build provenance is not suitable for strict comparison: %s; "+
					"use ./run-longmemeval.sh from a clean worktree for formal runs",
				issue,
			)
		}
	}
	if raw := strings.TrimSpace(*flagLMECompareResults); raw != "" {
		baseline, candidate, err := parseLongMemEvalComparePaths(raw)
		if err != nil {
			return err
		}
		return compareLongMemEvalResults(baseline, candidate, longMemEvalCompareOutputDir(candidate))
	}
	if path := strings.TrimSpace(*flagLMEJudgeResults); path != "" {
		return judgeLongMemEvalResults(ctx, path, longMemEvalAnalysisOutputDir(path))
	}
	if path := strings.TrimSpace(*flagLMEReanswerResults); path != "" {
		return reanswerLongMemEvalResults(ctx, path, longMemEvalAnalysisOutputDir(path))
	}
	if path := strings.TrimSpace(*flagLMERerankResults); path != "" {
		return rerankLongMemEvalResults(ctx, path, longMemEvalAnalysisOutputDir(path))
	}
	if path := strings.TrimSpace(*flagLMERefreshRetrievalResults); path != "" {
		return refreshLongMemEvalRetrievalResults(
			ctx, path, longMemEvalAnalysisOutputDir(path),
		)
	}
	if path := strings.TrimSpace(*flagLMEAnalyzeResults); path != "" {
		return analyzeLongMemEvalResults(path, longMemEvalAnalysisOutputDir(path))
	}
	if err := os.MkdirAll(*flagOutput, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	pgExtractionConfig, err := currentLongMemEvalPGVectorExtractionConfig()
	if err != nil {
		return err
	}

	modelName := getModelName()
	modelVariant := getModelVariant()
	baseLLM, err := newLongMemEvalModel(modelName, modelVariant)
	if err != nil {
		return err
	}
	datasetPath := resolveLongMemEvalDatasetPath()
	instances, err := loadLongMemEval(datasetPath)
	if err != nil {
		return fmt.Errorf("load dataset: %w", err)
	}
	cases := filterCases(instances)
	if len(cases) == 0 {
		return fmt.Errorf("no cases selected")
	}
	protocol := currentLongMemEvalProtocol()
	datasetDigest, selectionDigest, protocolDigest, err := longMemEvalExperimentDigests(
		datasetPath,
		cases,
		protocol,
	)
	if err != nil {
		return err
	}

	backends := parseMemoryBackends(*flagMemoryBackends)
	mem0Implementation := ""
	for _, backend := range backends {
		if backend != "mem0" {
			continue
		}
		mem0Implementation = longMemEvalMem0Implementation()
		if mem0Implementation == "unspecified" {
			return errors.New("mem0 backend requires -mem0-implementation or MEM0_IMPLEMENTATION")
		}
		break
	}
	mem0Config, err := prepareLongMemEvalMem0(ctx, backends)
	if err != nil {
		return err
	}
	runID := time.Now().UTC().Format("20060102T150405Z")
	results := &runResult{
		Metadata: map[string]any{
			"benchmark":               "longmemeval-memory",
			"implementation":          longMemEvalImplementation(),
			"build":                   currentLongMemEvalBuildProvenance(),
			"dataset":                 datasetPath,
			"dataset_sha256":          datasetDigest,
			"selection_sha256":        selectionDigest,
			"protocol":                protocol,
			"protocol_version":        lmeProtocolVersion,
			"protocol_sha256":         protocolDigest,
			"answer_prompt_version":   lmeAnswerPromptVersion,
			"answer_generation":       currentLongMemEvalAnswerGeneration(),
			"judge_prompt_version":    lmeJudgePromptVersion,
			"judge_generation":        currentLongMemEvalJudgeGeneration(),
			"model":                   modelName,
			"model_variant":           modelVariant,
			"model_temperature":       0,
			"model_call_timeout":      flagLMEModelCallTimeout.String(),
			"embedding_model":         getEmbedModelName(),
			"pgvector_extraction":     pgExtractionConfig,
			"backends":                backends,
			"top_k":                   *flagVectorTopK,
			"table_suffix":            *flagTableSuffix,
			"answer_enabled":          *flagLMEAnswer,
			"run_id":                  runID,
			"started_at":              time.Now().UTC().Format(time.RFC3339),
			"max_sessions":            *flagLMEMaxSessions,
			"max_pairs":               *flagLMEMaxPairs,
			"sample_per_type":         *flagLMEPerType,
			"sample_abstention_count": *flagLMEAbstentionCount,
			"sample_seed":             *flagLMESampleSeed,
			"selected_question_ids":   questionIDs(cases),
			"ingest_wait":             flagLMEIngestWait.String(),
			"ingest_policy":           "chronological session replay; trigger extraction after each user/assistant pair",
			"pgvector_ingest_path": "memory.Service.EnqueueAutoMemoryJob; wait for " +
				"memory:last_extract_at completion after each pair",
			"retrieval_note": "retrieval hits are searched memories, not raw transcript chunks",
			"evidence_note":  "source_sessions are inferred from the pair after which a memory first appeared or changed.",
			"answer_scoring": "raw model output; no retrieval-assisted answer post-processing",
			"token_usage_scope": "LLM and embedding usage made in this process. Self-hosted mem0 internal " +
				"usage is included when its server returns X-Mem0-Usage; provider_usage_reported marks coverage.",
		},
		Cases: make([]*caseResult, 0, len(cases)),
	}
	if mem0Config != nil {
		results.Metadata["mem0_runtime_configuration"] = mem0Config
	}
	if mem0Implementation != "" {
		results.Metadata["mem0_implementation"] = mem0Implementation
	}
	for _, backend := range backends {
		if backend == "mem0" {
			mode := "self-hosted-oss"
			if *flagMem0Cloud {
				mode = "hosted"
			}
			results.Metadata["mem0_mode"] = mode
			break
		}
	}

	log.Printf("LongMemEval memory run: cases=%d backends=%v model=%s pgvector_update_policy=%s pgvector_assistant_results=%t",
		len(cases), backends, modelName, pgExtractionConfig.UpdatePolicy,
		pgExtractionConfig.AssistantResultExtraction)
	for i, inst := range cases {
		log.Printf("[%d/%d] %s type=%s sessions=%d answer=%q",
			i+1, len(cases), inst.QuestionID, inst.QuestionType, len(inst.HaystackSessions), inst.Answer)
		cr := &caseResult{
			QuestionID:       inst.QuestionID,
			QuestionType:     inst.QuestionType,
			Question:         inst.Question,
			QuestionDate:     inst.QuestionDate,
			Answer:           inst.Answer.String(),
			AnswerSessionIDs: inst.AnswerSessionIDs,
			NumSessions:      len(inst.HaystackSessions),
			BackendResults:   make(map[string]*backendResult, len(backends)),
		}

		for _, backendName := range backends {
			tracker := &lmeTokenTracker{}
			llm := &lmeTrackingModel{
				base:    baseLLM,
				tracker: tracker,
				timeout: *flagLMEModelCallTimeout,
			}
			backend, err := newBackend(backendName, llm, pgExtractionConfig)
			if err != nil {
				cr.BackendResults[backendName] = &backendResult{Backend: backendName, Error: err.Error()}
				log.Printf("  %s create failed: %v", backendName, err)
				continue
			}
			tracker.Snapshot()
			br := runCaseBackend(ctx, llm, tracker, backend, inst, runID)
			cr.BackendResults[backendName] = br
			_ = backend.Close()
			log.Printf("  %s pairs=%d memories=%d hits=%d evidence=%s calls=%d tokens=%d cached=%d embed_calls=%d embed_tokens=%d provider_usage=%v em=%v f1=%.3f answer=%q",
				backendName, br.IngestedPairs, len(br.FinalMemories), len(br.Retrieval),
				br.FailureStage,
				tokenCalls(br.TokenUsage), tokenTotal(br.TokenUsage), tokenCached(br.TokenUsage),
				embeddingCalls(br.EmbeddingUsage), embeddingTokens(br.EmbeddingUsage),
				br.ProviderUsageReported,
				br.ExactMatch, br.F1, truncate(br.Answer, 120))
			saveCaseLog(*flagOutput, cr, br)
		}
		results.Cases = append(results.Cases, cr)
		results.Summary = buildLongMemEvalSummary(results.Cases)
		saveLongMemEvalResults(*flagOutput, results)
	}
	results.Summary = buildLongMemEvalSummary(results.Cases)
	printLongMemEvalSummary(results)
	return nil
}

func runCaseBackend(
	ctx context.Context,
	llm model.Model,
	tracker *lmeTokenTracker,
	backend memoryBackend,
	inst *lmeInstance,
	runID string,
) *backendResult {
	start := time.Now()
	userID := fmt.Sprintf("%s-%s-%s", backend.Name(), inst.QuestionID, runID)
	sessionID := fmt.Sprintf("%s-%s", backend.Name(), inst.QuestionID)
	userKey := memory.UserKey{AppName: lmeAppName, UserID: userID}
	sess := session.NewSession(lmeAppName, userID, sessionID)
	provenance := make(map[string]map[string]bool)
	answerProvenance := make(map[string]bool)
	pendingSources := make(map[string]bool)
	pendingHasAnswer := false
	br := &backendResult{
		Backend:      backend.Name(),
		UserID:       userID,
		SessionID:    sessionID,
		IngestTraces: make([]ingestTrace, 0),
	}

	pairsSeen := 0
	for sessIdx, s := range sortedSessions(inst) {
		if *flagLMEMaxSessions > 0 && sessIdx >= *flagLMEMaxSessions {
			break
		}
		pairs := pairTurns(s.Turns)
		for pairIdx, pair := range pairs {
			if *flagLMEMaxPairs > 0 && pairsSeen >= *flagLMEMaxPairs {
				br.IngestDuration = time.Since(start).Milliseconds()
				goto afterIngest
			}
			trace := ingestTrace{
				SessionIndex: s.OriginalIndex,
				SessionID:    s.ID,
				Date:         s.Date,
				PairIndex:    pairIdx,
				HasAnswer:    pair.HasAnswer,
				Messages:     traceMessages(pair.Messages),
			}
			pairStart := time.Now()
			appendMessages(sess, pair.Messages, s.ID, pairIdx)
			pendingSources[s.ID] = true
			pendingHasAnswer = pendingHasAnswer || pair.HasAnswer
			extraction, err := backend.IngestPair(ctx, sess, ingestMeta{
				QuestionID: inst.QuestionID,
				SessionID:  s.ID,
				SessionIdx: s.OriginalIndex,
				PairIdx:    pairIdx,
				HasAnswer:  pair.HasAnswer,
				Date:       s.Date,
				RunID:      runID,
			})
			trace.Extraction = extraction
			if *flagLMEIngestWait > 0 {
				time.Sleep(*flagLMEIngestWait)
			}
			memories, readErr := backend.Read(ctx, userKey, 500)
			if readErr != nil && err == nil {
				err = readErr
			}
			usage, embeddingUsage, providerUsage := snapshotLongMemEvalUsage(
				tracker,
				backend,
			)
			modelCalls := tracker.SnapshotCalls()
			if len(modelCalls) > 0 {
				if trace.Extraction == nil {
					trace.Extraction = &extractionTrace{}
				}
				trace.Extraction.ModelCalls = modelCalls
			}
			trace.TokenUsage = tokenUsagePtr(usage)
			trace.EmbeddingUsage = embeddingUsagePtr(embeddingUsage)
			trace.ProviderUsageReported = providerUsage.Reported
			trace.ProviderUsageError = providerUsage.Error
			addLongMemEvalBackendUsage(br, usage, embeddingUsage, providerUsage)
			newOrChanged := diffSnapshots(br.FinalMemories, memories)
			if len(newOrChanged) > 0 {
				recordProvenance(provenance, answerProvenance, newOrChanged, sortedSet(pendingSources), pendingHasAnswer)
				pendingSources = make(map[string]bool)
				pendingHasAnswer = false
			}
			newOrChanged = annotateSnapshots(newOrChanged, provenance, answerProvenance)
			memories = annotateSnapshots(memories, provenance, answerProvenance)
			trace.MemoryCount = len(memories)
			trace.NewMemories = newOrChanged
			br.FinalMemories = memories
			if err != nil {
				trace.Error = err.Error()
				br.Error = err.Error()
			}
			trace.DurationMs = time.Since(pairStart).Milliseconds()
			br.IngestTraces = append(br.IngestTraces, trace)
			pairsSeen++
			br.IngestedPairs = pairsSeen
			if *flagVerbose {
				log.Printf("    %s session=%s pair=%d new=%d total=%d err=%v",
					backend.Name(), s.ID, pairIdx, len(trace.NewMemories), trace.MemoryCount, err)
			}
			if err != nil {
				goto afterIngest
			}
		}
	}

	br.IngestDuration = time.Since(start).Milliseconds()

afterIngest:
	if err := backend.Flush(ctx); err != nil {
		br.Error = appendError(br.Error, "flush: "+err.Error())
	}
	if memories, err := backend.Read(ctx, userKey, 500); err == nil {
		newOrChanged := diffSnapshots(br.FinalMemories, memories)
		if len(newOrChanged) > 0 {
			recordProvenance(provenance, answerProvenance, newOrChanged, sortedSet(pendingSources), pendingHasAnswer)
		}
		br.FinalMemories = annotateSnapshots(memories, provenance, answerProvenance)
	} else {
		br.Error = appendError(br.Error, "final read: "+err.Error())
	}

	searchStart := time.Now()
	hits, err := backend.Search(ctx, userKey, inst.Question, *flagVectorTopK)
	br.SearchDuration = time.Since(searchStart).Milliseconds()
	searchUsage, searchEmbeddingUsage, searchProviderUsage := snapshotLongMemEvalUsage(
		tracker,
		backend,
	)
	addLongMemEvalBackendUsage(
		br,
		searchUsage,
		searchEmbeddingUsage,
		searchProviderUsage,
	)
	if err != nil {
		br.Error = appendError(br.Error, "search: "+err.Error())
	}
	br.Retrieval = annotateHits(hits, provenance, answerProvenance)

	if *flagLMEAnswer {
		answerStart := time.Now()
		rawAnswer, err := answerFromMemories(ctx, llm, inst, hits)
		br.AnswerModelCalls = tracker.SnapshotCalls()
		usage, embeddingUsage, providerUsage := snapshotLongMemEvalUsage(
			tracker,
			backend,
		)
		br.AnswerDuration = time.Since(answerStart).Milliseconds()
		br.AnswerUsage = tokenUsagePtr(usage)
		addLongMemEvalBackendUsage(br, usage, embeddingUsage, providerUsage)
		if err != nil {
			br.Error = appendError(br.Error, "answer: "+err.Error())
		}
		br.RawAnswer = rawAnswer
		br.Answer = strings.TrimSpace(rawAnswer)
	}
	usage, embeddingUsage, providerUsage := snapshotLongMemEvalUsage(
		tracker,
		backend,
	)
	addLongMemEvalBackendUsage(br, usage, embeddingUsage, providerUsage)
	br.ExactMatch = exactAnswerMatch(br.Answer, inst.Answer.String())
	br.F1 = metrics.CalculateF1(br.Answer, inst.Answer.String())
	br.BLEU = metrics.CalculateBLEU(br.Answer, inst.Answer.String())
	br.Evidence = computeEvidenceMetrics(inst, br, *flagVectorTopK)
	br.FailureStage = classifyFailure(inst, br)
	return br
}

func newBackend(
	name string,
	llm model.Model,
	pgExtractionConfig lmePGVectorExtractionConfig,
) (memoryBackend, error) {
	switch strings.TrimSpace(name) {
	case "pgvector":
		return newLongMemEvalPGVectorBackend(
			llm, pgExtractionConfig, false,
		)
	case "mem0":
		host := getMem0Host()
		timeout := 90 * time.Second
		usage := &lmeProviderUsageTracker{}
		httpClient := &http.Client{
			Timeout: timeout,
			Transport: &lmeMem0UsageTransport{
				base:    http.DefaultTransport,
				tracker: usage,
			},
		}
		opts := []memorymem0.ServiceOpt{
			memorymem0.WithHost(host),
			memorymem0.WithHTTPClient(httpClient),
			memorymem0.WithAsyncMode(false),
			memorymem0.WithTimeout(timeout),
			memorymem0.WithMemoryJobTimeout(timeout),
			memorymem0.WithLoadToolEnabled(true),
		}
		selfHosted := !*flagMem0Cloud
		if !*flagMem0Cloud {
			opts = append(opts,
				memorymem0.WithSelfHostedOSS(),
				memorymem0.WithSelfHostedOSSIncludeUnscopedMemories(),
			)
		} else if key := os.Getenv("MEM0_API_KEY"); key != "" {
			opts = append(opts, memorymem0.WithAPIKey(key))
		}
		svc, err := memorymem0.NewService(opts...)
		if err != nil {
			return nil, err
		}
		return &mem0Backend{
			svc:        svc,
			host:       host,
			selfHosted: selfHosted,
			httpClient: httpClient,
			usage:      usage,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported backend %q", name)
	}
}

func newLongMemEvalPGVectorBackend(
	llm model.Model,
	pgExtractionConfig lmePGVectorExtractionConfig,
	readOnly bool,
) (*pgvectorBackend, error) {
	dsn := getPGVectorDSN()
	if dsn == "" {
		return nil, fmt.Errorf("pgvector-dsn or PGVECTOR_DSN is required")
	}
	emb := newLongMemEvalTrackingEmbedder(
		newEmbeddingEmbedder(getEmbedModelName()),
	)
	opts := []memorypgvector.ServiceOpt{
		memorypgvector.WithPGVectorClientDSN(dsn),
		memorypgvector.WithTableName(tableNameWithSuffix(lmePGVectorTableBase)),
		memorypgvector.WithEmbedder(emb),
		memorypgvector.WithIndexDimension(emb.GetDimensions()),
		memorypgvector.WithMaxResults(*flagVectorTopK),
	}
	var tracingExtractor *lmeTracingExtractor
	if readOnly {
		opts = append(opts, memorypgvector.WithSkipDBInit(true))
	} else {
		tracingExtractor = &lmeTracingExtractor{
			MemoryExtractor: extractor.NewExtractor(
				llm,
				extractor.WithUpdatePolicy(pgExtractionConfig.UpdatePolicy),
				extractor.WithAssistantResultExtraction(
					pgExtractionConfig.AssistantResultExtraction,
				),
			),
		}
		opts = append(opts,
			memorypgvector.WithExtractor(tracingExtractor),
			memorypgvector.WithAsyncMemoryNum(1),
			memorypgvector.WithMemoryQueueSize(1),
			memorypgvector.WithMemoryJobTimeout(longMemEvalMemoryJobTimeout()),
		)
	}
	svc, err := memorypgvector.NewService(opts...)
	if err != nil {
		return nil, err
	}
	return &pgvectorBackend{
		svc:      svc,
		ext:      tracingExtractor,
		embedder: emb,
	}, nil
}

func appendMessages(sess *session.Session, messages []model.Message, sourceSession string, pairIdx int) {
	for i, msg := range messages {
		author := "user"
		if msg.Role == model.RoleAssistant {
			author = lmeAgentName
		}
		evt := event.New(fmt.Sprintf("%s-%04d-%d", sourceSession, pairIdx, i), author,
			event.WithResponse(&model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: msg,
				}},
			}),
		)
		evt.Timestamp = time.Now().UTC().Add(time.Duration(sess.GetEventCount()+1) * time.Millisecond)
		sess.UpdateUserSession(evt)
	}
}

func latestPairMessages(sess *session.Session) []model.Message {
	events := sess.GetEvents()
	out := make([]model.Message, 0, 2)
	for i := len(events) - 1; i >= 0 && len(out) < 2; i-- {
		e := events[i]
		if e.Response == nil || len(e.Response.Choices) == 0 {
			continue
		}
		msg := e.Response.Choices[0].Message
		if msg.Role != model.RoleUser && msg.Role != model.RoleAssistant {
			continue
		}
		if strings.TrimSpace(msg.Content) == "" {
			continue
		}
		out = append(out, msg)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func latestSessionMessageTimestamp(sess *session.Session) (time.Time, bool) {
	events := sess.GetEvents()
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.Response == nil || len(e.Response.Choices) == 0 {
			continue
		}
		msg := e.Response.Choices[0].Message
		if (msg.Role == model.RoleUser || msg.Role == model.RoleAssistant) &&
			strings.TrimSpace(msg.Content) != "" {
			return e.Timestamp.UTC(), true
		}
	}
	return time.Time{}, false
}

func longMemEvalMemoryJobTimeout() time.Duration {
	if *flagLMEModelCallTimeout > 0 {
		return *flagLMEModelCallTimeout
	}
	return lmeAutoMemoryTimeout
}

func waitForAutoMemory(
	ctx context.Context,
	sess *session.Session,
	want time.Time,
	timeout time.Duration,
) error {
	if timeout <= 0 {
		timeout = lmeAutoMemoryTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(lmeAutoMemoryPoll)
	defer ticker.Stop()

	for {
		if raw, ok := sess.GetState(lmeAutoMemoryLastErrorStateKey); ok &&
			len(raw) > 0 {
			return fmt.Errorf("auto memory job failed: %s", raw)
		}
		if raw, ok := sess.GetState(memory.SessionStateKeyAutoMemoryLastExtractAt); ok {
			got, err := time.Parse(time.RFC3339Nano, string(raw))
			if err != nil {
				return fmt.Errorf("parse auto memory completion marker %q: %w", raw, err)
			}
			if !got.Before(want) {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("wait for auto memory through %s: timeout after %s",
				want.Format(time.RFC3339Nano), timeout)
		case <-ticker.C:
		}
	}
}

func answerFromMemories(ctx context.Context, llm model.Model, inst *lmeInstance, hits []memoryHit) (string, error) {
	prompt := buildLongMemEvalAnswerPrompt(inst, hits)
	var lastErr error
	for attempt := 0; attempt < lmeAnswerMaxAttempts; attempt++ {
		req := newLongMemEvalAnswerRequest(prompt)
		if attempt > 0 {
			maxTokens := lmeAnswerRetryMaxTokens
			req.MaxTokens = &maxTokens
		}
		respCh, err := llm.GenerateContent(ctx, req)
		if err != nil {
			return "", err
		}
		var out string
		var delta strings.Builder
		truncated := false
		for resp := range respCh {
			if resp == nil {
				continue
			}
			if resp.Error != nil {
				return "", errors.New(resp.Error.Message)
			}
			if len(resp.Choices) > 0 {
				choice := resp.Choices[0]
				if choice.FinishReason != nil &&
					isLongMemEvalLengthFinishReason(*choice.FinishReason) {
					truncated = true
				}
				if choice.Delta.Content != "" {
					delta.WriteString(choice.Delta.Content)
				}
				if choice.Message.Content != "" {
					out = choice.Message.Content
				}
			}
		}
		if out == "" {
			out = delta.String()
		}
		out = strings.TrimSpace(out)
		if truncated && attempt+1 < lmeAnswerMaxAttempts {
			lastErr = errors.New("model answer reached the completion token limit")
			continue
		}
		if out != "" {
			if truncated {
				return out, errors.New("model answer remained truncated after retry")
			}
			return out, nil
		}
		lastErr = errors.New("model returned empty answer")
		time.Sleep(time.Duration(attempt+1) * time.Second)
	}
	return "", lastErr
}

func isLongMemEvalLengthFinishReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "length", "max_tokens":
		return true
	default:
		return false
	}
}

func reanswerLongMemEvalResults(ctx context.Context, path, outputDir string) error {
	result, err := loadLongMemEvalResults(path)
	if err != nil {
		return err
	}
	modelName := getModelName()
	modelVariant := getModelVariant()
	baseLLM, err := newLongMemEvalModel(modelName, modelVariant)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create re-answer output dir: %w", err)
	}
	return reanswerLongMemEvalResult(
		ctx,
		result,
		baseLLM,
		modelName,
		modelVariant,
		filepath.Join(outputDir, "reanswered_results.json"),
	)
}

func reanswerLongMemEvalResult(
	ctx context.Context,
	result *runResult,
	baseLLM model.Model,
	modelName string,
	modelVariant string,
	outPath string,
) error {
	if result == nil {
		return errors.New("re-answer results are nil")
	}
	if result.Metadata == nil {
		result.Metadata = make(map[string]any)
	}
	result.Metadata["reanswer_model"] = modelName
	result.Metadata["reanswer_model_variant"] = modelVariant
	result.Metadata["reanswer_build"] = currentLongMemEvalBuildProvenance()
	result.Metadata["answer_generation"] = currentLongMemEvalAnswerGeneration()
	result.Metadata["answer_prompt_version"] = lmeAnswerPromptVersion
	result.Metadata["judge_prompt_version"] = lmeJudgePromptVersion
	result.Metadata["judge_generation"] = currentLongMemEvalJudgeGeneration()
	result.Metadata["reanswered_at"] = time.Now().UTC().Format(time.RFC3339)
	result.Metadata["answer_scoring"] = "raw model output; no retrieval-assisted answer post-processing"
	result.Metadata["reanswer_note"] = "Answers regenerated from saved ranked retrieval hits; backend-specific similarity scores are not shown to the answer model. Responses ending with a length finish reason are retried once with the recorded larger token limit."
	for _, key := range []string{
		"judge_model",
		"judge_model_variant",
		"judge_build",
		"judge_runs",
		"judged_at",
		"judge_note",
	} {
		delete(result.Metadata, key)
	}
	for _, cr := range result.Cases {
		if cr == nil {
			continue
		}
		for _, br := range cr.BackendResults {
			if br != nil {
				br.Judge = nil
			}
		}
	}

	for _, cr := range result.Cases {
		if cr == nil {
			continue
		}
		log.Printf("re-answering %s type=%s", cr.QuestionID, cr.QuestionType)
		inst := &lmeInstance{
			QuestionID:   cr.QuestionID,
			QuestionType: cr.QuestionType,
			Question:     cr.Question,
			QuestionDate: cr.QuestionDate,
			Answer:       flexString(cr.Answer),
		}
		backendNames := make([]string, 0, len(cr.BackendResults))
		for backendName := range cr.BackendResults {
			backendNames = append(backendNames, backendName)
		}
		sort.Strings(backendNames)
		for _, backendName := range backendNames {
			br := cr.BackendResults[backendName]
			if br == nil {
				continue
			}
			tracker := &lmeTokenTracker{}
			llm := &lmeTrackingModel{
				base:    baseLLM,
				tracker: tracker,
				timeout: *flagLMEModelCallTimeout,
			}
			start := time.Now()
			raw, answerErr := answerFromMemories(ctx, llm, inst, br.Retrieval)
			br.AnswerModelCalls = tracker.SnapshotCalls()
			usage := tracker.Snapshot()
			replaceLongMemEvalAnswerUsage(br, usage)
			br.AnswerDuration = time.Since(start).Milliseconds()
			br.RawAnswer = raw
			br.Answer = strings.TrimSpace(raw)
			if answerErr != nil {
				br.Error = appendError(br.Error, "re-answer: "+answerErr.Error())
			}
			scoreLongMemEvalAnswer(cr, br)
			if answerErr != nil {
				br.FailureStage = "answer_error"
			}
			log.Printf("  %s answer=%q calls=%d tokens=%d err=%v",
				backendName, truncate(br.Answer, 80), usage.LLMCalls,
				usage.TotalTokens, answerErr)
		}
		result.Summary = buildLongMemEvalSummary(result.Cases)
		if err := writeLongMemEvalResults(outPath, result); err != nil {
			return fmt.Errorf("checkpoint re-answered results: %w", err)
		}
	}
	result.Summary = buildLongMemEvalSummary(result.Cases)
	printLongMemEvalSummary(result)
	log.Printf("LongMemEval re-answered results written to %s", outPath)
	return nil
}

func replaceLongMemEvalAnswerUsage(br *backendResult, usage lmeTokenUsage) {
	if br == nil {
		return
	}
	total := lmeTokenUsage{}
	if br.TokenUsage != nil {
		total = *br.TokenUsage
	}
	if br.AnswerUsage != nil {
		total.Sub(*br.AnswerUsage)
	}
	total.Add(usage)
	br.TokenUsage = tokenUsagePtr(total)
	br.AnswerUsage = tokenUsagePtr(usage)
}

func judgeLongMemEvalResults(ctx context.Context, path, outputDir string) error {
	judgeRuns := *flagLMEJudgeRuns
	if judgeRuns <= 0 || judgeRuns%2 == 0 {
		return fmt.Errorf("lme-judge-runs must be a positive odd number, got %d", judgeRuns)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read results: %w", err)
	}
	var result runResult
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("parse results: %w", err)
	}
	modelName := getEvalModelName()
	modelVariant := getModelVariant()
	baseLLM, err := newLongMemEvalModel(modelName, modelVariant)
	if err != nil {
		return err
	}
	if result.Metadata == nil {
		result.Metadata = make(map[string]any)
	}
	result.Metadata["judge_model"] = modelName
	result.Metadata["judge_model_variant"] = modelVariant
	result.Metadata["judge_build"] = currentLongMemEvalBuildProvenance()
	result.Metadata["judge_prompt_version"] = lmeJudgePromptVersion
	result.Metadata["judge_generation"] = currentLongMemEvalJudgeGeneration()
	result.Metadata["judge_runs"] = judgeRuns
	result.Metadata["judged_at"] = time.Now().UTC().Format(time.RFC3339)
	result.Metadata["judge_note"] = "LLM semantic correctness judge adapted from the official LongMemEval QA evaluator; only explicit final VERDICT votes are accepted, and multiple requested runs use strict majority voting."
	result.Metadata["answer_scoring"] = "raw model output; no retrieval-assisted answer post-processing"
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create judge output dir: %w", err)
	}
	outPath := filepath.Join(outputDir, longMemEvalJudgedOutputName(path))

	for _, cr := range result.Cases {
		if cr == nil {
			continue
		}
		log.Printf("judging %s type=%s", cr.QuestionID, cr.QuestionType)
		backendNames := make([]string, 0, len(cr.BackendResults))
		for backendName := range cr.BackendResults {
			backendNames = append(backendNames, backendName)
		}
		sort.Strings(backendNames)
		for _, backendName := range backendNames {
			br := cr.BackendResults[backendName]
			if br == nil {
				continue
			}
			restoreLongMemEvalRawAnswer(cr, br)
			if shouldReuseLongMemEvalJudge(br, modelName, judgeRuns) {
				log.Printf("  %s judge already valid; skipping", backendName)
				continue
			}
			if strings.TrimSpace(br.Answer) == "" {
				br.Judge = &lmeJudgeResult{
					Model:         modelName,
					RequestedRuns: judgeRuns,
					Error:         "missing answer",
				}
				continue
			}
			judge := judgeLongMemEvalConsensus(
				ctx, baseLLM, modelName, cr, br.Answer, judgeRuns,
			)
			br.Judge = judge
			log.Printf("  %s judge correct=%v votes=%d/%d raw=%q err=%s",
				backendName, judge.Correct, judge.ValidRuns, judge.RequestedRuns,
				truncate(judge.Raw, 80), judge.Error)
		}
		result.Summary = buildLongMemEvalSummary(result.Cases)
		if err := writeLongMemEvalResults(outPath, &result); err != nil {
			return fmt.Errorf("checkpoint judged results: %w", err)
		}
	}
	result.Summary = buildLongMemEvalSummary(result.Cases)
	printLongMemEvalSummary(&result)
	log.Printf("LongMemEval judged results written to %s", outPath)
	return nil
}

func longMemEvalJudgedOutputName(path string) string {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if stem == "results" {
		return "judged_results.json"
	}
	if strings.HasSuffix(stem, "_results") {
		return strings.TrimSuffix(stem, "_results") + "_judged_results.json"
	}
	return stem + "_judged.json"
}

func judgeLongMemEvalConsensus(
	ctx context.Context,
	baseLLM model.Model,
	modelName string,
	cr *caseResult,
	answer string,
	runs int,
) *lmeJudgeResult {
	judge := &lmeJudgeResult{
		Model:         modelName,
		RequestedRuns: runs,
		Attempts:      make([]lmeJudgeAttempt, 0, runs),
	}
	var totalUsage lmeTokenUsage
	var yesVotes, noVotes int
	var yesRaw, noRaw string
	for range runs {
		tracker := &lmeTokenTracker{}
		llm := &lmeTrackingModel{
			base:    baseLLM,
			tracker: tracker,
			timeout: *flagLMEModelCallTimeout,
		}
		start := time.Now()
		raw, err := judgeLongMemEvalAnswer(ctx, llm, cr, answer)
		usage := tracker.Snapshot()
		duration := time.Since(start).Milliseconds()
		attempt := lmeJudgeAttempt{
			Raw:        raw,
			ModelCalls: tracker.SnapshotCalls(),
			TokenUsage: tokenUsagePtr(usage),
			DurationMs: duration,
		}
		if err == nil {
			attempt.Correct, err = parseLongMemEvalJudge(raw)
		}
		if err != nil {
			attempt.Error = err.Error()
		} else if attempt.Correct {
			yesVotes++
			if yesRaw == "" {
				yesRaw = raw
			}
		} else {
			noVotes++
			if noRaw == "" {
				noRaw = raw
			}
		}
		judge.Attempts = append(judge.Attempts, attempt)
		judge.DurationMs += duration
		totalUsage.Add(usage)
	}
	judge.ValidRuns = yesVotes + noVotes
	judge.TokenUsage = tokenUsagePtr(totalUsage)
	required := runs/2 + 1
	switch {
	case yesVotes >= required:
		judge.Correct = true
		judge.Raw = yesRaw
	case noVotes >= required:
		judge.Raw = noRaw
	default:
		judge.Error = fmt.Sprintf(
			"judge did not reach strict majority: yes=%d no=%d required=%d",
			yesVotes, noVotes, required,
		)
	}
	return judge
}

func shouldReuseLongMemEvalJudge(br *backendResult, modelName string, runs int) bool {
	if br == nil || br.Judge == nil || br.Judge.Model != modelName {
		return false
	}
	savedRuns := br.Judge.RequestedRuns
	if savedRuns == 0 {
		savedRuns = 1
	}
	if savedRuns != runs {
		return false
	}
	_, valid := longMemEvalJudgeCorrect(br)
	return valid
}

func restoreLongMemEvalRawAnswer(cr *caseResult, br *backendResult) {
	if cr == nil || br == nil {
		return
	}
	raw := strings.TrimSpace(br.RawAnswer)
	if raw == "" || raw == br.Answer {
		return
	}
	br.Answer = raw
	scoreLongMemEvalAnswer(cr, br)
}

func scoreLongMemEvalAnswer(cr *caseResult, br *backendResult) {
	if cr == nil || br == nil {
		return
	}
	br.ExactMatch = exactAnswerMatch(br.Answer, cr.Answer)
	br.F1 = metrics.CalculateF1(br.Answer, cr.Answer)
	br.BLEU = metrics.CalculateBLEU(br.Answer, cr.Answer)
	switch br.FailureStage {
	case "ok", "answer_miss":
		if br.ExactMatch || br.F1 >= 0.8 {
			br.FailureStage = "ok"
		} else {
			br.FailureStage = "answer_miss"
		}
	case "ok_abstention", "abstention_answered":
		if isUnknownAnswer(br.Answer) {
			br.FailureStage = "ok_abstention"
		} else {
			br.FailureStage = "abstention_answered"
		}
	}
}

func judgeLongMemEvalAnswer(
	ctx context.Context,
	llm model.Model,
	cr *caseResult,
	response string,
) (string, error) {
	prompt := buildLongMemEvalJudgePrompt(cr, response)
	raw, err := generateLongMemEvalJudgeResponse(
		ctx,
		llm,
		newLongMemEvalJudgeRequest(prompt),
	)
	if err != nil {
		return raw, err
	}
	if _, err := parseLongMemEvalJudge(raw); err == nil {
		return raw, nil
	}
	repair, err := generateLongMemEvalJudgeResponse(
		ctx,
		llm,
		newLongMemEvalJudgeRepairRequest(prompt),
	)
	if err != nil {
		return raw, fmt.Errorf("repair judge verdict: %w", err)
	}
	verdict, err := parseLongMemEvalJudgeRepair(repair)
	if err != nil {
		return raw + "\n\nVerdict repair response: " + repair, err
	}
	return raw + "\n\nVERDICT: " + verdict, nil
}

func generateLongMemEvalJudgeResponse(
	ctx context.Context,
	llm model.Model,
	req *model.Request,
) (string, error) {
	respCh, err := llm.GenerateContent(ctx, req)
	if err != nil {
		return "", err
	}
	var out string
	var delta strings.Builder
	for resp := range respCh {
		if resp == nil {
			continue
		}
		if resp.Error != nil {
			return "", errors.New(resp.Error.Message)
		}
		if len(resp.Choices) > 0 {
			choice := resp.Choices[0]
			if choice.Delta.Content != "" {
				delta.WriteString(choice.Delta.Content)
			}
			if choice.Message.Content != "" {
				out = choice.Message.Content
			}
		}
	}
	if out == "" {
		out = delta.String()
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", errors.New("judge returned empty answer")
	}
	return out, nil
}

func newLongMemEvalJudgeRequest(prompt string) *model.Request {
	maxTokens := lmeJudgePrimaryMaxTokens
	temp := 0.0
	reasoningEffort := "low"
	thinkingEnabled := false
	return &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("You are a strict LongMemEval evaluator. Analyze the response, then end with exactly one final line: VERDICT: yes or VERDICT: no. The final verdict line is mandatory."),
			model.NewUserMessage(prompt),
		},
		GenerationConfig: model.GenerationConfig{
			Stream:          false,
			MaxTokens:       &maxTokens,
			Temperature:     &temp,
			ReasoningEffort: &reasoningEffort,
			ThinkingEnabled: &thinkingEnabled,
		},
	}
}

func newLongMemEvalJudgeRepairRequest(prompt string) *model.Request {
	maxTokens := lmeJudgeRepairMaxTokens
	temp := 0.0
	reasoningEffort := "low"
	thinkingEnabled := false
	return &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("Return only one JSON object matching the schema. Do not include analysis, markdown, or a verdict line."),
			model.NewUserMessage(fmt.Sprintf("Evaluate the task below. Ignore its output-format instruction and return only {\"correct\":true} or {\"correct\":false}.\n\n<task>\n%s\n</task>", prompt)),
		},
		GenerationConfig: model.GenerationConfig{
			Stream:          false,
			MaxTokens:       &maxTokens,
			Temperature:     &temp,
			ReasoningEffort: &reasoningEffort,
			ThinkingEnabled: &thinkingEnabled,
		},
		StructuredOutput: &model.StructuredOutput{
			Type: model.StructuredOutputJSONSchema,
			JSONSchema: &model.JSONSchemaConfig{
				Name:        "longmemeval_judge_verdict",
				Description: "Binary correctness verdict for one LongMemEval answer.",
				Strict:      true,
				Schema: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"correct": map[string]any{"type": "boolean"},
					},
					"required": []string{"correct"},
				},
			},
		},
		ExtraFields: map[string]any{
			"response_format": map[string]string{"type": "json_object"},
		},
	}
}

func buildLongMemEvalJudgePrompt(cr *caseResult, response string) string {
	if cr == nil {
		return ""
	}
	if strings.Contains(cr.QuestionID, "_abs") {
		return fmt.Sprintf(`Task: Decide whether the model correctly identifies the question as unanswerable.
Return yes if the model says the information is incomplete, unavailable, or not mentioned. Return no otherwise.

Question: %s

Explanation: %s

Model Response: %s

After your analysis, write the mandatory final line "VERDICT: yes" or "VERDICT: no".`, cr.Question, cr.Answer, response)
	}
	switch cr.QuestionType {
	case "single-session-user", "single-session-assistant", "multi-session":
		return fmt.Sprintf(`Task: Decide whether the model response is correct.
Return yes if the response contains the correct answer, is equivalent to the correct answer, or contains all intermediate steps needed to get it. Return no if it only contains a subset of the required answer.

Question: %s

Correct Answer: %s

Model Response: %s

After your analysis, write the mandatory final line "VERDICT: yes" or "VERDICT: no".`, cr.Question, cr.Answer, response)
	case "temporal-reasoning":
		return fmt.Sprintf(`Task: Decide whether the model response is correct.
Return yes if the response contains or is equivalent to the correct answer, or contains all intermediate steps needed to get it. Return no if it only contains a subset of the required answer. For day/week/month count questions, do not penalize off-by-one errors.

Question: %s

Correct Answer: %s

Model Response: %s

After your analysis, write the mandatory final line "VERDICT: yes" or "VERDICT: no".`, cr.Question, cr.Answer, response)
	case "knowledge-update":
		return fmt.Sprintf(`Task: Decide whether the model response is correct.
Return yes if the response contains the updated correct answer, even if it also mentions previous information. Return no otherwise.

Question: %s

Correct Answer: %s

Model Response: %s

After your analysis, write the mandatory final line "VERDICT: yes" or "VERDICT: no".`, cr.Question, cr.Answer, response)
	case "single-session-preference":
		return fmt.Sprintf(`Task: Decide whether the model response satisfies the desired personalized response.
Return yes if the response recalls and uses the user's personal information correctly. It does not need to reflect every point in the rubric. Return no otherwise.

Question: %s

Rubric: %s

Model Response: %s

After your analysis, write the mandatory final line "VERDICT: yes" or "VERDICT: no".`, cr.Question, cr.Answer, response)
	default:
		return fmt.Sprintf(`Task: Decide whether the model response is correct.
Return yes if the response is equivalent to the correct answer. Return no otherwise.

Question: %s

Correct Answer: %s

Model Response: %s

After your analysis, write the mandatory final line "VERDICT: yes" or "VERDICT: no".`, cr.Question, cr.Answer, response)
	}
}

var longMemEvalJudgeVerdictRE = regexp.MustCompile(`(?im)^\s*VERDICT:\s*(yes|no)\s*[.!]?\s*$`)

func parseLongMemEvalJudge(raw string) (bool, error) {
	matches := longMemEvalJudgeVerdictRE.FindAllStringSubmatch(raw, -1)
	if len(matches) == 0 {
		return false, errors.New("judge response missing explicit VERDICT")
	}
	verdict := strings.ToLower(matches[len(matches)-1][1])
	return verdict == "yes", nil
}

func parseLongMemEvalJudgeRepair(raw string) (string, error) {
	candidate := strings.TrimSpace(raw)
	if start := strings.LastIndex(candidate, "{"); start >= 0 {
		if end := strings.Index(candidate[start:], "}"); end >= 0 {
			candidate = candidate[start : start+end+1]
		}
	}
	var response struct {
		Correct *bool `json:"correct"`
	}
	if err := json.Unmarshal([]byte(candidate), &response); err != nil {
		return "", fmt.Errorf("decode judge verdict repair %q: %w", truncate(raw, 80), err)
	}
	if response.Correct == nil {
		return "", errors.New("judge verdict repair omitted correct")
	}
	if *response.Correct {
		return "yes", nil
	}
	return "no", nil
}

func newLongMemEvalAnswerRequest(prompt string) *model.Request {
	maxTokens := lmeAnswerPrimaryMaxTokens
	temp := 0.0
	reasoningEffort := "low"
	thinkingEnabled := false
	return &model.Request{
		Messages: []model.Message{model.NewUserMessage(prompt)},
		GenerationConfig: model.GenerationConfig{
			Stream:          false,
			MaxTokens:       &maxTokens,
			Temperature:     &temp,
			ReasoningEffort: &reasoningEffort,
			ThinkingEnabled: &thinkingEnabled,
		},
	}
}

func buildLongMemEvalAnswerPrompt(inst *lmeInstance, hits []memoryHit) string {
	var b strings.Builder
	if len(hits) == 0 {
		b.WriteString("(no memories retrieved)\n")
	} else {
		for i, hit := range hits {
			fmt.Fprintf(&b, "%d. %s", i+1, hit.Memory)
			if meta := formatMemoryMetadata(hit.Kind, hit.EventTime, hit.Participants, hit.Location); meta != "" {
				fmt.Fprintf(&b, " [%s]", meta)
			}
			b.WriteByte('\n')
		}
	}
	guidance := longMemEvalAnswerGuidance(inst)
	return fmt.Sprintf(`You are answering a LongMemEval memory question.

Use only the retrieved memories below. If the memories do not contain enough information, answer "I don't know".
Output only the final answer. Do not explain, reason step by step, cite
memory numbers, mention uncertainty analysis, or use markdown. The first token
must be part of the final answer. If the question asks for an order, list, or
sequence, output only a comma-separated list of the requested entities, without
numbering or dates unless the question explicitly asks for dates.
If the question asks "how long" or asks for a duration, output the elapsed
duration in the unit implied by the question or reference memories; do not
answer with the start date.
%s
For non-preference questions, verify that the retrieved memories directly
support every entity, relationship, event or action, time constraint, and
qualifier in the question. Related or nearby facts are not enough. If any
required condition is missing, answer "I don't know". Do not substitute a
similar but different event, project type, source, or purpose. For example,
course work, thesis research, job work, personal projects, applications,
presentations, purchases, visits, and recommendations are distinct unless the
memories explicitly connect them. Otherwise, answer with the shortest final
span that satisfies the question. If the question asks for a count, total,
duration, date difference, percentage, name, or other scalar value, compute the
final value from the memories and output only that value. If a question asks
for a product brand and the memories identify the product only by a store,
retailer, maker, source, or private-label name, use that name unless the
memories also name a different brand. Do not include markdown, explanations,
citations, or restatements of the question.

Question date: %s
Question type: %s
Question: %s

Retrieved memories:
%s

Answer with a concise final answer only.`, guidance, inst.QuestionDate, inst.QuestionType, inst.Question, b.String())
}

func longMemEvalAnswerGuidance(inst *lmeInstance) string {
	if inst == nil {
		return ""
	}
	if inst.QuestionType == "knowledge-update" {
		return `
For knowledge-update questions, first decide from the question wording whether
it asks for an earlier state or the latest state. If it asks for "previous",
"before", "old", "former", "prior", or what was true before an update, answer
with the earlier value immediately before the later update; do not answer with
the latest/current value. If it asks for "current", "latest", "now", or what
changed after an update, answer with the newest supported value. Use dates and
timeline wording in the retrieved memories to choose the value, but output only
the requested value itself.`
	}
	if strings.Contains(inst.QuestionType, "preference") {
		return `
For preference questions, answer the user's question directly and personalize
the response with relevant preferences, constraints, prior choices, personal
details, or recommendation history from the retrieved memories. Do not describe
the user in the third person or output a preference profile instead of answering
the question.
When any retrieved memory is relevant to the preference topic, do not say
"I don't know" and do not mention missing live data, current local events,
availability, prices, or fresh product listings.
Write a concise, natural response addressed to the user. When the question asks
for advice or a recommendation, give actionable advice that explicitly builds
on the remembered details instead of generic suggestions. Mention only concrete
details supported by memory; do not invent missing personal context.`
	}
	return ""
}

type sortedSession struct {
	OriginalIndex int
	ID            string
	Date          string
	Turns         []lmeTurn
}

func sortedSessions(inst *lmeInstance) []sortedSession {
	out := make([]sortedSession, 0, len(inst.HaystackSessions))
	for i, turns := range inst.HaystackSessions {
		id := fmt.Sprintf("session_%d", i)
		if i < len(inst.HaystackSessionIDs) {
			id = inst.HaystackSessionIDs[i]
		}
		date := ""
		if i < len(inst.HaystackDates) {
			date = inst.HaystackDates[i]
		}
		out = append(out, sortedSession{OriginalIndex: i, ID: id, Date: date, Turns: turns})
	}
	sort.SliceStable(out, func(i, j int) bool {
		ti, okI := parseLMEDate(out[i].Date)
		tj, okJ := parseLMEDate(out[j].Date)
		if okI && okJ {
			return ti.Before(tj)
		}
		if okI != okJ {
			return okI
		}
		return out[i].OriginalIndex < out[j].OriginalIndex
	})
	return out
}

func pairTurns(turns []lmeTurn) []lmePair {
	type cleanTurn struct {
		message   model.Message
		hasAnswer bool
	}
	clean := make([]cleanTurn, 0, len(turns))
	for _, turn := range turns {
		content := strings.TrimSpace(turn.Content)
		if content == "" {
			continue
		}
		role := model.RoleUser
		if strings.EqualFold(turn.Role, "assistant") {
			role = model.RoleAssistant
		}
		clean = append(clean, cleanTurn{
			message:   model.Message{Role: role, Content: content},
			hasAnswer: turn.HasAnswer,
		})
	}
	pairs := make([]lmePair, 0, (len(clean)+1)/2)
	for i := 0; i < len(clean); i += 2 {
		end := i + 2
		if end > len(clean) {
			end = len(clean)
		}
		pair := lmePair{Messages: make([]model.Message, 0, end-i)}
		for _, turn := range clean[i:end] {
			pair.Messages = append(pair.Messages, turn.message)
			pair.HasAnswer = pair.HasAnswer || turn.hasAnswer
		}
		pairs = append(pairs, pair)
	}
	return pairs
}

func loadLongMemEval(path string) ([]*lmeInstance, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var instances []*lmeInstance
	if err := json.Unmarshal(data, &instances); err != nil {
		return nil, err
	}
	return instances, nil
}

func filterCases(instances []*lmeInstance) []*lmeInstance {
	idSet := make(map[string]bool)
	if strings.TrimSpace(*flagLMEQuestionID) != "" {
		idSet[strings.TrimSpace(*flagLMEQuestionID)] = true
	}
	for _, id := range parseCommaList(*flagLMEQuestionIDs) {
		idSet[id] = true
	}
	typeSet := make(map[string]bool)
	for _, t := range parseCommaList(*flagLMEQuestionTypes) {
		typeSet[t] = true
	}
	filtered := make([]*lmeInstance, 0, len(instances))
	for _, inst := range instances {
		if inst == nil {
			continue
		}
		if len(idSet) > 0 && !idSet[inst.QuestionID] {
			continue
		}
		if len(typeSet) > 0 && !typeSet[inst.QuestionType] {
			continue
		}
		filtered = append(filtered, inst)
	}
	out := sampleCases(filtered, *flagLMEPerType, *flagLMEAbstentionCount, *flagLMESampleSeed)
	if *flagMaxTasks > 0 && len(out) > *flagMaxTasks {
		out = out[:*flagMaxTasks]
	}
	return out
}

func sampleCases(instances []*lmeInstance, perType, abstentionCount int, seed int64) []*lmeInstance {
	if perType <= 0 && abstentionCount <= 0 {
		return instances
	}
	selected := make(map[string]*lmeInstance)
	if perType > 0 {
		byType := make(map[string][]*lmeInstance)
		for _, inst := range instances {
			if inst == nil || isAbstentionQuestion(inst) {
				continue
			}
			byType[inst.QuestionType] = append(byType[inst.QuestionType], inst)
		}
		types := make([]string, 0, len(byType))
		for typ := range byType {
			types = append(types, typ)
		}
		sort.Strings(types)
		for _, typ := range types {
			for _, inst := range sampleStable(byType[typ], perType, seed+int64(len(typ))) {
				selected[inst.QuestionID] = inst
			}
		}
	}
	if abstentionCount > 0 {
		abstentions := make([]*lmeInstance, 0)
		for _, inst := range instances {
			if isAbstentionQuestion(inst) {
				abstentions = append(abstentions, inst)
			}
		}
		for _, inst := range sampleStable(abstentions, abstentionCount, seed+7919) {
			selected[inst.QuestionID] = inst
		}
	}
	out := make([]*lmeInstance, 0, len(selected))
	for _, inst := range selected {
		out = append(out, inst)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].QuestionID < out[j].QuestionID
	})
	return out
}

func sampleStable(instances []*lmeInstance, n int, seed int64) []*lmeInstance {
	if n <= 0 || len(instances) == 0 {
		return nil
	}
	sorted := append([]*lmeInstance(nil), instances...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].QuestionID < sorted[j].QuestionID
	})
	if n >= len(sorted) {
		return sorted
	}
	rng := rand.New(rand.NewSource(seed))
	perm := rng.Perm(len(sorted))[:n]
	out := make([]*lmeInstance, 0, n)
	for _, idx := range perm {
		out = append(out, sorted[idx])
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].QuestionID < out[j].QuestionID
	})
	return out
}

func questionIDs(instances []*lmeInstance) []string {
	out := make([]string, 0, len(instances))
	for _, inst := range instances {
		if inst == nil {
			continue
		}
		out = append(out, inst.QuestionID)
	}
	return out
}

func saveLongMemEvalResults(outputDir string, result *runResult) {
	if err := writeLongMemEvalResults(filepath.Join(outputDir, "results.json"), result); err != nil {
		log.Printf("write results: %v", err)
	}
}

func writeLongMemEvalResults(path string, result *runResult) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal results: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write temporary results: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace results: %w", err)
	}
	return nil
}

func saveCaseLog(outputDir string, cr *caseResult, br *backendResult) {
	path := filepath.Join(outputDir, fmt.Sprintf("%s_%s.log", cr.QuestionID, br.Backend))
	var b strings.Builder
	fmt.Fprintf(&b, "QuestionID: %s\nType: %s\nDate: %s\n", cr.QuestionID, cr.QuestionType, cr.QuestionDate)
	fmt.Fprintf(&b, "Question: %s\nReference: %s\nAnswerSessions: %s\n\n",
		cr.Question, cr.Answer, strings.Join(cr.AnswerSessionIDs, ","))
	fmt.Fprintf(&b, "Backend: %s\nUserID: %s\nPairs: %d\nError: %s\n\n",
		br.Backend, br.UserID, br.IngestedPairs, br.Error)
	if br.Evidence != nil {
		fmt.Fprintf(&b, "Evidence: stage=%s has_labels=%v abstention=%v extract_any=%v retrieval_any=%v retrieval_all=%v turn_extract_any=%v turn_retrieval_any=%v extracted=%s retrieved=%s\n\n",
			br.FailureStage,
			br.Evidence.HasEvidenceLabels,
			br.Evidence.IsAbstention,
			br.Evidence.ExtractRecallAny,
			br.Evidence.RetrievalRecallAny,
			br.Evidence.RetrievalRecallAll,
			br.Evidence.ExtractTurnRecallAny,
			br.Evidence.RetrievalTurnRecallAny,
			strings.Join(br.Evidence.ExtractedSourceSessions, ","),
			strings.Join(br.Evidence.RetrievedSourceSessions, ","))
	}
	if br.TokenUsage != nil {
		fmt.Fprintf(&b, "TokenUsage: prompt=%d completion=%d total=%d cached=%d calls=%d cache_hit=%.4f\n\n",
			br.TokenUsage.PromptTokens,
			br.TokenUsage.CompletionTokens,
			br.TokenUsage.TotalTokens,
			br.TokenUsage.CachedTokens,
			br.TokenUsage.LLMCalls,
			br.TokenUsage.CacheHitRate)
	}
	if br.EmbeddingUsage != nil {
		fmt.Fprintf(&b, "EmbeddingUsage: prompt=%d total=%d calls=%d\n\n",
			br.EmbeddingUsage.PromptTokens,
			br.EmbeddingUsage.TotalTokens,
			br.EmbeddingUsage.Calls)
	}
	fmt.Fprintf(&b, "ProviderUsage: reported=%v error=%s\n\n",
		br.ProviderUsageReported, br.ProviderUsageError)
	fmt.Fprintf(&b, "=== Ingestion Trace ===\n")
	for _, tr := range br.IngestTraces {
		fmt.Fprintf(&b, "[session=%s idx=%d pair=%d has_answer=%v date=%s] duration=%dms new=%d total=%d err=%s\n",
			tr.SessionID, tr.SessionIndex, tr.PairIndex, tr.HasAnswer, tr.Date, tr.DurationMs, len(tr.NewMemories), tr.MemoryCount, tr.Error)
		if tr.TokenUsage != nil {
			fmt.Fprintf(&b, "  token_usage: prompt=%d completion=%d total=%d cached=%d calls=%d cache_hit=%.4f\n",
				tr.TokenUsage.PromptTokens,
				tr.TokenUsage.CompletionTokens,
				tr.TokenUsage.TotalTokens,
				tr.TokenUsage.CachedTokens,
				tr.TokenUsage.LLMCalls,
				tr.TokenUsage.CacheHitRate)
		}
		if tr.EmbeddingUsage != nil {
			fmt.Fprintf(&b, "  embedding_usage: prompt=%d total=%d calls=%d\n",
				tr.EmbeddingUsage.PromptTokens,
				tr.EmbeddingUsage.TotalTokens,
				tr.EmbeddingUsage.Calls)
		}
		if tr.ProviderUsageReported || tr.ProviderUsageError != "" {
			fmt.Fprintf(&b, "  provider_usage: reported=%v error=%s\n",
				tr.ProviderUsageReported, tr.ProviderUsageError)
		}
		if tr.Extraction != nil {
			fmt.Fprintf(&b, "  extraction: existing=%d operations=%d error=%s\n",
				tr.Extraction.ExistingMemoryCount,
				len(tr.Extraction.Operations),
				tr.Extraction.Error)
			for _, op := range tr.Extraction.Operations {
				fmt.Fprintf(&b, "    %s id=%s kind=%s event_time=%s topics=%s memory=%s\n",
					op.Type,
					op.MemoryID,
					op.MemoryKind,
					op.EventTime,
					strings.Join(op.Topics, ","),
					truncate(op.Memory, 220))
			}
			for i, call := range tr.Extraction.ModelCalls {
				fmt.Fprintf(&b, "    model_call[%d] error=%s content=%s\n",
					i, call.Error, truncate(call.Content, 500))
				for _, toolCall := range call.ToolCalls {
					fmt.Fprintf(&b, "      tool=%s arguments=%s\n",
						toolCall.Name, truncate(toolCall.Arguments, 500))
				}
			}
		}
		for _, msg := range tr.Messages {
			fmt.Fprintf(&b, "  %s: %s\n", msg.Role, truncate(msg.Content, 220))
		}
		for _, mem := range tr.NewMemories {
			source := strings.Join(mem.SourceSessions, ",")
			if source != "" {
				source = " [" + source + "]"
			}
			fmt.Fprintf(&b, "  + memory%s has_answer=%v: %s\n", source, mem.SourceHasAnswer, mem.Memory)
		}
	}
	fmt.Fprintf(&b, "\n=== Final Memories (%d) ===\n", len(br.FinalMemories))
	for i, mem := range br.FinalMemories {
		source := strings.Join(mem.SourceSessions, ",")
		if source != "" {
			source = " [" + source + "]"
		}
		fmt.Fprintf(&b, "%d.%s has_answer=%v %s\n", i+1, source, mem.SourceHasAnswer, mem.Memory)
	}
	fmt.Fprintf(&b, "\n=== Retrieval (%d) ===\n", len(br.Retrieval))
	for i, hit := range br.Retrieval {
		source := strings.Join(hit.SourceSessions, ",")
		if source != "" {
			source = " [" + source + "]"
		}
		fmt.Fprintf(&b, "%d. score=%.4f%s has_answer=%v %s\n", i+1, hit.Score, source, hit.SourceHasAnswer, hit.Memory)
	}
	fmt.Fprintf(&b, "\n=== Answer ===\n%s\n\nExactMatch: %v\nF1: %.4f\nBLEU: %.4f\n",
		br.Answer, br.ExactMatch, br.F1, br.BLEU)
	if br.AnswerUsage != nil {
		fmt.Fprintf(&b, "AnswerTokenUsage: prompt=%d completion=%d total=%d cached=%d calls=%d cache_hit=%.4f\n",
			br.AnswerUsage.PromptTokens,
			br.AnswerUsage.CompletionTokens,
			br.AnswerUsage.TotalTokens,
			br.AnswerUsage.CachedTokens,
			br.AnswerUsage.LLMCalls,
			br.AnswerUsage.CacheHitRate)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		log.Printf("write case log: %v", err)
	}
}

func printLongMemEvalSummary(result *runResult) {
	fmt.Println("\nLongMemEval Memory Results")
	for _, cr := range result.Cases {
		fmt.Printf("- %s (%s): %s\n", cr.QuestionID, cr.QuestionType, cr.Question)
		for _, br := range cr.BackendResults {
			fmt.Printf("  %s: pairs=%d memories=%d hits=%d stage=%s calls=%d tokens=%d cached=%d embedCalls=%d embedTokens=%d providerUsage=%v EM=%v F1=%.3f BLEU=%.3f err=%s\n",
				br.Backend, br.IngestedPairs, len(br.FinalMemories), len(br.Retrieval),
				br.FailureStage,
				tokenCalls(br.TokenUsage), tokenTotal(br.TokenUsage), tokenCached(br.TokenUsage),
				embeddingCalls(br.EmbeddingUsage), embeddingTokens(br.EmbeddingUsage),
				br.ProviderUsageReported,
				br.ExactMatch, br.F1, br.BLEU, br.Error)
		}
	}
	if result.Summary == nil {
		return
	}
	fmt.Println("\n--- Backend Summary ---")
	for backend, summary := range result.Summary.BackendSummaries {
		judgeText := ""
		if summary.JudgedCases > 0 {
			judgeText = fmt.Sprintf(" judge=%d/%d", summary.JudgeCorrect, summary.JudgedCases)
		}
		fmt.Printf("  %s: cases=%d EM=%d%s evidence=%d extractAny=%d retrievalAny=%d retrievalAll=%d turnEvidence=%d turnExtractAny=%d turnRetrievalAny=%d avgF1=%.3f avgBLEU=%.3f calls=%d tokens=%d cached=%d cacheHit=%.3f embedCalls=%d embedTokens=%d providerUsage=%d/%d\n",
			backend, summary.Cases, summary.ExactMatches,
			judgeText,
			summary.EvidenceCases, summary.ExtractRecallAny, summary.RetrievalRecallAny, summary.RetrievalRecallAll,
			summary.TurnEvidenceCases, summary.ExtractTurnAny, summary.RetrievalTurnAny,
			summary.AvgF1, summary.AvgBLEU,
			summary.TokenUsage.LLMCalls, summary.TokenUsage.TotalTokens,
			summary.TokenUsage.CachedTokens, summary.TokenUsage.CacheHitRate,
			summary.EmbeddingUsage.Calls, summary.EmbeddingUsage.TotalTokens,
			summary.ProviderUsageCases, summary.Cases)
	}
}

func buildLongMemEvalSummary(cases []*caseResult) *runSummary {
	summary := &runSummary{
		TotalCases:       len(cases),
		BackendSummaries: make(map[string]*backendSummary),
	}
	var f1Sums = make(map[string]float64)
	var bleuSums = make(map[string]float64)
	for _, cr := range cases {
		if cr == nil {
			continue
		}
		for backend, br := range cr.BackendResults {
			if br == nil {
				continue
			}
			bs := summary.BackendSummaries[backend]
			if bs == nil {
				bs = &backendSummary{}
				summary.BackendSummaries[backend] = bs
			}
			bs.Cases++
			if br.ExactMatch {
				bs.ExactMatches++
			}
			if judgeCorrect, judgeAvailable := longMemEvalJudgeCorrect(br); judgeAvailable {
				bs.JudgedCases++
				if judgeCorrect {
					bs.JudgeCorrect++
				}
			}
			bs.TotalPairs += br.IngestedPairs
			bs.TotalMemories += len(br.FinalMemories)
			bs.TotalHits += len(br.Retrieval)
			if br.Evidence != nil && br.Evidence.HasEvidenceLabels {
				bs.EvidenceCases++
				if br.Evidence.ExtractRecallAny {
					bs.ExtractRecallAny++
				}
				if br.Evidence.RetrievalRecallAny {
					bs.RetrievalRecallAny++
				}
				if br.Evidence.RetrievalRecallAll {
					bs.RetrievalRecallAll++
				}
				if br.Evidence.HasAnswerTurnLabels {
					bs.TurnEvidenceCases++
					if br.Evidence.ExtractTurnRecallAny {
						bs.ExtractTurnAny++
					}
					if br.Evidence.RetrievalTurnRecallAny {
						bs.RetrievalTurnAny++
					}
				}
			}
			f1Sums[backend] += br.F1
			bleuSums[backend] += br.BLEU
			if br.TokenUsage != nil {
				bs.TokenUsage.Add(*br.TokenUsage)
				summary.TokenUsage.Add(*br.TokenUsage)
			}
			if br.EmbeddingUsage != nil {
				bs.EmbeddingUsage.Add(*br.EmbeddingUsage)
				summary.EmbeddingUsage.Add(*br.EmbeddingUsage)
			}
			if br.ProviderUsageReported {
				bs.ProviderUsageCases++
			}
			if br.Judge != nil && br.Judge.TokenUsage != nil {
				bs.JudgeTokenUsage.Add(*br.Judge.TokenUsage)
				summary.JudgeTokenUsage.Add(*br.Judge.TokenUsage)
			}
		}
	}
	for backend, bs := range summary.BackendSummaries {
		if bs.Cases == 0 {
			continue
		}
		bs.AvgF1 = f1Sums[backend] / float64(bs.Cases)
		bs.AvgBLEU = bleuSums[backend] / float64(bs.Cases)
	}
	return summary
}

func tokenCalls(u *lmeTokenUsage) int {
	if u == nil {
		return 0
	}
	return u.LLMCalls
}

func tokenTotal(u *lmeTokenUsage) int {
	if u == nil {
		return 0
	}
	return u.TotalTokens
}

func tokenCached(u *lmeTokenUsage) int {
	if u == nil {
		return 0
	}
	return u.CachedTokens
}

func embeddingCalls(u *lmeEmbeddingUsage) int {
	if u == nil {
		return 0
	}
	return u.Calls
}

func embeddingTokens(u *lmeEmbeddingUsage) int {
	if u == nil {
		return 0
	}
	return u.TotalTokens
}

func resolveLongMemEvalDatasetPath() string {
	dataset := strings.TrimSpace(*flagDataset)
	dataFile := strings.TrimSpace(*flagDataFile)
	if dataset == "" {
		dataset = defaultLongMemEvalDataset
	}
	if dataset == defaultLoCoMoDataset && dataFile == defaultLoCoMoDataFile {
		return defaultLongMemEvalDataset
	}
	if strings.EqualFold(filepath.Ext(dataset), ".json") {
		return dataset
	}
	if dataFile == "" || dataFile == defaultLoCoMoDataFile {
		return dataset
	}
	return filepath.Join(dataset, dataFile)
}

func parseCommaList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func snapshotsFromEntries(entries []*memory.Entry) []memorySnapshot {
	out := make([]memorySnapshot, 0, len(entries))
	for _, e := range entries {
		if e == nil {
			continue
		}
		out = append(out, memorySnapshot{
			ID:           e.ID,
			Memory:       e.Memory.Memory,
			Score:        e.Score,
			Kind:         string(e.Memory.Kind),
			EventTime:    formatEventTime(e.Memory.EventTime),
			Participants: append([]string(nil), e.Memory.Participants...),
			Location:     e.Memory.Location,
			CreatedAt:    e.CreatedAt,
			UpdatedAt:    e.UpdatedAt,
		})
	}
	return out
}

func hitsFromEntries(entries []*memory.Entry) []memoryHit {
	out := make([]memoryHit, 0, len(entries))
	for _, e := range entries {
		if e == nil {
			continue
		}
		out = append(out, memoryHit{
			ID:           e.ID,
			Memory:       e.Memory.Memory,
			Score:        e.Score,
			Kind:         string(e.Memory.Kind),
			EventTime:    formatEventTime(e.Memory.EventTime),
			Participants: append([]string(nil), e.Memory.Participants...),
			Location:     e.Memory.Location,
			CreatedAt:    e.CreatedAt,
			UpdatedAt:    e.UpdatedAt,
		})
	}
	return out
}

func formatEventTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.DateOnly)
}

func formatMemoryMetadata(kind, eventTime string, participants []string, location string) string {
	var parts []string
	if kind != "" {
		parts = append(parts, "kind="+kind)
	}
	if eventTime != "" {
		parts = append(parts, "event_time="+eventTime)
	}
	if len(participants) > 0 {
		parts = append(parts, "participants="+strings.Join(participants, ","))
	}
	if location != "" {
		parts = append(parts, "location="+location)
	}
	return strings.Join(parts, "; ")
}

func diffSnapshots(before, after []memorySnapshot) []memorySnapshot {
	seen := make(map[string]memorySnapshot, len(before))
	for _, mem := range before {
		seen[memoryIdentity(mem)] = mem
	}
	var out []memorySnapshot
	for _, mem := range after {
		prev, ok := seen[memoryIdentity(mem)]
		if !ok || prev.Memory != mem.Memory {
			out = append(out, mem)
		}
	}
	return out
}

func recordProvenance(
	provenance map[string]map[string]bool,
	answerProvenance map[string]bool,
	memories []memorySnapshot,
	sourceSessions []string,
	hasAnswer bool,
) {
	if len(sourceSessions) == 0 {
		return
	}
	for _, mem := range memories {
		key := memoryIdentity(mem)
		if key == "" {
			continue
		}
		sources := provenance[key]
		if sources == nil {
			sources = make(map[string]bool)
			provenance[key] = sources
		}
		for _, sourceSession := range sourceSessions {
			sourceSession = strings.TrimSpace(sourceSession)
			if sourceSession != "" {
				sources[sourceSession] = true
			}
		}
		if hasAnswer {
			answerProvenance[key] = true
		}
	}
}

func annotateSnapshots(
	memories []memorySnapshot,
	provenance map[string]map[string]bool,
	answerProvenance map[string]bool,
) []memorySnapshot {
	out := append([]memorySnapshot(nil), memories...)
	for i := range out {
		out[i].SourceSessions = sourceSessionsForMemory(out[i], provenance)
		out[i].SourceHasAnswer = answerProvenance[memoryIdentity(out[i])]
	}
	return out
}

func annotateHits(
	hits []memoryHit,
	provenance map[string]map[string]bool,
	answerProvenance map[string]bool,
) []memoryHit {
	out := append([]memoryHit(nil), hits...)
	for i := range out {
		mem := memorySnapshot{
			ID:     out[i].ID,
			Memory: out[i].Memory,
		}
		out[i].SourceSessions = sourceSessionsForMemory(mem, provenance)
		out[i].SourceHasAnswer = answerProvenance[memoryIdentity(mem)]
	}
	return out
}

func sourceSessionsForMemory(mem memorySnapshot, provenance map[string]map[string]bool) []string {
	sources := provenance[memoryIdentity(mem)]
	if len(sources) == 0 {
		return nil
	}
	out := make([]string, 0, len(sources))
	for source := range sources {
		out = append(out, source)
	}
	sort.Strings(out)
	return out
}

func memoryIdentity(mem memorySnapshot) string {
	if mem.ID != "" {
		return mem.ID
	}
	h := sha1.Sum([]byte(mem.Memory))
	return hex.EncodeToString(h[:])
}

func traceMessages(messages []model.Message) []traceMessage {
	out := make([]traceMessage, 0, len(messages))
	for _, msg := range messages {
		out = append(out, traceMessage{Role: msg.Role.String(), Content: msg.Content})
	}
	return out
}

func computeEvidenceMetrics(inst *lmeInstance, br *backendResult, topK int) *evidenceMetrics {
	ev := &evidenceMetrics{
		IsAbstention:     isAbstentionQuestion(inst),
		TopK:             topK,
		AnswerSessionIDs: append([]string(nil), inst.AnswerSessionIDs...),
	}
	ev.HasAnswerTurnLabels = hasAnswerTurnLabels(inst)
	answerSet := make(map[string]bool, len(inst.AnswerSessionIDs))
	for _, id := range inst.AnswerSessionIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		answerSet[id] = true
	}
	ev.HasEvidenceLabels = len(answerSet) > 0 && !ev.IsAbstention
	ev.ExtractedSourceSessions = matchingSourceSessionsFromSnapshots(br.FinalMemories, answerSet)
	ev.RetrievedSourceSessions = matchingSourceSessionsFromHits(br.Retrieval, answerSet)
	ev.ExtractTurnRecallAny = hasAnswerSourceSnapshot(br.FinalMemories)
	ev.RetrievalTurnRecallAny = hasAnswerSourceHit(br.Retrieval)
	if !ev.HasEvidenceLabels {
		return ev
	}
	extractedSet := stringSet(ev.ExtractedSourceSessions)
	retrievedSet := stringSet(ev.RetrievedSourceSessions)
	ev.ExtractRecallAny = intersects(extractedSet, answerSet)
	ev.RetrievalRecallAny = intersects(retrievedSet, answerSet)
	ev.RetrievalRecallAll = containsAll(retrievedSet, answerSet)
	return ev
}

func matchingSourceSessionsFromSnapshots(memories []memorySnapshot, answerSet map[string]bool) []string {
	seen := make(map[string]bool)
	for _, mem := range memories {
		for _, source := range mem.SourceSessions {
			if answerSet[source] {
				seen[source] = true
			}
		}
	}
	return sortedSet(seen)
}

func matchingSourceSessionsFromHits(hits []memoryHit, answerSet map[string]bool) []string {
	seen := make(map[string]bool)
	for _, hit := range hits {
		for _, source := range hit.SourceSessions {
			if answerSet[source] {
				seen[source] = true
			}
		}
	}
	return sortedSet(seen)
}

func hasAnswerSourceSnapshot(memories []memorySnapshot) bool {
	for _, mem := range memories {
		if mem.SourceHasAnswer {
			return true
		}
	}
	return false
}

func hasAnswerSourceHit(hits []memoryHit) bool {
	for _, hit := range hits {
		if hit.SourceHasAnswer {
			return true
		}
	}
	return false
}

func classifyFailure(inst *lmeInstance, br *backendResult) string {
	if br.Error != "" {
		return "backend_error"
	}
	if br.Evidence == nil {
		return "unknown"
	}
	if br.Evidence.IsAbstention {
		if !*flagLMEAnswer {
			return "retrieval_only"
		}
		if isUnknownAnswer(br.Answer) {
			return "ok_abstention"
		}
		return "abstention_answered"
	}
	if !br.Evidence.HasEvidenceLabels {
		return "no_evidence_labels"
	}
	if br.Evidence.HasAnswerTurnLabels && !br.Evidence.ExtractTurnRecallAny {
		return "extract_miss"
	}
	if !br.Evidence.ExtractRecallAny {
		return "extract_miss"
	}
	if br.Evidence.HasAnswerTurnLabels && !br.Evidence.RetrievalTurnRecallAny {
		return "retrieval_miss"
	}
	if !br.Evidence.RetrievalRecallAny {
		return "retrieval_miss"
	}
	if !*flagLMEAnswer {
		return "retrieval_only"
	}
	if br.ExactMatch || br.F1 >= 0.8 {
		return "ok"
	}
	return "answer_miss"
}

func isAbstentionQuestion(inst *lmeInstance) bool {
	return inst != nil && strings.HasSuffix(inst.QuestionID, "_abs")
}

func hasAnswerTurnLabels(inst *lmeInstance) bool {
	if inst == nil {
		return false
	}
	for _, turns := range inst.HaystackSessions {
		for _, turn := range turns {
			if turn.HasAnswer {
				return true
			}
		}
	}
	return false
}

func isUnknownAnswer(answer string) bool {
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer == "" {
		return false
	}
	unknowns := []string{
		"i don't know",
		"i do not know",
		"unknown",
		"not enough information",
		"not mentioned",
		"cannot answer",
	}
	for _, phrase := range unknowns {
		if strings.Contains(answer, phrase) {
			return true
		}
	}
	return false
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			out[v] = true
		}
	}
	return out
}

func intersects(a, b map[string]bool) bool {
	for k := range a {
		if b[k] {
			return true
		}
	}
	return false
}

func containsAll(have, want map[string]bool) bool {
	if len(want) == 0 {
		return false
	}
	for k := range want {
		if !have[k] {
			return false
		}
	}
	return true
}

func sortedSet(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for v := range values {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func exactAnswerMatch(prediction, reference string) bool {
	prediction = normalizeExactAnswer(prediction)
	reference = normalizeExactAnswer(reference)
	if reference == "" {
		return prediction == ""
	}
	return prediction == reference
}

func normalizeExactAnswer(answer string) string {
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer == "" {
		return ""
	}
	answer = strings.ReplaceAll(answer, "<\uff5cend\u2581of\u2581sentence\uff5c>", " ")
	var b strings.Builder
	for _, r := range answer {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func parseLMEDate(date string) (time.Time, bool) {
	date = strings.TrimSpace(date)
	if date == "" {
		return time.Time{}, false
	}
	if i := strings.Index(date, "("); i >= 0 {
		if j := strings.Index(date[i:], ")"); j >= 0 {
			date = strings.TrimSpace(date[:i] + date[i+j+1:])
		}
	}
	t, err := time.ParseInLocation("2006/01/02 15:04", date, time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func lmeUnixTimestamp(date string) (int64, bool) {
	t, ok := parseLMEDate(date)
	if !ok {
		return 0, false
	}
	return t.Unix(), true
}

func lmeObservationDate(date string) (string, bool) {
	t, ok := parseLMEDate(date)
	if !ok {
		return "", false
	}
	return t.Format(time.DateOnly), true
}

func isRetryableMem0Error(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func isRetryableMem0Status(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func longMemEvalMem0OSSRequestTimeout() time.Duration {
	timeout := 90 * time.Second
	if flagLMEModelCallTimeout != nil && *flagLMEModelCallTimeout > 0 && *flagLMEModelCallTimeout < timeout {
		timeout = *flagLMEModelCallTimeout
	}
	return timeout
}

func contextWithOptionalTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return ctx, func() {}
	}
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining <= timeout {
			return ctx, func() {}
		}
	}
	return context.WithTimeout(ctx, timeout)
}

func appendError(base, next string) string {
	if base == "" {
		return next
	}
	if next == "" {
		return base
	}
	return base + "; " + next
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}
