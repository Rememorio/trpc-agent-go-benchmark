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
	"net/http"
	"os"
	"path/filepath"
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
	IngestPair(ctx context.Context, sess *session.Session, meta ingestMeta) error
	Flush(ctx context.Context) error
	Search(ctx context.Context, userKey memory.UserKey, query string, topK int) ([]memoryHit, error)
	Read(ctx context.Context, userKey memory.UserKey, limit int) ([]memorySnapshot, error)
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
	CreatedAt       time.Time `json:"created_at,omitempty"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
}

type memorySnapshot struct {
	ID              string    `json:"id,omitempty"`
	Memory          string    `json:"memory"`
	Score           float64   `json:"score,omitempty"`
	SourceSessions  []string  `json:"source_sessions,omitempty"`
	SourceHasAnswer bool      `json:"source_has_answer,omitempty"`
	CreatedAt       time.Time `json:"created_at,omitempty"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
}

type ingestTrace struct {
	SessionIndex int              `json:"session_index"`
	SessionID    string           `json:"session_id"`
	Date         string           `json:"date,omitempty"`
	PairIndex    int              `json:"pair_index"`
	HasAnswer    bool             `json:"has_answer,omitempty"`
	Messages     []traceMessage   `json:"messages"`
	NewMemories  []memorySnapshot `json:"new_memories,omitempty"`
	MemoryCount  int              `json:"memory_count"`
	TokenUsage   *lmeTokenUsage   `json:"token_usage,omitempty"`
	Error        string           `json:"error,omitempty"`
	DurationMs   int64            `json:"duration_ms"`
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
	Backend        string           `json:"backend"`
	UserID         string           `json:"user_id"`
	SessionID      string           `json:"session_id"`
	IngestedPairs  int              `json:"ingested_pairs"`
	IngestTraces   []ingestTrace    `json:"ingest_traces"`
	FinalMemories  []memorySnapshot `json:"final_memories"`
	Retrieval      []memoryHit      `json:"retrieval"`
	Answer         string           `json:"answer,omitempty"`
	TokenUsage     *lmeTokenUsage   `json:"token_usage,omitempty"`
	AnswerUsage    *lmeTokenUsage   `json:"answer_token_usage,omitempty"`
	Evidence       *evidenceMetrics `json:"evidence,omitempty"`
	FailureStage   string           `json:"failure_stage,omitempty"`
	ExactMatch     bool             `json:"exact_match"`
	F1             float64          `json:"f1"`
	BLEU           float64          `json:"bleu"`
	IngestDuration int64            `json:"ingest_duration_ms"`
	SearchDuration int64            `json:"search_duration_ms"`
	AnswerDuration int64            `json:"answer_duration_ms,omitempty"`
	Error          string           `json:"error,omitempty"`
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
}

