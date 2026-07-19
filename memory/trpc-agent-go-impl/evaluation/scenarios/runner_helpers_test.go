//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package scenarios

import (
	"context"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go-benchmark/memory/trpc-agent-go-impl/evaluation/dataset"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type recoveryModel struct {
	request      *model.Request
	text         string
	empty        bool
	finishReason string
	calls        int
}

func (m *recoveryModel) GenerateContent(
	_ context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	m.request = request
	m.calls++
	responseText := m.text
	if responseText == "" && !m.empty {
		responseText = `{"answer":"recovered answer"}`
	}
	finishReason := m.finishReason
	if finishReason == "" {
		finishReason = "stop"
	}
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Choices: []model.Choice{{
			Message:      model.NewAssistantMessage(responseText),
			FinishReason: &finishReason,
		}},
		Usage: &model.Usage{
			PromptTokens:     20,
			CompletionTokens: 3,
			TotalTokens:      23,
			PromptTokensDetails: model.PromptTokensDetails{
				CachedTokens: 4,
			},
		},
	}
	close(ch)
	return ch, nil
}

func (*recoveryModel) Info() model.Info {
	return model.Info{}
}

func TestSessionMessagesAnchorsOpeningSpeakerAsUser(t *testing.T) {
	sess := dataset.Session{
		SessionDate: "2:24 pm on 14 August, 2023",
		Turns: []dataset.Turn{
			{Speaker: "Melanie", Text: "We celebrated my daughter's birthday."},
			{Speaker: "Caroline", Text: "What concert was it?"},
			{Speaker: "Melanie", Text: "It was Matt Patterson."},
		},
	}

	got := sessionMessages(sess)
	if len(got) != 4 {
		t.Fatalf("messages = %d, want 4", len(got))
	}
	if got[0].Role != model.RoleSystem {
		t.Fatalf("date role = %q, want system", got[0].Role)
	}
	wantRoles := []model.Role{
		model.RoleUser,
		model.RoleAssistant,
		model.RoleUser,
	}
	for i, want := range wantRoles {
		if got[i+1].Role != want {
			t.Fatalf("message %d role = %q, want %q", i, got[i+1].Role, want)
		}
	}
	if !strings.HasPrefix(got[1].Content, "[Melanie]:") ||
		!strings.HasPrefix(got[2].Content, "[Caroline]:") {
		t.Fatalf("speaker prefixes not retained: %+v", got)
	}
}

func TestQAMemorySearchInstruction_SingleSearch(t *testing.T) {
	got := qaMemorySearchInstruction(1)
	if !strings.Contains(got, "exactly once") {
		t.Fatalf("missing single-search rule: %q", got)
	}
	assertGroundedQAPrompt(t, got)
}

func TestQAMemorySearchInstruction_MultiSearch(t *testing.T) {
	got := qaMemorySearchInstruction(2)
	if !strings.Contains(got, "exactly 2 times") {
		t.Fatalf("missing multi-search rule: %q", got)
	}
	if !strings.Contains(got, fallbackAnswer) {
		t.Fatalf("missing fallback answer: %q", got)
	}
	if !strings.Contains(got, "Search #1") {
		t.Fatalf("missing workflow search marker: %q", got)
	}
	assertGroundedQAPrompt(t, got)
}

func assertGroundedQAPrompt(t *testing.T, got string) {
	t.Helper()
	for _, want := range []string{
		"Topical relevance is not answer support",
		"support the exact subject",
		"unambiguous paraphrase",
		"Never transfer a fact",
		"Never output an empty answer",
		`output exactly "Yes" or "No"`,
		"shortest complete answer span",
		"never replace it with a pronoun or vague category label",
		"every supported part",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing grounding rule %q: %q", want, got)
		}
	}
	for _, unwanted := range []string{
		"When in doubt between answering",
		"If ANY retrieved memory is topically related",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("stale guessing rule %q: %q", unwanted, got)
		}
	}
}

func TestMemorySearchProtocol(t *testing.T) {
	search := ToolCallTrace{Name: "memory_search"}
	tests := []struct {
		name          string
		steps         []StepTrace
		expected      int
		wantCalls     int
		wantViolation bool
	}{
		{
			name: "sequential calls",
			steps: []StepTrace{
				{ToolCalls: []ToolCallTrace{search}},
				{ToolCalls: []ToolCallTrace{search}},
				{},
			},
			expected:  2,
			wantCalls: 2,
		},
		{
			name: "batched calls",
			steps: []StepTrace{
				{ToolCalls: []ToolCallTrace{search, search}},
				{},
			},
			expected:      2,
			wantCalls:     2,
			wantViolation: true,
		},
		{
			name: "missing call",
			steps: []StepTrace{
				{ToolCalls: []ToolCallTrace{search}},
				{},
			},
			expected:      2,
			wantCalls:     1,
			wantViolation: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls, violation := memorySearchProtocol(
				test.steps, test.expected,
			)
			if calls != test.wantCalls {
				t.Fatalf("calls = %d, want %d", calls, test.wantCalls)
			}
			if got := violation != ""; got != test.wantViolation {
				t.Fatalf(
					"violation present = %v, want %v: %q",
					got, test.wantViolation, violation,
				)
			}
		})
	}
}

