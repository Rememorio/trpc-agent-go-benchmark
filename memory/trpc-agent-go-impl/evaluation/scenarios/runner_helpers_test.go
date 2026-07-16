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
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestQAMemorySearchInstruction_SingleSearch(t *testing.T) {
	got := qaMemorySearchInstruction(1)
	if !strings.Contains(got, "exactly once") {
		t.Fatalf("missing single-search rule: %q", got)
	}
	assertGroundedQAPrompt(t, got)
}

func TestQAMemorySearchInstruction_MultiSearch(t *testing.T) {
	got := qaMemorySearchInstruction(2)
	if !strings.Contains(got, "exactly 2 separate") {
		t.Fatalf("missing multi-search rule: %q", got)
	}
	if !strings.Contains(got, fallbackAnswer) {
		t.Fatalf("missing fallback answer: %q", got)
	}
	if !strings.Contains(got, "different short query") {
		t.Fatalf("missing workflow search marker: %q", got)
	}
	assertGroundedQAPrompt(t, got)
}

func assertGroundedQAPrompt(t *testing.T, got string) {
	t.Helper()
	for _, want := range []string{
		"Topical relevance is not answer support",
		"support the exact subject",
		"Never transfer a fact",
		"Never output an empty answer",
		`output exactly "Yes" or "No"`,
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
			name: "planned batch",
			steps: []StepTrace{
				{ToolCalls: []ToolCallTrace{search, search}},
				{},
			},
			expected:  2,
			wantCalls: 2,
		},
		{
			name: "sequential calls",
			steps: []StepTrace{
				{ToolCalls: []ToolCallTrace{search}},
				{ToolCalls: []ToolCallTrace{search}},
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