type backendSummary struct {
	Cases              int           `json:"cases"`
	ExactMatches       int           `json:"exact_matches"`
	TotalPairs         int           `json:"total_pairs"`
	TotalMemories      int           `json:"total_memories"`
	TotalHits          int           `json:"total_hits"`
	EvidenceCases      int           `json:"evidence_cases"`
	ExtractRecallAny   int           `json:"extract_recall_any"`
	RetrievalRecallAny int           `json:"retrieval_recall_any"`
	RetrievalRecallAll int           `json:"retrieval_recall_all"`
	TurnEvidenceCases  int           `json:"turn_evidence_cases"`
	ExtractTurnAny     int           `json:"extract_turn_recall_any"`
	RetrievalTurnAny   int           `json:"retrieval_turn_recall_any"`
	AvgF1              float64       `json:"avg_f1"`
	AvgBLEU            float64       `json:"avg_bleu"`
	TokenUsage         lmeTokenUsage `json:"token_usage"`
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

type lmeTokenUsage struct {
	PromptTokens        int     `json:"prompt_tokens"`
	CompletionTokens    int     `json:"completion_tokens"`
	TotalTokens         int     `json:"total_tokens"`
	CachedTokens        int     `json:"cached_tokens,omitempty"`
	CacheCreationTokens int     `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int     `json:"cache_read_tokens,omitempty"`
	ReasoningTokens     int     `json:"reasoning_tokens,omitempty"`
	LLMCalls            int     `json:"llm_calls"`
	CacheHitRate        float64 `json:"cache_hit_rate,omitempty"`
}

type lmeTokenTracker struct {
	mu    sync.Mutex
	usage lmeTokenUsage
}

func (t *lmeTokenTracker) Record(u *model.Usage) {
	if t == nil || u == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.usage.PromptTokens += u.PromptTokens
	t.usage.CompletionTokens += u.CompletionTokens
	t.usage.TotalTokens += u.TotalTokens
	t.usage.CachedTokens += u.PromptTokensDetails.CachedTokens
	t.usage.CacheCreationTokens += u.PromptTokensDetails.CacheCreationTokens
	t.usage.CacheReadTokens += u.PromptTokensDetails.CacheReadTokens
	t.usage.ReasoningTokens += u.CompletionTokensDetails.ReasoningTokens
	t.usage.LLMCalls++
	t.usage.setCacheHitRate()
}

func (t *lmeTokenTracker) Snapshot() lmeTokenUsage {
	if t == nil {
		return lmeTokenUsage{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	snap := t.usage
	t.usage = lmeTokenUsage{}
	return snap
}

type lmeTrackingModel struct {
	base    model.Model
	tracker *lmeTokenTracker
}

func (m *lmeTrackingModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	respCh, err := m.base.GenerateContent(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make(chan *model.Response)
	go func() {
		defer close(out)
		for resp := range respCh {
			if resp != nil && resp.Usage != nil {
				m.tracker.Record(resp.Usage)
			}
			out <- resp
		}
	}()
	return out, nil
}

func (m *lmeTrackingModel) Info() model.Info { return m.base.Info() }

func (u *lmeTokenUsage) Add(other lmeTokenUsage) {
	u.PromptTokens += other.PromptTokens
	u.CompletionTokens += other.CompletionTokens
	u.TotalTokens += other.TotalTokens
	u.CachedTokens += other.CachedTokens
	u.CacheCreationTokens += other.CacheCreationTokens
	u.CacheReadTokens += other.CacheReadTokens
	u.ReasoningTokens += other.ReasoningTokens
	u.LLMCalls += other.LLMCalls
	u.setCacheHitRate()
}

func (u *lmeTokenUsage) setCacheHitRate() {
	if u.PromptTokens <= 0 {
		u.CacheHitRate = 0
		return
	}
	u.CacheHitRate = float64(u.CachedTokens) / float64(u.PromptTokens)
}

func (u lmeTokenUsage) IsZero() bool {
	return u.PromptTokens == 0 &&
		u.CompletionTokens == 0 &&
		u.TotalTokens == 0 &&
		u.CachedTokens == 0 &&
		u.CacheCreationTokens == 0 &&
		u.CacheReadTokens == 0 &&
		u.ReasoningTokens == 0 &&
		u.LLMCalls == 0
}

func tokenUsagePtr(u lmeTokenUsage) *lmeTokenUsage {
	if u.IsZero() {
		return nil
	}
	u.setCacheHitRate()
	return &u
}

type pgvectorBackend struct {
	svc memory.Service
	ext extractor.MemoryExtractor
}

func (b *pgvectorBackend) Name() string { return "pgvector" }

func (b *pgvectorBackend) Flush(ctx context.Context) error { return nil }

func (b *pgvectorBackend) IngestPair(ctx context.Context, sess *session.Session, meta ingestMeta) error {
	userKey := memory.UserKey{AppName: sess.AppName, UserID: sess.UserID}
	existing, err := b.svc.ReadMemories(ctx, userKey, 500)
	if err != nil {
		return err
	}
	messages := latestPairMessages(sess)
	if len(messages) == 0 {
		return nil
	}
	if t, ok := parseLMEDate(meta.Date); ok {
		ctx = extractor.WithReferenceDate(ctx, t)
	}
	ops, err := b.ext.Extract(ctx, messages, existing)
	if err != nil {
		return err
	}
	for _, op := range ops {
		if err := executeOperation(ctx, b.svc, userKey, op); err != nil {
			return err
		}
	}
	return nil
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

func (b *pgvectorBackend) Close() error { return b.svc.Close() }

type mem0Backend struct {
	svc        *memorymem0.Service
	host       string
	selfHosted bool
	httpClient *http.Client
}

func (b *mem0Backend) Name() string { return "mem0" }

func (b *mem0Backend) Flush(ctx context.Context) error { return b.svc.Close() }

func (b *mem0Backend) IngestPair(ctx context.Context, sess *session.Session, meta ingestMeta) error {
	if b.selfHosted {
		return b.ingestPairOSS(ctx, sess, meta)
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
	return b.svc.IngestSession(ctx, pairSess,
		session.WithIngestAgentID(lmeAgentName),
		session.WithIngestRunID(meta.RunID),
		session.WithIngestMetadata(metadata),
	)
}

func (b *mem0Backend) ingestPairOSS(ctx context.Context, sess *session.Session, meta ingestMeta) error {
	messages := latestPairMessages(sess)
	apiMsgs := make([]map[string]string, 0, len(messages))
	for _, msg := range messages {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		apiMsgs = append(apiMsgs, map[string]string{
			"role":    msg.Role.String(),
			"content": withObservationDate(content, meta.Date),
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(b.host, "/")+"/memories", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := b.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("mem0 OSS ingest failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
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

func (b *mem0Backend) Close() error { return b.svc.Close() }

func runLongMemEvalMemory(ctx context.Context) error {
	if path := strings.TrimSpace(*flagLMEAnalyzeResults); path != "" {
		return analyzeLongMemEvalResults(path, longMemEvalAnalysisOutputDir(path))
	}
	if err := os.MkdirAll(*flagOutput, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	modelName := getModelName()
	baseLLM := openaimodel.New(modelName)
	datasetPath := resolveLongMemEvalDatasetPath()
	instances, err := loadLongMemEval(datasetPath)
	if err != nil {
		return fmt.Errorf("load dataset: %w", err)
	}
	cases := filterCases(instances)
	if len(cases) == 0 {
		return fmt.Errorf("no cases selected")
	}

	backends := parseMemoryBackends(*flagMemoryBackends)
	runID := time.Now().UTC().Format("20060102T150405Z")
	results := &runResult{
		Metadata: map[string]any{
			"benchmark":               "longmemeval-memory",
			"dataset":                 datasetPath,
			"model":                   modelName,
			"backends":                backends,
			"top_k":                   *flagVectorTopK,
			"run_id":                  runID,
			"started_at":              time.Now().UTC().Format(time.RFC3339),
			"max_sessions":            *flagLMEMaxSessions,
			"max_pairs":               *flagLMEMaxPairs,
			"sample_per_type":         *flagLMEPerType,
			"sample_abstention_count": *flagLMEAbstentionCount,
			"sample_seed":             *flagLMESampleSeed,
			"selected_question_ids":   questionIDs(cases),
			"ingest_policy":           "chronological session replay; trigger extraction after each user/assistant pair",
			"retrieval_note":          "retrieval hits are searched memories, not raw transcript chunks",
			"evidence_note":           "source_sessions are inferred from the pair after which a memory first appeared or changed.",
			"token_usage_scope": "LLM calls made through the trpc-agent-go model in this process. " +
				"Embedding provider usage and mem0 self-hosted server internal LLM usage are not reported.",
		},
		Cases: make([]*caseResult, 0, len(cases)),
	}

	log.Printf("LongMemEval memory run: cases=%d backends=%v model=%s", len(cases), backends, modelName)
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
			llm := &lmeTrackingModel{base: baseLLM, tracker: tracker}
			backend, err := newBackend(backendName, llm)
			if err != nil {
				cr.BackendResults[backendName] = &backendResult{Backend: backendName, Error: err.Error()}
				log.Printf("  %s create failed: %v", backendName, err)
				continue
			}
			tracker.Snapshot()
			br := runCaseBackend(ctx, llm, tracker, backend, inst, runID)
			cr.BackendResults[backendName] = br
			_ = backend.Close()
			log.Printf("  %s pairs=%d memories=%d hits=%d evidence=%s calls=%d tokens=%d cached=%d em=%v f1=%.3f answer=%q",
				backendName, br.IngestedPairs, len(br.FinalMemories), len(br.Retrieval),
				br.FailureStage,
				tokenCalls(br.TokenUsage), tokenTotal(br.TokenUsage), tokenCached(br.TokenUsage),
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
			err := backend.IngestPair(ctx, sess, ingestMeta{
				QuestionID: inst.QuestionID,
				SessionID:  s.ID,
				SessionIdx: s.OriginalIndex,
				PairIdx:    pairIdx,
				HasAnswer:  pair.HasAnswer,
				Date:       s.Date,
				RunID:      runID,
			})
			if *flagLMEIngestWait > 0 {
				time.Sleep(*flagLMEIngestWait)
			}
			memories, readErr := backend.Read(ctx, userKey, 500)
			if readErr != nil && err == nil {
				err = readErr
			}
			usage := tracker.Snapshot()
			trace.TokenUsage = tokenUsagePtr(usage)
			if trace.TokenUsage != nil {
				if br.TokenUsage == nil {
					br.TokenUsage = &lmeTokenUsage{}
				}
				br.TokenUsage.Add(usage)
			}
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
	if err != nil {
		br.Error = appendError(br.Error, "search: "+err.Error())
	}
	br.Retrieval = annotateHits(hits, provenance, answerProvenance)

	if *flagLMEAnswer {
		answerStart := time.Now()
		answer, err := answerFromMemories(ctx, llm, inst, hits)
		usage := tracker.Snapshot()
		br.AnswerDuration = time.Since(answerStart).Milliseconds()
		br.AnswerUsage = tokenUsagePtr(usage)
		if br.AnswerUsage != nil {
			if br.TokenUsage == nil {
				br.TokenUsage = &lmeTokenUsage{}
			}
			br.TokenUsage.Add(usage)
		}
		if err != nil {
			br.Error = appendError(br.Error, "answer: "+err.Error())
		}
		br.Answer = answer
	}
	usage := tracker.Snapshot()
	if !usage.IsZero() {
		if br.TokenUsage == nil {
			br.TokenUsage = &lmeTokenUsage{}
		}
		br.TokenUsage.Add(usage)
	}
	br.ExactMatch = containsExactMatch(br.Answer, inst.Answer.String())
	br.F1 = metrics.CalculateF1(br.Answer, inst.Answer.String())
	br.BLEU = metrics.CalculateBLEU(br.Answer, inst.Answer.String())
	br.Evidence = computeEvidenceMetrics(inst, br, *flagVectorTopK)
	br.FailureStage = classifyFailure(inst, br)
	return br
}

func newBackend(name string, llm model.Model) (memoryBackend, error) {
	switch strings.TrimSpace(name) {
	case "pgvector":
		dsn := getPGVectorDSN()
		if dsn == "" {
			return nil, fmt.Errorf("pgvector-dsn or PGVECTOR_DSN is required")
		}
		emb := newEmbeddingEmbedder(getEmbedModelName())
		tableName := tableNameWithSuffix(lmePGVectorTableBase)
		svc, err := memorypgvector.NewService(
			memorypgvector.WithPGVectorClientDSN(dsn),
			memorypgvector.WithTableName(tableName),
			memorypgvector.WithEmbedder(emb),
			memorypgvector.WithIndexDimension(emb.GetDimensions()),
			memorypgvector.WithMaxResults(*flagVectorTopK),
		)
		if err != nil {
			return nil, err
		}
		return &pgvectorBackend{svc: svc, ext: extractor.NewExtractor(llm)}, nil
	case "mem0":
		host := strings.TrimSpace(*flagMem0Host)
		if host == "" {
			host = os.Getenv("MEM0_HOST")
		}
		if host == "" {
			host = defaultMem0Host
		}
		timeout := 90 * time.Second
		opts := []memorymem0.ServiceOpt{
			memorymem0.WithHost(host),
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
			httpClient: &http.Client{Timeout: timeout},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported backend %q", name)
	}
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

func executeOperation(
	ctx context.Context,
	svc memory.Service,
	userKey memory.UserKey,
	op *extractor.Operation,
) error {
	if op == nil {
		return nil
	}
	switch op.Type {
	case extractor.OperationAdd:
		return svc.AddMemory(ctx, userKey, op.Memory, op.Topics,
			memory.WithMetadata(operationMetadata(op)))
	case extractor.OperationUpdate:
		key := memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: op.MemoryID}
		return svc.UpdateMemory(ctx, key, op.Memory, op.Topics,
			memory.WithUpdateMetadata(operationMetadata(op)))
	case extractor.OperationDelete:
		key := memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: op.MemoryID}
		return svc.DeleteMemory(ctx, key)
	case extractor.OperationClear:
		return svc.ClearMemories(ctx, userKey)
	default:
		return fmt.Errorf("unknown memory operation %q", op.Type)
	}
}

func operationMetadata(op *extractor.Operation) *memory.Metadata {
	kind := op.MemoryKind
	if kind == "" {
		kind = memory.KindFact
	}
	return &memory.Metadata{
		Kind:         kind,
		EventTime:    op.EventTime,
		Participants: op.Participants,
		Location:     op.Location,
	}
}

func answerFromMemories(ctx context.Context, llm model.Model, inst *lmeInstance, hits []memoryHit) (string, error) {
	var b strings.Builder
	if len(hits) == 0 {
		b.WriteString("(no memories retrieved)\n")
	} else {
		for i, hit := range hits {
			fmt.Fprintf(&b, "%d. %s", i+1, hit.Memory)
			if hit.Score != 0 {
				fmt.Fprintf(&b, " (score=%.4f)", hit.Score)
			}
			b.WriteByte('\n')
		}
	}
	prompt := fmt.Sprintf(`You are answering a LongMemEval memory question.

Use only the retrieved memories below. If the memories do not contain enough information, answer "I don't know".

Question date: %s
Question: %s

Retrieved memories:
%s

Answer with a concise final answer only.`, inst.QuestionDate, inst.Question, b.String())

	maxTokens := 512
	temp := 0.0
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage(prompt)},
		GenerationConfig: model.GenerationConfig{
			Stream:      false,
			MaxTokens:   &maxTokens,
			Temperature: &temp,
		},
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
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
		if out != "" {
			return out, nil
		}
		lastErr = errors.New("model returned empty answer")
		time.Sleep(time.Duration(attempt+1) * time.Second)
	}
	return "", lastErr
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
	typeSet := make(map[string]bool)
	for _, t := range parseCommaList(*flagLMEQuestionTypes) {
		typeSet[t] = true
	}
	filtered := make([]*lmeInstance, 0, len(instances))
	for _, inst := range instances {
		if inst == nil {
			continue
		}
		if *flagLMEQuestionID != "" && inst.QuestionID != *flagLMEQuestionID {
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
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		log.Printf("marshal results: %v", err)
		return
	}
	if err := os.WriteFile(filepath.Join(outputDir, "results.json"), data, 0644); err != nil {
		log.Printf("write results: %v", err)
	}
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
			fmt.Printf("  %s: pairs=%d memories=%d hits=%d stage=%s calls=%d tokens=%d cached=%d EM=%v F1=%.3f BLEU=%.3f err=%s\n",
				br.Backend, br.IngestedPairs, len(br.FinalMemories), len(br.Retrieval),
				br.FailureStage,
				tokenCalls(br.TokenUsage), tokenTotal(br.TokenUsage), tokenCached(br.TokenUsage),
				br.ExactMatch, br.F1, br.BLEU, br.Error)
		}
	}
	if result.Summary == nil {
		return
	}
	fmt.Println("\n--- Backend Summary ---")
	for backend, summary := range result.Summary.BackendSummaries {
		fmt.Printf("  %s: cases=%d EM=%d evidence=%d extractAny=%d retrievalAny=%d retrievalAll=%d turnEvidence=%d turnExtractAny=%d turnRetrievalAny=%d avgF1=%.3f avgBLEU=%.3f calls=%d tokens=%d cached=%d cacheHit=%.3f\n",
			backend, summary.Cases, summary.ExactMatches,
			summary.EvidenceCases, summary.ExtractRecallAny, summary.RetrievalRecallAny, summary.RetrievalRecallAll,
			summary.TurnEvidenceCases, summary.ExtractTurnAny, summary.RetrievalTurnAny,
			summary.AvgF1, summary.AvgBLEU,
			summary.TokenUsage.LLMCalls, summary.TokenUsage.TotalTokens,
			summary.TokenUsage.CachedTokens, summary.TokenUsage.CacheHitRate)
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
			ID:        e.ID,
			Memory:    e.Memory.Memory,
			Score:     e.Score,
			CreatedAt: e.CreatedAt,
			UpdatedAt: e.UpdatedAt,
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
			ID:        e.ID,
			Memory:    e.Memory.Memory,
			Score:     e.Score,
			CreatedAt: e.CreatedAt,
			UpdatedAt: e.UpdatedAt,
		})
	}
	return out
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

func containsExactMatch(prediction, reference string) bool {
	prediction = strings.ToLower(strings.TrimSpace(prediction))
	reference = strings.ToLower(strings.TrimSpace(reference))
	if reference == "" {
		return prediction == ""
	}
	return strings.Contains(prediction, reference)
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

func withObservationDate(content, date string) string {
	date = strings.TrimSpace(date)
	if date == "" {
		return content
	}
	return fmt.Sprintf("Observation date: %s\n%s", date, content)
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
