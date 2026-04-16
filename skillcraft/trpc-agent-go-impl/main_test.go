package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestConsumeEventsTracksClaimDoneAndSkillUsage(t *testing.T) {
	evtCh := make(chan *event.Event, 1)
	evtCh <- &event.Event{
		Response: &model.Response{
			Usage: &model.Usage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolCalls: []model.ToolCall{
							{
								Function: model.FunctionDefinitionParam{
									Name:      "mcp_local-claim_done",
									Arguments: []byte(`{}`),
								},
							},
							{
								Function: model.FunctionDefinitionParam{
									Name:      "skill_load",
									Arguments: []byte(`{"skill":"Perform DNA Sequence Analysis"}`),
								},
							},
						},
					},
				},
			},
		},
	}
	close(evtCh)

	stats := consumeEvents(evtCh)
	require.True(t, stats.ClaimDoneCalled)
	require.True(t, stats.SkillUsageObserved)
	require.Equal(t, []string{"skill_load"}, stats.SkillToolCalls)
	require.Equal(t, []string{"Perform DNA Sequence Analysis"}, stats.LoadedSkillNames)
}

func TestBuildComparisonUsesWarmStartSubset(t *testing.T) {
	baselineCases := []*taskRunResult{
		{
			TaskID:              "task-1",
			TotalTokens:         100,
			EndToEndTotalTokens: 100,
			Evaluation:          &officialEval{Passed: true, Score: scorePayload{Percent: 50}},
		},
		{
			TaskID:              "task-2",
			TotalTokens:         200,
			EndToEndTotalTokens: 200,
			Evaluation:          &officialEval{Passed: true, Score: scorePayload{Percent: 60}},
		},
	}
	evolutionCases := []*taskRunResult{
		{
			TaskID:              "task-1",
			TotalTokens:         150,
			ReviewerTotalTokens: 30,
			EndToEndTotalTokens: 180,
			Evaluation:          &officialEval{Passed: true, Score: scorePayload{Percent: 70}},
		},
		{
			TaskID:              "task-2",
			TotalTokens:         120,
			ReviewerTotalTokens: 30,
			EndToEndTotalTokens: 150,
			HadAvailableSkills:  true,
			SkillUsageObserved:  true,
			Evaluation:          &officialEval{Passed: true, Score: scorePayload{Percent: 80}},
		},
	}

	baseline := &modeResult{
		Mode:    modeBaseline,
		Cases:   baselineCases,
		Summary: summarizeMode(baselineCases, nil),
	}
	evolution := &modeResult{
		Mode:    modeEvolution,
		Cases:   evolutionCases,
		Summary: summarizeMode(evolutionCases, []string{"Perform DNA Sequence Analysis"}),
	}

	comp := buildComparison(baseline, evolution)
	require.NotNil(t, comp)
	require.Equal(t, 1, comp.WarmStartTaskCount)
	require.InDelta(t, 20.0, comp.WarmStartScoreDelta, 0.02)
	require.InDelta(t, -80.0, comp.WarmStartTokenDelta, 0.02)
	require.InDelta(t, -50.0, comp.WarmStartEndToEndTokenDelta, 0.02)
	require.InDelta(t, 50.0, comp.SkillUsageObservedDelta, 0.02)
}

func TestBuildInstructionPrioritizesTaskSpecOverSkills(t *testing.T) {
	task := &taskDefinition{
		TaskDoc:          "SEQ_01: ATGC...",
		NeededLocalTools: []string{"claim_done"},
	}

	prompt := buildInstruction(task, "/tmp/workspace", []string{"DNA Sequence Analysis Workflow"})

	require.Contains(t, prompt, "Read the full task specification before deciding whether a managed skill applies.")
	require.Contains(t, prompt, "Managed skills may come from smaller or earlier tasks and can be incomplete.")
	require.Contains(t, prompt, "compare it against the current task's required APIs")
	require.Contains(t, prompt, "trailing `...`")
	require.Contains(t, prompt, "Do not blindly repeat the same tool call on the same input.")
	require.Contains(t, prompt, "prefer one complete write with write_file")
	require.Contains(t, prompt, "write the final output once near the end")
	require.Contains(t, prompt, "Do not create draft files, scratch files, or auxiliary reports")
	require.Contains(t, prompt, "final saved file is valid JSON")
}

func TestBuildUserPromptPutsTaskSpecBeforeManagedSkills(t *testing.T) {
	task := &taskDefinition{
		TaskDoc: "SEQ_01: ATGC...",
	}

	prompt := buildUserPrompt(task, "/tmp/workspace", []string{"DNA Sequence Analysis Workflow"})

	require.Contains(t, prompt, "## Task Specification")
	require.Contains(t, prompt, "## Managed Skills Available")
	require.Less(t,
		strings.Index(prompt, "## Task Specification"),
		strings.Index(prompt, "## Managed Skills Available"),
	)
	require.Contains(t, prompt, "task specification overrides any skill")
}

func TestResultStatusFromEvaluation(t *testing.T) {
	require.Equal(t, "ok", resultStatusFromEvaluation(nil))
	require.Equal(t, "ok", resultStatusFromEvaluation(&officialEval{Passed: true, Status: "pass"}))
	require.Equal(t, "fail", resultStatusFromEvaluation(&officialEval{Passed: false, Status: "fail"}))
	require.Equal(t, "evaluation_failed", resultStatusFromEvaluation(&officialEval{Passed: false}))
}