func TestRecoverMemoryQAAnswer(t *testing.T) {
	m := &recoveryModel{}
	res := collectResult{
		usage: TokenUsage{LLMCalls: 3},
		steps: []StepTrace{
			{
				Step:  1,
				Phase: "memory-search",
				ToolCalls: []ToolCallTrace{{
					Name:   "memory_search",
					Args:   `{"query":"first"}`,
					Result: `{"results":[{"memory":"evidence"}]}`,
				}},
			},
			{Step: 2, Phase: "answer", FinishReason: "length"},
		},
	}
	got, trace := recoverMemoryQAAnswer(
		context.Background(), m, "What happened?", res,
	)
	if trace == nil || !trace.Succeeded {
		t.Fatalf("recovery trace = %+v", trace)
	}
	if trace.Trigger != "empty-answer,finish-reason:length" {
		t.Fatalf("trigger = %q", trace.Trigger)
	}
	if got.text != "recovered answer" {
		t.Fatalf("answer = %q", got.text)
	}
	if got.usage.LLMCalls != 4 || got.usage.TotalTokens != 23 ||
		got.usage.CachedTokens != 4 {
		t.Fatalf("usage = %+v", got.usage)
	}
	if len(got.steps) != 3 || got.steps[2].Phase != "answer-recovery" ||
		got.steps[2].FinishReason != "stop" {
		t.Fatalf("steps = %+v", got.steps)
	}
	if m.request == nil || len(m.request.Messages) != 1 {
		t.Fatalf("request = %+v", m.request)
	}
	if m.request.GenerationConfig.MaxTokens == nil ||
		*m.request.GenerationConfig.MaxTokens != MemoryQARecoveryMaxTokens {
		t.Fatalf(
			"recovery max tokens = %v, want %d",
			m.request.GenerationConfig.MaxTokens,
			MemoryQARecoveryMaxTokens,
		)
	}
	if m.request.StructuredOutput == nil ||
		m.request.StructuredOutput.Type != model.StructuredOutputJSONSchema ||
		m.request.StructuredOutput.JSONSchema == nil ||
		!m.request.StructuredOutput.JSONSchema.Strict {
		t.Fatalf("structured output = %+v", m.request.StructuredOutput)
	}
	properties, ok := m.request.StructuredOutput.JSONSchema.Schema["properties"].(map[string]any)
	if !ok || properties["answer"] == nil {
		t.Fatalf(
			"structured output schema = %+v",
			m.request.StructuredOutput.JSONSchema.Schema,
		)
	}
	prompt := m.request.Messages[0].Content
	for _, want := range []string{
		"What happened?", `{"query":"first"}`, "evidence", `"answer"`,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("recovery prompt missing %q: %q", want, prompt)
		}
	}
}

func TestRecoverMemoryQAAnswerSkipsCompleteAnswer(t *testing.T) {
	res := collectResult{
		text:  "complete",
		steps: []StepTrace{{FinishReason: "stop"}},
	}
	got, trace := recoverMemoryQAAnswer(
		context.Background(), nil, "question", res,
	)
	if trace != nil || got.text != res.text {
		t.Fatalf("got = %+v, trace = %+v", got, trace)
	}
}

func TestMemoryQARecoveryTriggerFormatViolation(t *testing.T) {
	tests := []struct {
		name    string
		answer  string
		trigger string
	}{
		{
			name:    "multiline",
			answer:  "Sweden\nBased on the memories, Sweden",
			trigger: "answer-format:multiline",
		},
		{
			name: "too many words",
			answer: "one two three four five six seven eight nine ten " +
				"eleven twelve thirteen",
			trigger: "answer-format:too-many-words",
		},
		{
			name:    "complete short answer",
			answer:  "Friends, family, and mentors",
			trigger: "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := memoryQARecoveryTrigger(collectResult{text: test.answer})
			if got != test.trigger {
				t.Fatalf("trigger = %q, want %q", got, test.trigger)
			}
		})
	}
}

