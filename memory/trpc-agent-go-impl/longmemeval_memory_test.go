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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func (s *lmeExtractorStub) Extract(
	context.Context,
	[]model.Message,
	[]*memory.Entry,
) ([]*extractor.Operation, error) {
	return s.ops, nil
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
}

func TestWithObservationDate(t *testing.T) {
	t.Parallel()

	got := withObservationDate("The Fitbit has been used for 9 months.", "2023/04/10 (Mon) 14:47")
	if !strings.HasPrefix(got, "Observation date: 2023/04/10 (Mon) 14:47\n") {
		t.Fatalf("missing observation date prefix: %q", got)
	}
	if !strings.Contains(got, "Do not use today's system date") {
		t.Fatalf("missing system-date guard: %q", got)
	}
	if !strings.Contains(got, "The Fitbit has been used for 9 months.") {
		t.Fatalf("missing original content: %q", got)
	}
	if out := withObservationDate("content", "  "); out != "content" {
		t.Fatalf("empty date should leave content unchanged: %q", out)
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
	if err != nil || config != nil || configured {
		t.Fatalf("disabled runtime configuration should be a no-op: config=%#v err=%v", config, err)
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
		Kind:         "episode",
		EventTime:    "2023-05-20",
		Participants: []string{"Alice"},
		Location:     "Community Center",
	}})

	if !strings.Contains(prompt, "Question type: single-session-preference") {
		t.Fatalf("missing question type: %s", prompt)
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

func TestPostprocessLongMemEvalAnswerCompletesTruncatedListItem(t *testing.T) {
	t.Parallel()

	inst := &lmeInstance{
		QuestionType: "temporal-reasoning",
		Question:     "What is the order of the museums from earliest to latest?",
	}
	raw := "Science Museum, Museum of Contemporary Art, Natural"
	hits := []memoryHit{{Memory: "User visited the Natural History Museum on 2023-03-04."}}

	got := postprocessLongMemEvalAnswer(inst, hits, raw)
	want := "Science Museum, Museum of Contemporary Art, Natural History Museum"
	if got != want {
		t.Fatalf("unexpected answer: got %q want %q", got, want)
	}
}

func TestPostprocessLongMemEvalAnswerExtractsNumberedOrder(t *testing.T) {
	t.Parallel()

	inst := &lmeInstance{
		QuestionType: "temporal-reasoning",
		Question:     "What is the order of the six museums I visited from earliest to latest?",
	}
	raw := `Let me find the visits.
1. Science Museum - January 15, 2023
2. Museum of Contemporary Art - around January 15, 2023
3. Metropolitan Museum of Art - February 10, 2023`

	got := postprocessLongMemEvalAnswer(inst, nil, raw)
	want := "Science Museum, Museum of Contemporary Art, Metropolitan Museum of Art"
	if got != want {
		t.Fatalf("unexpected answer: got %q want %q", got, want)
	}
}

func TestPostprocessLongMemEvalAnswerSumsShortCountAnswer(t *testing.T) {
	t.Parallel()

	inst := &lmeInstance{
		QuestionType: "multi-session",
		Question:     "How many plants did I initially plant for tomatoes and cucumbers?",
	}
	got := postprocessLongMemEvalAnswer(inst, nil, "5 tomato plants and 3 cucumber plants")
	if got != "8" {
		t.Fatalf("unexpected answer: got %q want 8", got)
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
	if req.MaxTokens == nil || *req.MaxTokens != 512 {
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
					},
				},
			},
		}},
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

func TestCompareLongMemEvalResults(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	candidateDir := t.TempDir()
	outputDir := t.TempDir()
	basePath := filepath.Join(baseDir, "results.json")
	candidatePath := filepath.Join(candidateDir, "results.json")
	base := &runResult{
		Summary: &runSummary{TotalCases: 2},
		Cases: []*caseResult{{
			QuestionID:   "q1",
			QuestionType: "single-session-assistant",
			Question:     "What was the fifth bottle?",
			Answer:       "Absinthe",
			BackendResults: map[string]*backendResult{
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
				"pgvector": {
					Backend:      "pgvector",
					FailureStage: "answer_miss",
					Answer:       "The recommendation was Option B.",
					F1:           0.5,
					Judge:        &lmeJudgeResult{Correct: true},
				},
			},
		}},
	}
	candidate := &runResult{
		Summary: &runSummary{TotalCases: 2},
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
					Judge:        &lmeJudgeResult{Correct: false},
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
	}
	rows := compareLongMemEvalRows(
		longMemEvalAnalysisRows(base),
		longMemEvalAnalysisRows(candidate),
	)
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

type capturingModel struct {
	request *model.Request
}

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
	}}}
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
