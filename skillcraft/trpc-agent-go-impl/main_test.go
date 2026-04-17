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
		TaskDoc:          "SEQ_01: ATGC...\n\nSave results to `dna_results.json`:",
		NeededLocalTools: []string{"claim_done"},
	}

	prompt := buildInstruction(task, "/tmp/workspace", []string{"DNA Sequence Analysis Workflow"})

	require.Contains(t, prompt, "Read the full task specification before deciding whether a managed skill applies.")
	require.Contains(t, prompt, "Managed skills may come from smaller or earlier tasks and can be incomplete.")
	require.Contains(t, prompt, "compare it against the current task's required APIs")
	require.Contains(t, prompt, "trailing `...`")
	require.Contains(t, prompt, "do not call the same tool with the same arguments again")
	require.Contains(t, prompt, "prefer one complete write with write_file")
	require.Contains(t, prompt, "write the final output once near the end")
	require.Contains(t, prompt, "Do not create draft files, scratch files, or auxiliary reports")
	require.Contains(t, prompt, "final saved file is valid JSON")
	require.Contains(t, prompt, "Required final deliverable: dna_results.json")
	require.Contains(t, prompt, "Save it by calling local-write_final_json")
	require.Contains(t, prompt, "{\"path\":\"dna_results.json\",\"content\":<raw JSON text>}")
	require.Contains(t, prompt, "verify that dna_results.json exists")
	require.Contains(t, prompt, "put raw JSON text")
	require.Contains(t, prompt, "do not escape every newline as \\n")
	require.Contains(t, prompt, "Never end your turn with the final JSON only inside an assistant message")
	require.Contains(t, prompt, "If a managed skill mentions a tool that is not in the tool list available for this task, skip that step")
}

func TestExtractTaskEntitiesParsesPrimaryTaskTable(t *testing.T) {
	taskDoc := `# Task: Weather Monitor (4 Cities × 4 APIs) - M2

## Cities to Analyze

| # | City | Latitude | Longitude |
|---|------|----------|----------|
| 1 | Tokyo | 35.6762 | 139.6503 |
| 2 | New York | 40.7128 | -74.006 |
| 3 | London | 51.5074 | -0.1278 |
| 4 | Sydney | -33.8688 | 151.2093 |

## Summary Requirements

| Data Type | Tool Returns | Required Output |
|-----------|-------------|-----------------|
| Hourly | 168 values | avg, max, min |`

	entities := extractTaskEntities(taskDoc)
	require.NotNil(t, entities)
	require.Equal(t, "cities", entities.Label)
	require.Equal(t, []string{"Tokyo", "New York", "London", "Sydney"}, entities.Values)
}