func TestRecoverMemoryQAAnswerRejectsMalformedRecovery(t *testing.T) {
	m := &recoveryModel{
		text: `{"answer":"one two three four five six seven eight nine ten ` +
			`eleven twelve thirteen"}`,
	}
	res := collectResult{
		text: "This answer is already much too long because it contains " +
			"more than twelve separate words for no useful reason",
		usage: TokenUsage{LLMCalls: 3},
		steps: []StepTrace{{
			Step:  1,
			Phase: "memory-search",
			ToolCalls: []ToolCallTrace{{
				Name:   "memory_search",
				Result: `{"results":[{"memory":"evidence"}]}`,
			}},
		}},
	}

	got, trace := recoverMemoryQAAnswer(
		context.Background(), m, "What happened?", res,
	)
	if trace == nil || trace.Succeeded ||
		!strings.Contains(trace.Error, "too-many-words") {
		t.Fatalf("trace = %+v", trace)
	}
	if got.text != res.text {
		t.Fatalf("answer replaced with malformed recovery: %q", got.text)
	}
	if got.usage.LLMCalls != 4 || got.usage.TotalTokens != 23 {
		t.Fatalf("usage = %+v", got.usage)
	}
	if len(got.steps) != 2 || got.steps[1].Phase != "answer-recovery" {
		t.Fatalf("steps = %+v", got.steps)
	}
	if got.steps[1].Error == "" {
		t.Fatalf("recovery failure missing from step: %+v", got.steps[1])
	}
}

func TestParseMemoryQARecoveryAnswerRejectsInvalidJSON(t *testing.T) {
	_, err := parseMemoryQARecoveryAnswer("not JSON")
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("error = %v", err)
	}
}

func TestParseMemoryQARecoveryAnswerTrimsAnswer(t *testing.T) {
	got, err := parseMemoryQARecoveryAnswer(`{"answer":"  Sweden  "}`)
	if err != nil || got != "Sweden" {
		t.Fatalf("answer = %q, error = %v", got, err)
	}
}

func TestRecoverMemoryQAAnswerRecordsTerminalEmptyResponse(t *testing.T) {
	m := &recoveryModel{empty: true, finishReason: "length"}
	res := collectResult{
		usage: TokenUsage{LLMCalls: 3},
		steps: []StepTrace{{
			Step:  1,
			Phase: "memory-search",
			ToolCalls: []ToolCallTrace{{
				Name:   "memory_search",
				Result: `{"results":[{"memory":"Sweden"}]}`,
			}},
		}},
	}

	got, trace := recoverMemoryQAAnswer(
		context.Background(), m, "Where did Caroline move from?", res,
	)
	if m.calls != 1 {
		t.Fatalf("model calls = %d, want 1", m.calls)
	}
	if trace == nil || trace.Succeeded || trace.FinishReason != "length" ||
		trace.Error != "recovery returned an empty answer" {
		t.Fatalf("trace = %+v", trace)
	}
	if got.usage.LLMCalls != 4 || got.usage.TotalTokens != 23 {
		t.Fatalf("usage = %+v", got.usage)
	}
	if len(got.steps) != 2 {
		t.Fatalf("steps = %+v", got.steps)
	}
	step := got.steps[1]
	if step.Phase != "answer-recovery" || step.LLMCalls != 1 ||
		step.FinishReason != "length" || step.TotalTokens != 23 ||
		step.Error != trace.Error {
		t.Fatalf("recovery step = %+v", step)
	}
}

func TestRecoverMemoryQAAnswerRequiresEvidence(t *testing.T) {
	res := collectResult{
		steps: []StepTrace{{FinishReason: "length"}},
	}
	got, trace := recoverMemoryQAAnswer(
		context.Background(), nil, "question", res,
	)
	if trace == nil || trace.Error !=
		"no memory_search evidence available for recovery" {
		t.Fatalf("trace = %+v", trace)
	}
	if got.text != "" || len(got.steps) != len(res.steps) {
		t.Fatalf("got = %+v", got)
	}
}

func TestMemoryQAUserMessageReinforcesAnswerFormat(t *testing.T) {
	msg := memoryQAUserMessage("What happened?")
	if msg.Role != model.RoleUser {
		t.Fatalf("role = %q, want user", msg.Role)
	}
	for _, want := range []string{
		"What happened?",
		"shortest complete final answer span",
		"explicit entity names",
		"every directly requested part",
		"For yes/no, output only Yes or No",
	} {
		if !strings.Contains(msg.Content, want) {
			t.Fatalf("missing %q in %q", want, msg.Content)
		}
	}
}

