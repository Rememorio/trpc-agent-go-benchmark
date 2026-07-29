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
	"strconv"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go-benchmark/memory/trpc-agent-go-impl/evaluation/metrics"
	"trpc.group/trpc-go/trpc-agent-go-benchmark/memory/trpc-agent-go-impl/evaluation/scenarios"
)

type locomoReplicateTestFixture struct {
	manifestPath         string
	selectionPath        string
	mainFreshResult      string
	mainFixedResult      string
	mainFixedMarker      string
	mainStats            string
	candidateFreshResult string
	candidateStats       string
	candidateSnapshot    string
	candidateSnapshotSHA string
}

func TestCompareLoCoMoReplicates(t *testing.T) {
	fixture := newLoCoMoReplicateTestFixture(t)
	if err := compareLoCoMoReplicates(
		fixture.manifestPath, filepath.Dir(fixture.manifestPath),
	); err != nil {
		t.Fatalf("compare LoCoMo replicates: %v", err)
	}
	var comparison locomoReplicateComparison
	readLoCoMoTestJSON(
		t,
		filepath.Join(
			filepath.Dir(fixture.manifestPath),
			"locomo_replicate_comparison.json",
		),
		&comparison,
	)
	if !comparison.Gate.Passed ||
		!comparison.Gate.IntegrityPassed ||
		!comparison.Gate.QualityPassed ||
		!comparison.Gate.CostPassed {
		t.Fatalf("gate = %+v", comparison.Gate)
	}
	if len(comparison.Questions) != 2 ||
		comparison.PairedBootstrap.QuestionCount != 2 ||
		comparison.PairedBootstrap.Resamples !=
			locomoReplicateBootstrapResamples {
		t.Fatalf("comparison = %+v", comparison)
	}
	candidate := comparison.Arms[locomoReplicateRoleCandidate]
	if candidate.PrimaryMeanF1 != 0.85 ||
		candidate.FreshExtraction.IngestDurationMs != 100 ||
		candidate.ExtractionDiagnostics.ToolCalls != 1 {
		t.Fatalf("candidate = %+v", candidate)
	}
	badCases, err := os.ReadFile(filepath.Join(
		filepath.Dir(fixture.manifestPath),
		"locomo_replicate_bad_cases.tsv",
	))
	if err != nil {
		t.Fatalf("read bad cases: %v", err)
	}
	if !strings.Contains(string(badCases), "q-1\tcategory-a") ||
		!strings.Contains(string(badCases), "q-2\tcategory-b") {
		t.Fatalf("bad cases = %s", badCases)
	}
}

func TestCompareLoCoMoReplicatesRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*testing.T, locomoReplicateTestFixture)
		wantError string
	}{
		{
			name: "fixed memory ingests",
			mutate: func(t *testing.T, fixture locomoReplicateTestFixture) {
				result := readLoCoMoTestResult(t, fixture.mainFixedResult)
				result.Summary.IngestDurationMs = 1
				result.SampleResults[0].IngestDurationMs = 1
				writeLoCoMoTestJSON(t, fixture.mainFixedResult, result)
			},
			wantError: "fixed-memory replicate performed ingestion",
		},
		{
			name: "duration rollup drifts",
			mutate: func(t *testing.T, fixture locomoReplicateTestFixture) {
				result := readLoCoMoTestResult(t, fixture.mainFreshResult)
				result.Summary.QADurationMs++
				writeLoCoMoTestJSON(t, fixture.mainFreshResult, result)
			},
			wantError: "phase duration rollup mismatch",
		},
		{
			name: "usage rollup drifts",
			mutate: func(t *testing.T, fixture locomoReplicateTestFixture) {
				result := readLoCoMoTestResult(t, fixture.mainFreshResult)
				result.Summary.TotalTokens++
				writeLoCoMoTestJSON(t, fixture.mainFreshResult, result)
			},
			wantError: "summary total usage rollup mismatch",
		},
		{
			name: "module manifest changes",
			mutate: func(t *testing.T, fixture locomoReplicateTestFixture) {
				result := readLoCoMoTestResult(t, fixture.mainFixedResult)
				result.Metadata.Build.ModuleManifestSHA256 =
					strings.Repeat("c", 64)
				writeLoCoMoTestJSON(t, fixture.mainFixedResult, result)
			},
			wantError: "build manifest changed",
		},
		{
			name: "protocol metadata changes",
			mutate: func(t *testing.T, fixture locomoReplicateTestFixture) {
				result := readLoCoMoTestResult(t, fixture.mainFreshResult)
				result.Metadata.QAPromptVersion = "changed"
				writeLoCoMoTestJSON(t, fixture.mainFreshResult, result)
			},
			wantError: "protocol metadata",
		},
		{
			name: "module replacement changes",
			mutate: func(t *testing.T, fixture locomoReplicateTestFixture) {
				result := readLoCoMoTestResult(t, fixture.mainFreshResult)
				module := result.Metadata.Build.Modules[lmeAgentModulePath]
				module.ReplacementVersion = "changed"
				result.Metadata.Build.Modules[lmeAgentModulePath] = module
				writeLoCoMoTestJSON(t, fixture.mainFreshResult, result)
			},
			wantError: "result module",
		},
		{
			name: "extraction configuration changes",
			mutate: func(t *testing.T, fixture locomoReplicateTestFixture) {
				result := readLoCoMoTestResult(t, fixture.mainFreshResult)
				result.Metadata.PGVectorExtraction.UpdatePolicy =
					pgvectorUpdatePolicyAddOnly
				writeLoCoMoTestJSON(t, fixture.mainFreshResult, result)
			},
			wantError: "extraction configuration",
		},
		{
			name: "summary is incomplete",
			mutate: func(t *testing.T, fixture locomoReplicateTestFixture) {
				result := readLoCoMoTestResult(t, fixture.mainFreshResult)
				result.Summary.TotalSamples = 0
				writeLoCoMoTestJSON(t, fixture.mainFreshResult, result)
			},
			wantError: "summary is incomplete",
		},
		{
			name: "reuse flag changes",
			mutate: func(t *testing.T, fixture locomoReplicateTestFixture) {
				result := readLoCoMoTestResult(t, fixture.mainFreshResult)
				result.Metadata.ReuseMemories = true
				writeLoCoMoTestJSON(t, fixture.mainFreshResult, result)
			},
			wantError: "reuse_memories",
		},
		{
			name: "extraction call fails",
			mutate: func(t *testing.T, fixture locomoReplicateTestFixture) {
				result := readLoCoMoTestResult(t, fixture.candidateFreshResult)
				result.SampleResults[0].ExtractionCalls[0].Error =
					"provider unavailable"
				writeLoCoMoTestJSON(
					t, fixture.candidateFreshResult, result,
				)
			},
			wantError: "extraction call error",
		},
		{
			name: "extraction operation is unregistered",
			mutate: func(t *testing.T, fixture locomoReplicateTestFixture) {
				result := readLoCoMoTestResult(t, fixture.candidateFreshResult)
				result.SampleResults[0].ExtractionCalls[0].
					ToolCalls[0].Name = "memory_delete"
				writeLoCoMoTestJSON(
					t, fixture.candidateFreshResult, result,
				)
			},
			wantError: "used unregistered operation",
		},
		{
			name: "QA step fails",
			mutate: func(t *testing.T, fixture locomoReplicateTestFixture) {
				result := readLoCoMoTestResult(t, fixture.mainFreshResult)
				result.SampleResults[0].QAResults[0].Steps[0].Error =
					"provider unavailable"
				writeLoCoMoTestJSON(t, fixture.mainFreshResult, result)
			},
			wantError: "QA step error",
		},
		{
			name: "QA category changes",
			mutate: func(t *testing.T, fixture locomoReplicateTestFixture) {
				result := readLoCoMoTestResult(t, fixture.mainFreshResult)
				result.SampleResults[0].QAResults[0].Category = "unknown"
				writeLoCoMoTestJSON(t, fixture.mainFreshResult, result)
			},
			wantError: "unexpected category",
		},
		{
			name: "QA search protocol changes",
			mutate: func(t *testing.T, fixture locomoReplicateTestFixture) {
				result := readLoCoMoTestResult(t, fixture.mainFreshResult)
				result.SampleResults[0].QAResults[0].SearchCalls = 1
				writeLoCoMoTestJSON(t, fixture.mainFreshResult, result)
			},
			wantError: "incomplete QA diagnostics",
		},
		{
			name: "sample identity changes",
			mutate: func(t *testing.T, fixture locomoReplicateTestFixture) {
				result := readLoCoMoTestResult(t, fixture.mainFreshResult)
				result.SampleResults[0].SampleID = "unexpected"
				writeLoCoMoTestJSON(t, fixture.mainFreshResult, result)
			},
			wantError: "unexpected or duplicate sample",
		},
		{
			name: "build provenance is incomplete",
			mutate: func(t *testing.T, fixture locomoReplicateTestFixture) {
				result := readLoCoMoTestResult(t, fixture.mainFreshResult)
				result.Metadata.Build.ModuleManifestSHA256 = ""
				writeLoCoMoTestJSON(t, fixture.mainFreshResult, result)
			},
			wantError: "build provenance",
		},
		{
			name: "sample usage rollup drifts",
			mutate: func(t *testing.T, fixture locomoReplicateTestFixture) {
				result := readLoCoMoTestResult(t, fixture.mainFreshResult)
				result.SampleResults[0].QATokenUsage.TotalTokens++
				writeLoCoMoTestJSON(t, fixture.mainFreshResult, result)
			},
			wantError: "sample QA token usage rollup mismatch",
		},
		{
			name: "snapshot marker is false",
			mutate: func(t *testing.T, fixture locomoReplicateTestFixture) {
				writeLoCoMoTestJSON(t, fixture.mainFixedMarker, false)
			},
			wantError: "snapshot changed",
		},
		{
			name: "snapshot digest is invalid",
			mutate: func(t *testing.T, fixture locomoReplicateTestFixture) {
				if err := os.WriteFile(
					fixture.candidateSnapshotSHA, []byte("invalid\n"), 0o600,
				); err != nil {
					t.Fatalf("change snapshot digest: %v", err)
				}
			},
			wantError: "invalid SHA-256 file",
		},
		{
			name: "table stats are invalid",
			mutate: func(t *testing.T, fixture locomoReplicateTestFixture) {
				writeLoCoMoTestJSON(
					t,
					fixture.mainStats,
					locomoReplicateTableStats{},
				)
			},
			wantError: "invalid LoCoMo table stats",
		},
		{
			name: "selection duplicates a question",
			mutate: func(t *testing.T, fixture locomoReplicateTestFixture) {
				writeLoCoMoTestJSON(
					t,
					fixture.selectionPath,
					locomoReplicateSelection{
						Questions: []locomoReplicateSelectedQuestion{
							{QuestionID: "q-1"},
							{QuestionID: "q-1"},
						},
					},
				)
			},
			wantError: "duplicate question",
		},
		{
			name: "snapshot changes",
			mutate: func(t *testing.T, fixture locomoReplicateTestFixture) {
				if err := os.WriteFile(
					fixture.candidateSnapshot, []byte("changed\n"), 0o600,
				); err != nil {
					t.Fatalf("change snapshot: %v", err)
				}
			},
			wantError: "memory snapshot digest mismatch",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLoCoMoReplicateTestFixture(t)
			test.mutate(t, fixture)
			err := compareLoCoMoReplicates(
				fixture.manifestPath,
				filepath.Dir(fixture.manifestPath),
			)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestCompareLoCoMoReplicatesReportsGateFailures(t *testing.T) {
	fixture := newLoCoMoReplicateTestFixture(t)
	var stats locomoReplicateTableStats
	readLoCoMoTestJSON(t, fixture.candidateStats, &stats)
	stats.TotalRecords = 31
	stats.ActiveRecords = 31
	writeLoCoMoTestJSON(t, fixture.candidateStats, stats)

	result := readLoCoMoTestResult(t, fixture.candidateFreshResult)
	for _, sample := range result.SampleResults {
		for _, qa := range sample.QAResults {
			if qa.Category == "category-a" {
				qa.Metrics.F1 = 0.1
			}
		}
	}
	writeLoCoMoTestJSON(t, fixture.candidateFreshResult, result)
	for index := 2; index <= 3; index++ {
		path := filepath.Join(
			filepath.Dir(fixture.manifestPath),
			"candidate", "replicate-"+strconv.Itoa(index)+".json",
		)
		fixed := readLoCoMoTestResult(t, path)
		for _, sample := range fixed.SampleResults {
			for _, qa := range sample.QAResults {
				if qa.Category == "category-a" {
					qa.Metrics.F1 = 0.1
				}
			}
		}
		writeLoCoMoTestJSON(t, path, fixed)
	}
	if err := compareLoCoMoReplicates(
		fixture.manifestPath, filepath.Dir(fixture.manifestPath),
	); err != nil {
		t.Fatalf("compare LoCoMo replicates: %v", err)
	}
	var comparison locomoReplicateComparison
	readLoCoMoTestJSON(
		t,
		filepath.Join(
			filepath.Dir(fixture.manifestPath),
			"locomo_replicate_comparison.json",
		),
		&comparison,
	)
	if comparison.Gate.Passed ||
		comparison.Gate.QualityPassed ||
		comparison.Gate.CostPassed {
		t.Fatalf("gate = %+v", comparison.Gate)
	}
}

func TestValidateLoCoMoReplicateManifest(t *testing.T) {
	fixture := newLoCoMoReplicateTestFixture(t)
	manifest, err := decodeLoCoMoReplicateManifest(fixture.manifestPath)
	if err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	tests := []struct {
		name      string
		mutate    func(*locomoReplicateManifest)
		wantError string
	}{
		{
			name: "schema",
			mutate: func(value *locomoReplicateManifest) {
				value.SchemaVersion++
			},
			wantError: "schema version",
		},
		{
			name: "empty experiment",
			mutate: func(value *locomoReplicateManifest) {
				value.Experiment = ""
			},
			wantError: "experiment is empty",
		},
		{
			name: "empty selection",
			mutate: func(value *locomoReplicateManifest) {
				value.Selection = ""
			},
			wantError: "selection is empty",
		},
		{
			name: "incomplete protocol",
			mutate: func(value *locomoReplicateManifest) {
				value.Protocol.VectorTopK = 0
			},
			wantError: "protocol is incomplete",
		},
		{
			name: "wrong arm count",
			mutate: func(value *locomoReplicateManifest) {
				value.Arms = value.Arms[:1]
			},
			wantError: "arm count",
		},
		{
			name: "duplicate role",
			mutate: func(value *locomoReplicateManifest) {
				value.Arms[1].Role = value.Arms[0].Role
			},
			wantError: "duplicate LoCoMo replicate role",
		},
		{
			name: "duplicate arm name",
			mutate: func(value *locomoReplicateManifest) {
				value.Arms[1].Name = value.Arms[0].Name
			},
			wantError: "duplicate LoCoMo replicate arm",
		},
		{
			name: "arm is incomplete",
			mutate: func(value *locomoReplicateManifest) {
				value.Arms[0].MemorySnapshot = ""
			},
			wantError: "arm \"main-arm\" is incomplete",
		},
		{
			name: "wrong replicate count",
			mutate: func(value *locomoReplicateManifest) {
				value.Arms[0].Replicates =
					value.Arms[0].Replicates[:2]
			},
			wantError: "count = 2",
		},
		{
			name: "first replicate is fixed",
			mutate: func(value *locomoReplicateManifest) {
				value.Arms[0].Replicates[0].Kind =
					locomoReplicateKindFixedMemory
			},
			wantError: "starts without a fresh run",
		},
		{
			name: "duplicate replicate name",
			mutate: func(value *locomoReplicateManifest) {
				value.Arms[0].Replicates[1].Name =
					value.Arms[0].Replicates[0].Name
			},
			wantError: "duplicate replicate",
		},
		{
			name: "missing fixed marker",
			mutate: func(value *locomoReplicateManifest) {
				value.Arms[0].Replicates[1].SnapshotUnchanged = ""
			},
			wantError: "must be fixed-memory",
		},
		{
			name: "invalid gate",
			mutate: func(value *locomoReplicateManifest) {
				value.Gate.IngestDurationRatioMaximum = 0
			},
			wantError: "gate is invalid",
		},
		{
			name: "duplicate category",
			mutate: func(value *locomoReplicateManifest) {
				value.Protocol.ExpectedCategories[1] =
					value.Protocol.ExpectedCategories[0]
			},
			wantError: "duplicate values",
		},
		{
			name: "fresh replicate has marker",
			mutate: func(value *locomoReplicateManifest) {
				value.Arms[0].Replicates[0].SnapshotUnchanged = "marker.json"
			},
			wantError: "fresh LoCoMo replicate",
		},
		{
			name: "duplicate allowed operation",
			mutate: func(value *locomoReplicateManifest) {
				value.Arms[0].AllowedExtractionOperations =
					[]string{"memory_add", "memory_add"}
			},
			wantError: "duplicate allowed operations",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := manifest
			value.Protocol.ExpectedCategories = append(
				[]string(nil), manifest.Protocol.ExpectedCategories...,
			)
			value.Arms = append(
				[]locomoReplicateArmSpec(nil), manifest.Arms...,
			)
			for index := range value.Arms {
				value.Arms[index].Replicates = append(
					[]locomoReplicateInputSpec(nil),
					manifest.Arms[index].Replicates...,
				)
			}
			test.mutate(&value)
			err := validateLoCoMoReplicateManifest(value)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestDecodeLoCoMoReplicateManifestRejectsExtraJSON(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		name string
		data string
		want string
	}{
		{
			name: "unknown field",
			data: `{"schema_version":1,"unknown":true}`,
			want: "unknown field",
		},
		{
			name: "trailing value",
			data: `{"schema_version":1} {}`,
			want: "trailing JSON value",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(root, test.name+".json")
			if err := os.WriteFile(path, []byte(test.data), 0o600); err != nil {
				t.Fatalf("write manifest: %v", err)
			}
			_, err := decodeLoCoMoReplicateManifest(path)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestBootstrapLoCoMoDifferences(t *testing.T) {
	empty := bootstrapLoCoMoDifferences(nil)
	if empty.QuestionCount != 0 || empty.Lower != 0 || empty.Upper != 0 {
		t.Fatalf("empty bootstrap = %+v", empty)
	}
	result := bootstrapLoCoMoDifferences([]float64{0.1, 0.2, -0.1})
	repeated := bootstrapLoCoMoDifferences([]float64{0.1, 0.2, -0.1})
	if result != repeated ||
		result.Lower > result.PointEstimate ||
		result.Upper < result.PointEstimate {
		t.Fatalf("bootstrap = %+v, repeated = %+v", result, repeated)
	}
}

func TestLoCoMoReplicateHelpers(t *testing.T) {
	if got := formatLoCoMoReplicateBadCases(nil); got != "" {
		t.Fatalf("nil bad cases = %q", got)
	}
	absolute := filepath.Join(t.TempDir(), "result.json")
	if got := resolveLoCoMoReplicatePath("/ignored", absolute); got != absolute {
		t.Fatalf("absolute path = %q, want %q", got, absolute)
	}
	if validSHA256(strings.Repeat("A", 64)) ||
		validSHA256(strings.Repeat("a", 63)) {
		t.Fatal("invalid SHA-256 accepted")
	}
	if err := writeLoCoMoReplicateJSON(
		filepath.Join(t.TempDir(), "missing", "result.json"),
		map[string]bool{"ok": true},
	); err == nil {
		t.Fatal("writeLoCoMoReplicateJSON succeeded with a missing parent")
	}
	if _, _, err := loadLoCoMoReplicateSelection(
		filepath.Join(t.TempDir(), "missing.json"),
	); err == nil {
		t.Fatal("missing selection was accepted")
	}
	malformed := filepath.Join(t.TempDir(), "malformed.json")
	if err := os.WriteFile(malformed, []byte("{"), 0o600); err != nil {
		t.Fatalf("write malformed JSON: %v", err)
	}
	var malformedTarget map[string]any
	if err := readLoCoMoReplicateJSON(
		malformed, &malformedTarget,
	); err == nil {
		t.Fatal("malformed JSON was accepted")
	}
	check := locomoRatioGateCheck("zero-main", 1, 0, 2)
	if check.Passed || !strings.Contains(check.Actual, "undefined") {
		t.Fatalf("zero denominator check = %+v", check)
	}
	diagnostics := summarizeLoCoMoExtractionDiagnostics(
		[]*scenarios.SampleResult{
			nil,
			{
				ExtractionCalls: []scenarios.ExtractionCallTrace{
					{Error: "failed"},
					{
						SourceMessages: []scenarios.ExtractionMessageTrace{{}},
						ToolCalls: []scenarios.ToolCallTrace{{
							Name: "memory_add",
						}},
					},
				},
			},
		},
	)
	if diagnostics.ModelCalls != 2 ||
		diagnostics.ModelCallErrors != 1 ||
		diagnostics.CallsWithoutTool != 1 ||
		diagnostics.ToolCalls != 1 ||
		diagnostics.SourceMessages != 1 {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
}

func newLoCoMoReplicateTestFixture(
	t *testing.T,
) locomoReplicateTestFixture {
	t.Helper()
	root := t.TempDir()
	selectionPath := filepath.Join(root, "selection.json")
	writeLoCoMoTestJSON(t, selectionPath, locomoReplicateSelection{
		Questions: []locomoReplicateSelectedQuestion{
			{QuestionID: "q-1"},
			{QuestionID: "q-2"},
		},
	})
	protocol := locomoReplicateProtocol{
		ExpectedSamples:    1,
		ExpectedQuestions:  2,
		ExpectedReplicates: 3,
		ExpectedSampleIDs:  []string{"sample-1"},
		ExpectedCategories: []string{"category-a", "category-b"},
		BenchmarkRevision:  "benchmark-revision",
		ReplayProtocol:     locomoAutoReplayProtocol,
		RoleMapping:        locomoRoleMapping,
		Model:              "glm52",
		ModelVariant:       "glm",
		EvalModel:          "glm52",
		QAPromptVersion:    scenarios.MemoryQAPromptVersion,
		QASearchStrategy:   scenarios.MemoryQASearchStrategy,
		QASearchPasses:     2,
		VectorTopK:         30,
	}
	main := writeLoCoMoTestArm(
		t, root, protocol, "main-arm", locomoReplicateRoleMain,
		"upstream", "main-version", "reconcile", false, "", 0.8,
	)
	candidate := writeLoCoMoTestArm(
		t, root, protocol, "candidate-arm",
		locomoReplicateRoleCandidate, "candidate", "candidate-version",
		"history-preserving", true, "assistant-result-preserving", 0.85,
	)
	manifest := locomoReplicateManifest{
		SchemaVersion: locomoReplicateManifestSchemaVersion,
		Experiment:    "locomo-test",
		Selection:     filepath.Base(selectionPath),
		Protocol:      protocol,
		Arms:          []locomoReplicateArmSpec{main, candidate},
		Gate: locomoReplicateGateConfig{
			OverallNoninferiorityMargin:     0.03,
			PerCategoryNoninferiorityMargin: 0.15,
			MemoryCountRatioMaximum:         2,
			ExtractionTokenRatioMaximum:     2,
			EmbeddingTokenRatioMaximum:      2,
			IngestDurationRatioMaximum:      2,
		},
	}
	manifestPath := filepath.Join(root, "manifest.json")
	writeLoCoMoTestJSON(t, manifestPath, manifest)
	return locomoReplicateTestFixture{
		manifestPath:         manifestPath,
		selectionPath:        selectionPath,
		mainFreshResult:      filepath.Join(root, main.Replicates[0].Results),
		mainFixedResult:      filepath.Join(root, main.Replicates[1].Results),
		mainFixedMarker:      filepath.Join(root, main.Replicates[1].SnapshotUnchanged),
		mainStats:            filepath.Join(root, main.TableStats),
		candidateFreshResult: filepath.Join(root, candidate.Replicates[0].Results),
		candidateStats:       filepath.Join(root, candidate.TableStats),
		candidateSnapshot:    filepath.Join(root, candidate.MemorySnapshot),
		candidateSnapshotSHA: filepath.Join(root, candidate.MemorySnapshotSHA256),
	}
}

func writeLoCoMoTestArm(
	t *testing.T,
	root string,
	protocol locomoReplicateProtocol,
	name string,
	role string,
	buildProfile string,
	moduleVersion string,
	updatePolicy string,
	assistantExtraction bool,
	assistantPolicy string,
	f1 float64,
) locomoReplicateArmSpec {
	t.Helper()
	armDir := filepath.Join(root, role)
	if err := os.MkdirAll(armDir, 0o755); err != nil {
		t.Fatalf("create arm dir: %v", err)
	}
	snapshotPath := filepath.Join(armDir, "memory-snapshot.jsonl")
	if err := os.WriteFile(
		snapshotPath, []byte(`{"memory_id":"1"}`+"\n"), 0o600,
	); err != nil {
		t.Fatalf("write memory snapshot: %v", err)
	}
	snapshotSHA, err := sha256File(snapshotPath)
	if err != nil {
		t.Fatalf("hash memory snapshot: %v", err)
	}
	snapshotSHAPath := filepath.Join(armDir, "memory-snapshot.sha256")
	if err := os.WriteFile(
		snapshotSHAPath,
		[]byte(snapshotSHA+"  memory-snapshot.jsonl\n"),
		0o600,
	); err != nil {
		t.Fatalf("write memory snapshot digest: %v", err)
	}
	statsPath := filepath.Join(armDir, "table-stats.json")
	writeLoCoMoTestJSON(t, statsPath, locomoReplicateTableStats{
		TotalRecords:  10,
		ActiveRecords: 10,
	})
	spec := locomoReplicateArmSpec{
		Name:                        name,
		Role:                        role,
		BuildProfile:                buildProfile,
		ModuleReplacementVersion:    moduleVersion,
		UpdatePolicy:                updatePolicy,
		AssistantResultExtraction:   assistantExtraction,
		AssistantResultUpdatePolicy: assistantPolicy,
		TableSuffix:                 "_" + role,
		TableStats:                  relativeLoCoMoTestPath(t, root, statsPath),
		MemorySnapshot:              relativeLoCoMoTestPath(t, root, snapshotPath),
		MemorySnapshotSHA256:        relativeLoCoMoTestPath(t, root, snapshotSHAPath),
		AllowedExtractionOperations: []string{"memory_add"},
	}
	for index := 1; index <= 3; index++ {
		kind := locomoReplicateKindFixedMemory
		if index == 1 {
			kind = locomoReplicateKindFresh
		}
		resultPath := filepath.Join(
			armDir, "replicate-"+strconv.Itoa(index)+".json",
		)
		writeLoCoMoTestJSON(
			t,
			resultPath,
			newLoCoMoTestResult(
				protocol, spec, kind, moduleVersion, f1,
			),
		)
		input := locomoReplicateInputSpec{
			Name:    "answer-" + strconv.Itoa(index),
			Kind:    kind,
			Results: relativeLoCoMoTestPath(t, root, resultPath),
		}
		if index > 1 {
			markerPath := filepath.Join(
				armDir,
				"replicate-"+strconv.Itoa(index)+"-snapshot-unchanged.json",
			)
			writeLoCoMoTestJSON(t, markerPath, true)
			input.SnapshotUnchanged = relativeLoCoMoTestPath(
				t, root, markerPath,
			)
		}
		spec.Replicates = append(spec.Replicates, input)
	}
	return spec
}

func newLoCoMoTestResult(
	protocol locomoReplicateProtocol,
	arm locomoReplicateArmSpec,
	kind string,
	moduleVersion string,
	f1 float64,
) EvaluationResult {
	reuse := kind == locomoReplicateKindFixedMemory
	qaUsage := scenarios.TokenUsage{
		PromptTokens: 100,
		TotalTokens:  120,
		LLMCalls:     2,
	}
	qaEmbeddings := scenarios.EmbeddingUsage{
		PromptTokens: 20,
		TotalTokens:  20,
		Calls:        2,
	}
	var extractionUsage *scenarios.TokenUsage
	var extractionEmbeddings *scenarios.EmbeddingUsage
	var extractionCalls []scenarios.ExtractionCallTrace
	var ingestDuration int64
	if !reuse {
		value := scenarios.TokenUsage{
			PromptTokens: 50,
			TotalTokens:  60,
			LLMCalls:     1,
		}
		extractionUsage = &value
		embeddingValue := scenarios.EmbeddingUsage{
			PromptTokens: 10,
			TotalTokens:  10,
			Calls:        1,
		}
		extractionEmbeddings = &embeddingValue
		extractionCalls = []scenarios.ExtractionCallTrace{{
			Step:           1,
			PromptTokens:   50,
			TotalTokens:    60,
			SourceMessages: []scenarios.ExtractionMessageTrace{{}},
			ToolCalls: []scenarios.ToolCallTrace{{
				Name: "memory_add",
			}},
		}}
		ingestDuration = 100
	}
	totalUsage := qaUsage
	if extractionUsage != nil {
		totalUsage.Add(*extractionUsage)
	}
	totalEmbeddings := qaEmbeddings
	if extractionEmbeddings != nil {
		totalEmbeddings.Add(*extractionEmbeddings)
	}
	sample := &scenarios.SampleResult{
		SampleID:                 "sample-1",
		TotalTimeMs:              ingestDuration + 200,
		IngestDurationMs:         ingestDuration,
		QADurationMs:             200,
		TokenUsage:               &totalUsage,
		ExtractionTokenUsage:     extractionUsage,
		QATokenUsage:             &qaUsage,
		EmbeddingUsage:           &totalEmbeddings,
		ExtractionEmbeddingUsage: extractionEmbeddings,
		QAEmbeddingUsage:         &qaEmbeddings,
		ExtractionCalls:          extractionCalls,
		QAResults: []*scenarios.QAResult{
			newLoCoMoTestQA("q-1", "category-a", f1),
			newLoCoMoTestQA("q-2", "category-b", f1),
		},
	}
	return EvaluationResult{
		Metadata: &EvalMetadata{
			Framework:        "trpc-agent-go",
			Model:            protocol.Model,
			ModelVariant:     protocol.ModelVariant,
			EvalModel:        protocol.EvalModel,
			Scenario:         string(scenarios.ScenarioAuto),
			MemoryBackend:    "pgvector",
			QASearchPasses:   protocol.QASearchPasses,
			QAPromptVersion:  protocol.QAPromptVersion,
			QASearchStrategy: protocol.QASearchStrategy,
			VectorTopK:       protocol.VectorTopK,
			ReplayProtocol:   protocol.ReplayProtocol,
			RoleMapping:      protocol.RoleMapping,
			ReuseMemories:    reuse,
			TableSuffix:      arm.TableSuffix,
			PGVectorExtraction: &pgvectorExtractionConfig{
				UpdatePolicy:                pgvectorUpdatePolicy(arm.UpdatePolicy),
				AssistantResultExtraction:   arm.AssistantResultExtraction,
				AssistantResultUpdatePolicy: arm.AssistantResultUpdatePolicy,
			},
			Build: lmeBuildProvenance{
				Revision:             protocol.BenchmarkRevision,
				BuildProfile:         arm.BuildProfile,
				ModuleManifestSHA256: strings.Repeat("a", 64),
				ModuleSumSHA256:      strings.Repeat("b", 64),
				Modules: map[string]lmeModuleProvenance{
					lmeAgentModulePath: {
						ReplacementVersion: moduleVersion,
					},
					lmePGVectorModulePath: {
						ReplacementVersion: moduleVersion,
					},
				},
			},
		},
		Summary: &EvalSummary{
			TotalSamples:             1,
			TotalQuestions:           2,
			OverallF1:                f1,
			OverallBLEU:              f1,
			TotalTimeMs:              sample.TotalTimeMs,
			IngestDurationMs:         ingestDuration,
			QADurationMs:             200,
			TotalPromptTokens:        totalUsage.PromptTokens,
			TotalCompletionTokens:    totalUsage.CompletionTokens,
			TotalTokens:              totalUsage.TotalTokens,
			TotalCachedTokens:        totalUsage.CachedPromptTokens(),
			TotalLLMCalls:            totalUsage.LLMCalls,
			TokenUsage:               &totalUsage,
			ExtractionTokenUsage:     extractionUsage,
			QATokenUsage:             &qaUsage,
			EmbeddingUsage:           &totalEmbeddings,
			ExtractionEmbeddingUsage: extractionEmbeddings,
			QAEmbeddingUsage:         &qaEmbeddings,
		},
		SampleResults: []*scenarios.SampleResult{sample},
	}
}

func newLoCoMoTestQA(
	questionID string,
	category string,
	f1 float64,
) *scenarios.QAResult {
	usage := scenarios.TokenUsage{
		PromptTokens: 50,
		TotalTokens:  60,
		LLMCalls:     1,
	}
	return &scenarios.QAResult{
		QuestionID:  questionID,
		Category:    category,
		Metrics:     metrics.QAMetrics{F1: f1, BLEU: f1},
		TokenUsage:  &usage,
		SearchCalls: 2,
		Steps:       []scenarios.StepTrace{{Step: 1}},
	}
}

func readLoCoMoTestResult(
	t *testing.T,
	path string,
) EvaluationResult {
	t.Helper()
	var result EvaluationResult
	readLoCoMoTestJSON(t, path, &result)
	return result
}

func readLoCoMoTestJSON(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func writeLoCoMoTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func relativeLoCoMoTestPath(
	t *testing.T,
	root string,
	path string,
) string {
	t.Helper()
	relative, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatalf("relative path: %v", err)
	}
	return relative
}