func TestBuildInstructionEnforcesExactTaskEntitiesOverInitialFiles(t *testing.T) {
	task := &taskDefinition{
		TaskDoc: `## Countries to Analyze

| # | Country | Code | Region |
|---|---------|------|--------|
| 1 | United States | US | North America |
| 2 | China | CHN | East Asia & Pacific |
| 3 | Japan | JPN | East Asia & Pacific |
| 4 | Germany | DEU | Europe & Central Asia |`,
		NeededLocalTools: []string{"claim_done"},
		HasInitialContent: true,
	}

	prompt := buildInstruction(task, "/tmp/workspace", nil)

	require.Contains(t, prompt, "Initial workspace files may be helper inputs")
	require.Contains(t, prompt, "requires exactly these countries: United States, China, Japan, Germany")
	require.Contains(t, prompt, "Do not add extra countries")
	require.Contains(t, prompt, "filter it down to the exact task-specified set")
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

func TestBuildUserPromptIncludesExactTaskEntities(t *testing.T) {
	task := &taskDefinition{
		TaskDoc: `## Dishes to Include

| # | Dish | Search Name | Cuisine |
|---|------|-------------|---------|
| 1 | Spaghetti Carbonara | carbonara | Italian |
| 2 | Tandoori Chicken | tandoori | Indian |
| 3 | Pad Thai | pad thai | Thai |
| 4 | Beef Bourguignon | beef bourguignon | French |
| 5 | Sushi | sushi | Japanese |`,
		HasInitialContent: true,
	}

	prompt := buildUserPrompt(task, "/tmp/workspace", nil)

	require.Contains(t, prompt, "## Exact Required Entities")
	require.Contains(t, prompt, "Dishes: Spaghetti Carbonara, Tandoori Chicken, Pad Thai, Beef Bourguignon, Sushi")
	require.Contains(t, prompt, "Do not add extra entries from initial workspace files")
	require.Contains(t, prompt, "task specification is authoritative")
}

func TestExtractRequiredOutputFile(t *testing.T) {
	taskDoc := "## Required Output\n\nSave results to `weather_report.json`:\n\n```json\n{}"

	require.Equal(t, "weather_report.json", extractRequiredOutputFile(taskDoc))
	require.Equal(t, "", extractRequiredOutputFile("no explicit output file"))
}

func TestBuildUserPromptIncludesFinalizationRules(t *testing.T) {
	task := &taskDefinition{
		TaskDoc: "## Required Output\n\nSave results to `recipe_cookbook.json`:\n\n```json\n{}",
	}

	prompt := buildUserPrompt(task, "/tmp/workspace", nil)

	require.Contains(t, prompt, "## Finalization Rules")
	require.Contains(t, prompt, "Required deliverable: `recipe_cookbook.json`")
	require.Contains(t, prompt, "Save it via `local-write_final_json`")
	require.Contains(t, prompt, "`{\"path\":\"recipe_cookbook.json\",\"content\":<raw JSON>}`")
	require.Contains(t, prompt, "fall back to `write_file` only if that tool errors")
	require.Contains(t, prompt, "must be raw JSON text")
	require.Contains(t, prompt, "Do not end your turn with the final JSON only in chat")
}

func TestBuildInstructionAddsWorkingNotesGuidanceForLargeTasks(t *testing.T) {
	task := &taskDefinition{
		TaskDoc: `## Cities to Analyze

| # | City | Latitude | Longitude |
|---|------|----------|----------|
| 1 | Tokyo | 35.6762 | 139.6503 |
| 2 | New York | 40.7128 | -74.006 |
| 3 | London | 51.5074 | -0.1278 |
| 4 | Sydney | -33.8688 | 151.2093 |
| 5 | Dubai | 25.2048 | 55.2708 |

## Summary Requirements

Output ONLY summary statistics, NOT raw data arrays!`,
		MaxTurns: 100,
	}

	prompt := buildInstruction(task, "/tmp/workspace", nil)

	require.Contains(t, prompt, "do not rely on raw tool outputs staying in context forever")
	require.Contains(t, prompt, "single compact helper JSON file such as working_notes.json")
	require.Contains(t, prompt, "read it back later")
	require.Contains(t, prompt, "Do not store raw arrays or raw tool dumps")
	require.True(t, taskNeedsWorkingNotes(task))
	require.True(t, taskNeedsLowerCompletionBudget(task))
}

func TestResultStatusFromEvaluation(t *testing.T) {
	require.Equal(t, "ok", resultStatusFromEvaluation(nil))
	require.Equal(t, "ok", resultStatusFromEvaluation(&officialEval{Passed: true, Status: "pass"}))
	require.Equal(t, "partial", resultStatusFromEvaluation(&officialEval{Passed: true, Status: "partial"}))
	require.Equal(t, "fail", resultStatusFromEvaluation(&officialEval{Passed: false, Status: "fail"}))
	require.Equal(t, "evaluation_failed", resultStatusFromEvaluation(&officialEval{Passed: false}))
}

func TestReportScalesOmittedForExplicitTasks(t *testing.T) {
	cfg := &benchmarkConfig{
		Scales:        []string{"e1", "e2", "e3"},
		ExplicitTasks: []string{"openmeteo-weather/e1"},
	}
	require.Nil(t, reportScales(cfg))

	cfg = &benchmarkConfig{
		Scales: []string{"e1", "e2", "e3"},
	}
	require.Equal(t, []string{"e1", "e2", "e3"}, reportScales(cfg))
}