func TestMemoryQAGenerationConfig(t *testing.T) {
	config := memoryQAGenerationConfig()
	if config.Stream {
		t.Fatal("memory QA must use non-streaming generation")
	}
	if config.MaxTokens == nil || *config.MaxTokens != memoryQAMaxTokens {
		t.Fatalf("MaxTokens = %v, want %d", config.MaxTokens, memoryQAMaxTokens)
	}
	if config.Temperature == nil || *config.Temperature != 0 {
		t.Fatalf("Temperature = %v, want 0", config.Temperature)
	}
	if config.ReasoningEffort == nil || *config.ReasoningEffort != "low" {
		t.Fatalf("ReasoningEffort = %v, want low", config.ReasoningEffort)
	}
	if config.ThinkingEnabled == nil || *config.ThinkingEnabled {
		t.Fatalf("ThinkingEnabled = %v, want false", config.ThinkingEnabled)
	}
}

func TestCollectFinalTextAndUsage_WaitsForRunnerCompletion(t *testing.T) {
	ch := make(chan *event.Event)
	resultCh := make(chan collectResult, 1)
	errCh := make(chan error, 1)

	go func() {
		res, err := collectFinalTextAndUsage(ch)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- res
	}()

	finishReason := "length"
	final := event.NewResponseEvent(
		"invocation",
		"author",
		&model.Response{
			Done: true,
			Choices: []model.Choice{
				{
					Message:      model.NewAssistantMessage("answer"),
					FinishReason: &finishReason,
				},
			},
			Usage: &model.Usage{
				PromptTokens:     10,
				CompletionTokens: 4,
				TotalTokens:      14,
			},
		},
	)
	ch <- final

	select {
	case err := <-errCh:
		t.Fatalf("unexpected error before runner completion: %v", err)
	case res := <-resultCh:
		t.Fatalf("returned before runner completion: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}

	completion := event.NewResponseEvent(
		"invocation",
		"author",
		&model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
		},
	)
	ch <- completion
	close(ch)

	select {
	case err := <-errCh:
		t.Fatalf("unexpected error: %v", err)
	case res := <-resultCh:
		if res.text != "answer" {
			t.Fatalf("unexpected collected text: %q", res.text)
		}
		if len(res.steps) != 1 {
			t.Fatalf("steps = %d, want 1", len(res.steps))
		}
		if got := res.steps[0].FinishReason; got != finishReason {
			t.Fatalf("finish reason = %q, want %q", got, finishReason)
		}
		if got := res.steps[0].TotalTokens; got != 14 {
			t.Fatalf("step tokens = %d, want 14", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for runner completion")
	}
}

func TestCollectFinalTextAndUsage_RecordsEveryAssistantStep(t *testing.T) {
	toolFinish := "tool_calls"
	finalFinish := "stop"
	ch := make(chan *event.Event, 5)
	ch <- event.NewResponseEvent(
		"invocation",
		"author",
		&model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{
							Function: model.FunctionDefinitionParam{
								Name:      "memory_search",
								Arguments: []byte(`{"query":"first"}`),
							},
						},
						{
							Function: model.FunctionDefinitionParam{
								Name:      "memory_search",
								Arguments: []byte(`{"query":"second"}`),
							},
						},
					},
				},
				FinishReason: &toolFinish,
			}},
		},
	)
	for _, result := range []string{"first result", "second result"} {
		ch <- event.NewResponseEvent(
			"invocation",
			"author",
			&model.Response{
				Object: model.ObjectTypeToolResponse,
				Choices: []model.Choice{{
					Message: model.NewToolMessage(
						"tool-id", "memory_search", result,
					),
				}},
			},
		)
	}
	ch <- event.NewResponseEvent(
		"invocation",
		"author",
		&model.Response{
			Choices: []model.Choice{{
				Message:      model.NewAssistantMessage("answer"),
				FinishReason: &finalFinish,
			}},
		},
	)
	ch <- event.NewResponseEvent(
		"invocation",
		"author",
		&model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
		},
	)
	close(ch)

	res, err := collectFinalTextAndUsage(ch)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}
	if res.text != "answer" {
		t.Fatalf("text = %q, want answer", res.text)
	}
	if len(res.steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(res.steps))
	}
	if got := res.steps[0].FinishReason; got != toolFinish {
		t.Fatalf("tool finish reason = %q, want %q", got, toolFinish)
	}
	if got := res.steps[1].FinishReason; got != finalFinish {
		t.Fatalf("final finish reason = %q, want %q", got, finalFinish)
	}
	if got := res.steps[0].ToolCalls; len(got) != 2 ||
		got[0].Result != "first result" ||
		got[1].Result != "second result" {
		t.Fatalf("tool traces = %+v", got)
	}
}
