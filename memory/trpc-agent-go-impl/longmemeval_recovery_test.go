package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestApplyLongMemEvalRecovery(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.json")
	replacementPath := filepath.Join(dir, "replacement.json")
	outputDir := filepath.Join(dir, "output")

	source := recoveryTestResult("source-answer", 2)
	replacement := recoveryTestResult("replacement-answer", 1)
	replacement.Cases = replacement.Cases[:1]
	replacement.Cases[0].BackendResults = map[string]*backendResult{
		"pgvector": replacement.Cases[0].BackendResults["pgvector"],
	}
	replacement.Summary = buildLongMemEvalSummary(replacement.Cases)
	writeRecoveryTestResults(t, sourcePath, source)
	writeRecoveryTestResults(t, replacementPath, replacement)

	sourceSHA, err := sha256File(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	manifest := longMemEvalRecoveryManifest{
		SchemaVersion: longMemEvalRecoverySchemaVersion,
		Status:        "registered-before-provider-calls",
		RegisteredAt:  time.Now().UTC().Format(time.RFC3339),
		Reason:        "provider_failure",
		Source: longMemEvalRecoverySource{
			ResultsPath:   "source.json",
			ResultsSHA256: sourceSHA,
		},
		Replacements: []longMemEvalRecoveryReplacement{
			{ResultsPath: "replacement.json"},
		},
		ExpectedUnits: []longMemEvalRecoveryUnit{
			{
				QuestionIDSHA256: sha256String("question-1"),
				Backend:          "pgvector",
			},
		},
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	writeRecoveryTestJSON(t, manifestPath, manifest)

	if err := applyLongMemEvalRecovery(manifestPath, outputDir); err != nil {
		t.Fatal(err)
	}
	got, err := loadLongMemEvalResults(
		filepath.Join(outputDir, "results.json"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cases[0].BackendResults["pgvector"].Answer !=
		"replacement-answer" {
		t.Fatalf(
			"recovered answer = %q",
			got.Cases[0].BackendResults["pgvector"].Answer,
		)
	}
	if got.Cases[0].BackendResults["mem0"].Answer != "source-answer" ||
		got.Cases[1].BackendResults["pgvector"].Answer != "source-answer" {
		t.Fatal("recovery changed an unregistered unit")
	}
	if got.Summary.BackendSummaries["pgvector"].Cases != 2 ||
		got.Summary.BackendSummaries["mem0"].Cases != 2 {
		t.Fatalf("unexpected rebuilt summary: %#v", got.Summary)
	}
	recovery, ok := got.Metadata["recovery"].(map[string]any)
	if !ok || recovery["replacement_count"] != float64(1) ||
		recovery["quality_outcomes_inspected"] != false {
		t.Fatalf("unexpected recovery metadata: %#v", recovery)
	}
}

func TestApplyLongMemEvalRecoveryRejectsInvalidInput(t *testing.T) {
	testCases := []struct {
		name    string
		mutate  func(*longMemEvalRecoveryManifest, *runResult)
		wantErr string
	}{
		{
			name: "source hash",
			mutate: func(manifest *longMemEvalRecoveryManifest, _ *runResult) {
				manifest.Source.ResultsSHA256 = strings.Repeat("0", 64)
			},
			wantErr: "source results hash mismatch",
		},
		{
			name: "quality inspected",
			mutate: func(manifest *longMemEvalRecoveryManifest, _ *runResult) {
				manifest.QualityOutcomesInspected = true
			},
			wantErr: "inspected quality outcomes",
		},
		{
			name: "unregistered backend",
			mutate: func(_ *longMemEvalRecoveryManifest, replacement *runResult) {
				replacement.Cases[0].BackendResults["mem0"] =
					&backendResult{Backend: "mem0"}
			},
			wantErr: "unregistered unit",
		},
		{
			name: "case mismatch",
			mutate: func(_ *longMemEvalRecoveryManifest, replacement *runResult) {
				replacement.Cases[0].QuestionType = "different"
			},
			wantErr: "case metadata does not match",
		},
		{
			name: "runtime error",
			mutate: func(_ *longMemEvalRecoveryManifest, replacement *runResult) {
				replacement.Cases[0].BackendResults["pgvector"].Error =
					"provider failed"
			},
			wantErr: "contains a runtime error",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			sourcePath := filepath.Join(dir, "source.json")
			replacementPath := filepath.Join(dir, "replacement.json")
			source := recoveryTestResult("source", 1)
			replacement := recoveryTestResult("replacement", 1)
			replacement.Cases[0].BackendResults = map[string]*backendResult{
				"pgvector": replacement.Cases[0].BackendResults["pgvector"],
			}
			writeRecoveryTestResults(t, sourcePath, source)
			sourceSHA, err := sha256File(sourcePath)
			if err != nil {
				t.Fatal(err)
			}
			manifest := longMemEvalRecoveryManifest{
				SchemaVersion: longMemEvalRecoverySchemaVersion,
				Status:        "registered-before-provider-calls",
				RegisteredAt:  time.Now().UTC().Format(time.RFC3339),
				Reason:        "provider_failure",
				Source: longMemEvalRecoverySource{
					ResultsPath:   "source.json",
					ResultsSHA256: sourceSHA,
				},
				Replacements: []longMemEvalRecoveryReplacement{
					{ResultsPath: "replacement.json"},
				},
				ExpectedUnits: []longMemEvalRecoveryUnit{
					{
						QuestionIDSHA256: sha256String("question-1"),
						Backend:          "pgvector",
					},
				},
			}
			tc.mutate(&manifest, replacement)
			replacement.Summary = buildLongMemEvalSummary(replacement.Cases)
			writeRecoveryTestResults(t, replacementPath, replacement)
			manifestPath := filepath.Join(dir, "manifest.json")
			writeRecoveryTestJSON(t, manifestPath, manifest)

			err = applyLongMemEvalRecovery(
				manifestPath, filepath.Join(dir, "output"),
			)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func recoveryTestResult(answer string, cases int) *runResult {
	result := &runResult{
		Metadata: map[string]any{
			"dataset_sha256":  "dataset",
			"protocol_sha256": "protocol",
			"build": map[string]any{
				"benchmark_revision": "revision",
			},
		},
		Cases: make([]*caseResult, 0, cases),
	}
	for i := 1; i <= cases; i++ {
		result.Cases = append(result.Cases, &caseResult{
			QuestionID:       "question-" + string(rune('0'+i)),
			QuestionType:     "single-session-user",
			Question:         "question",
			QuestionDate:     "2026-01-01",
			Answer:           "reference",
			AnswerSessionIDs: []string{"session"},
			NumSessions:      1,
			BackendResults: map[string]*backendResult{
				"pgvector": {
					Backend:       "pgvector",
					Answer:        answer,
					IngestedPairs: 1,
				},
				"mem0": {
					Backend:       "mem0",
					Answer:        answer,
					IngestedPairs: 1,
				},
			},
		})
	}
	result.Summary = buildLongMemEvalSummary(result.Cases)
	return result
}

func writeRecoveryTestResults(t *testing.T, path string, result *runResult) {
	t.Helper()
	if err := writeLongMemEvalResults(path, result); err != nil {
		t.Fatal(err)
	}
}

func writeRecoveryTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}
