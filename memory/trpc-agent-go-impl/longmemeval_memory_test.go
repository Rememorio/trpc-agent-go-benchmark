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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

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

func TestMem0OSSIngestRetriesTransientStatus(t *testing.T) {
	t.Parallel()

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if r.URL.Path != "/memories" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if attempts == 1 {
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
	if attempts != 2 {
		t.Fatalf("unexpected attempts: got %d want 2", attempts)
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
	if !strings.Contains(prompt, "LongMemEval expects the user's preference profile") {
		t.Fatalf("missing preference guidance: %s", prompt)
	}
	if !strings.Contains(prompt, "do not say\n\"I don't know\"") {
		t.Fatalf("missing unknown-answer guard: %s", prompt)
	}
	if !strings.Contains(prompt, "When any retrieved memory is relevant to the preference topic") {
		t.Fatalf("missing relevant-memory guard: %s", prompt)
	}
	if !strings.Contains(prompt, "not a recommendation list") {
		t.Fatalf("missing recommendation-list guard: %s", prompt)
	}
	if !strings.Contains(prompt, "Start\nwith \"The user would prefer\"") {
		t.Fatalf("missing preference start constraint: %s", prompt)
	}
	if !strings.Contains(prompt, "compatibility, quality") {
		t.Fatalf("missing concrete preference dimensions: %s", prompt)
	}
	if !strings.Contains(prompt, "The user would prefer") {
		t.Fatalf("missing preference answer shape: %s", prompt)
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

	if strings.Contains(prompt, "preference profile") {
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

	if !parseLongMemEvalJudge("Yes.") {
		t.Fatal("yes response should be correct")
	}
	if !parseLongMemEvalJudge("yesThe model response is correct.") {
		t.Fatal("yes prefix should be correct")
	}
	if !parseLongMemEvalJudge("The answer is yes.") {
		t.Fatal("yes-only fallback should be correct")
	}
	if parseLongMemEvalJudge("No.") {
		t.Fatal("no response should not be correct")
	}
	if parseLongMemEvalJudge("No, not yes.") {
		t.Fatal("first-token no should not be correct")
	}
	if !parseLongMemEvalJudge("The response correctly recalls and uses the user's personal information.") {
		t.Fatal("positive explanatory judge response should be correct")
	}
	if parseLongMemEvalJudge("The response does not satisfy the rubric.") {
		t.Fatal("negative explanatory judge response should not be correct")
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
}

func TestCompareLongMemEvalResults(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	candidateDir := t.TempDir()
	outputDir := t.TempDir()
	basePath := filepath.Join(baseDir, "results.json")
	candidatePath := filepath.Join(candidateDir, "results.json")
	base := &runResult{
		Summary: &runSummary{TotalCases: 1},
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
		}},
	}
	candidate := &runResult{
		Summary: &runSummary{TotalCases: 1},
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
