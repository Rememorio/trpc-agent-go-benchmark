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
		mutate  func(*recoveryTestFixture)
		wantErr string
	}{
		{
			name: "source hash",
			mutate: func(f *recoveryTestFixture) {
				f.manifest.Source.ResultsSHA256 = strings.Repeat("0", 64)
			},
			wantErr: "source results hash mismatch",
		},
		{
			name: "source summary",
			mutate: func(f *recoveryTestFixture) {
				f.source.Summary.TotalCases++
			},
			wantErr: "source summary does not match",
		},
		{
			name: "source cases",
			mutate: func(f *recoveryTestFixture) {
				f.source.Cases = nil
				f.source.Summary = buildLongMemEvalSummary(f.source.Cases)
			},
			wantErr: "source results contain no cases",
		},
		{
			name: "source metadata",
			mutate: func(f *recoveryTestFixture) {
				f.source.Metadata = nil
			},
			wantErr: "source results have no metadata",
		},
		{
			name: "missing source summary",
			mutate: func(f *recoveryTestFixture) {
				f.source.Summary = nil
			},
			wantErr: "source results have no summary",
		},
		{
			name: "duplicate source case",
			mutate: func(f *recoveryTestFixture) {
				f.source.Cases = append(
					f.source.Cases, f.source.Cases[0],
				)
				f.source.Summary = buildLongMemEvalSummary(f.source.Cases)
			},
			wantErr: "duplicate case",
		},
		{
			name: "replacement summary",
			mutate: func(f *recoveryTestFixture) {
				f.replacement.Summary.TotalCases++
			},
			wantErr: "replacement summary does not match",
		},
		{
			name: "replacement cases",
			mutate: func(f *recoveryTestFixture) {
				f.replacement.Cases = nil
				f.replacement.Summary = buildLongMemEvalSummary(
					f.replacement.Cases,
				)
			},
			wantErr: "replacement results contain no cases",
		},
		{
			name: "replacement metadata",
			mutate: func(f *recoveryTestFixture) {
				f.replacement.Metadata = nil
			},
			wantErr: "replacement results have no metadata",
		},
		{
			name: "protocol mismatch",
			mutate: func(f *recoveryTestFixture) {
				f.replacement.Metadata["protocol_sha256"] = "other"
			},
			wantErr: `metadata "protocol_sha256" does not match`,
		},
		{
			name: "build mismatch",
			mutate: func(f *recoveryTestFixture) {
				f.replacement.Metadata["build"] = map[string]any{
					"benchmark_revision": "other",
				}
			},
			wantErr: "build metadata does not match",
		},
		{
			name: "unregistered backend",
			mutate: func(f *recoveryTestFixture) {
				f.replacement.Cases[0].BackendResults["mem0"] =
					&backendResult{Backend: "mem0"}
				f.replacement.Summary = buildLongMemEvalSummary(
					f.replacement.Cases,
				)
			},
			wantErr: "unregistered unit",
		},
		{
			name: "duplicate expected unit",
			mutate: func(f *recoveryTestFixture) {
				f.manifest.ExpectedUnits = append(
					f.manifest.ExpectedUnits,
					f.manifest.ExpectedUnits[0],
				)
			},
			wantErr: "duplicate unit",
		},
		{
			name: "invalid expected unit hash",
			mutate: func(f *recoveryTestFixture) {
				f.manifest.ExpectedUnits[0].QuestionIDSHA256 = "invalid"
			},
			wantErr: "question_id_sha256 is invalid",
		},
		{
			name: "missing expected unit",
			mutate: func(f *recoveryTestFixture) {
				f.manifest.ExpectedUnits = append(
					f.manifest.ExpectedUnits,
					longMemEvalRecoveryUnit{
						QuestionIDSHA256: sha256String("question-1"),
						Backend:          "mem0",
					},
				)
			},
			wantErr: "replaced 1 unit(s), want 2",
		},
		{
			name: "duplicate replacement unit",
			mutate: func(f *recoveryTestFixture) {
				f.manifest.Replacements = append(
					f.manifest.Replacements,
					f.manifest.Replacements[0],
				)
			},
			wantErr: "appears more than once",
		},
		{
			name: "replacement case absent from source",
			mutate: func(f *recoveryTestFixture) {
				f.replacement.Cases[0].QuestionID = "question-9"
				f.replacement.Summary = buildLongMemEvalSummary(
					f.replacement.Cases,
				)
			},
			wantErr: "absent from source results",
		},
		{
			name: "case mismatch",
			mutate: func(f *recoveryTestFixture) {
				f.replacement.Cases[0].QuestionType = "different"
			},
			wantErr: "case metadata does not match",
		},
		{
			name: "no replacement backend",
			mutate: func(f *recoveryTestFixture) {
				f.replacement.Cases[0].BackendResults = nil
				f.replacement.Summary = buildLongMemEvalSummary(
					f.replacement.Cases,
				)
			},
			wantErr: "contains no backend results",
		},
		{
			name: "nil replacement backend",
			mutate: func(f *recoveryTestFixture) {
				f.replacement.Cases[0].BackendResults["pgvector"] = nil
				f.replacement.Summary = buildLongMemEvalSummary(
					f.replacement.Cases,
				)
			},
			wantErr: "nil backend result",
		},
		{
			name: "backend name mismatch",
			mutate: func(f *recoveryTestFixture) {
				f.replacement.Cases[0].
					BackendResults["pgvector"].Backend = "mem0"
			},
			wantErr: "does not match result backend",
		},
		{
			name: "backend absent from source",
			mutate: func(f *recoveryTestFixture) {
				delete(f.source.Cases[0].BackendResults, "pgvector")
				f.source.Summary = buildLongMemEvalSummary(f.source.Cases)
			},
			wantErr: "absent from source case",
		},
		{
			name: "runtime error",
			mutate: func(f *recoveryTestFixture) {
				f.replacement.Cases[0].BackendResults["pgvector"].Error =
					"provider failed"
			},
			wantErr: "contains a runtime error",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newRecoveryTestFixture(t)
			tc.mutate(fixture)
			err := fixture.apply(t)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadLongMemEvalRecoveryManifestRejectsInvalidInput(t *testing.T) {
	testCases := []struct {
		name    string
		mutate  func(*longMemEvalRecoveryManifest)
		wantErr string
	}{
		{
			name: "schema version",
			mutate: func(manifest *longMemEvalRecoveryManifest) {
				manifest.SchemaVersion++
			},
			wantErr: "schema_version",
		},
		{
			name: "status",
			mutate: func(manifest *longMemEvalRecoveryManifest) {
				manifest.Status = "draft"
			},
			wantErr: "is not preregistered",
		},
		{
			name: "registered at",
			mutate: func(manifest *longMemEvalRecoveryManifest) {
				manifest.RegisteredAt = "not-a-time"
			},
			wantErr: "parse recovery registered_at",
		},
		{
			name: "reason",
			mutate: func(manifest *longMemEvalRecoveryManifest) {
				manifest.Reason = " "
			},
			wantErr: "reason is empty",
		},
		{
			name: "quality inspected",
			mutate: func(manifest *longMemEvalRecoveryManifest) {
				manifest.QualityOutcomesInspected = true
			},
			wantErr: "inspected quality outcomes",
		},
		{
			name: "source",
			mutate: func(manifest *longMemEvalRecoveryManifest) {
				manifest.Source.ResultsPath = ""
			},
			wantErr: "source is incomplete",
		},
		{
			name: "replacements",
			mutate: func(manifest *longMemEvalRecoveryManifest) {
				manifest.Replacements = nil
			},
			wantErr: "has no replacements",
		},
		{
			name: "expected units",
			mutate: func(manifest *longMemEvalRecoveryManifest) {
				manifest.ExpectedUnits = nil
			},
			wantErr: "has no replacements",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newRecoveryTestFixture(t)
			fixture.manifest.Source.ResultsSHA256 = strings.Repeat("0", 64)
			tc.mutate(&fixture.manifest)
			writeRecoveryTestJSON(
				t, fixture.manifestPath, fixture.manifest,
			)
			_, _, err := loadLongMemEvalRecoveryManifest(
				fixture.manifestPath,
			)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
		})
	}

	t.Run("unknown field", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "manifest.json")
		manifest := newRecoveryTestFixture(t).manifest
		data, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]any
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		value["unexpected"] = true
		writeRecoveryTestJSON(t, path, value)
		_, _, err = loadLongMemEvalRecoveryManifest(path)
		if err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("error = %v, want unknown field", err)
		}
	})

	t.Run("multiple values", func(t *testing.T) {
		fixture := newRecoveryTestFixture(t)
		writeRecoveryTestJSON(
			t, fixture.manifestPath, fixture.manifest,
		)
		file, err := os.OpenFile(
			fixture.manifestPath, os.O_APPEND|os.O_WRONLY, 0,
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString("{}\n"); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		_, _, err = loadLongMemEvalRecoveryManifest(
			fixture.manifestPath,
		)
		if err == nil || !strings.Contains(err.Error(), "multiple JSON values") {
			t.Fatalf("error = %v, want multiple JSON values", err)
		}
	})
}

type recoveryTestFixture struct {
	dir             string
	manifestPath    string
	sourcePath      string
	replacementPath string
	outputDir       string
	manifest        longMemEvalRecoveryManifest
	source          *runResult
	replacement     *runResult
}

func newRecoveryTestFixture(t *testing.T) *recoveryTestFixture {
	t.Helper()
	dir := t.TempDir()
	replacement := recoveryTestResult("replacement", 1)
	replacement.Cases[0].BackendResults = map[string]*backendResult{
		"pgvector": replacement.Cases[0].BackendResults["pgvector"],
	}
	replacement.Summary = buildLongMemEvalSummary(replacement.Cases)
	return &recoveryTestFixture{
		dir:             dir,
		manifestPath:    filepath.Join(dir, "manifest.json"),
		sourcePath:      filepath.Join(dir, "source.json"),
		replacementPath: filepath.Join(dir, "replacement.json"),
		outputDir:       filepath.Join(dir, "output"),
		manifest: longMemEvalRecoveryManifest{
			SchemaVersion: longMemEvalRecoverySchemaVersion,
			Status:        "registered-before-provider-calls",
			RegisteredAt:  time.Now().UTC().Format(time.RFC3339),
			Reason:        "provider_failure",
			Source: longMemEvalRecoverySource{
				ResultsPath: "source.json",
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
		},
		source:      recoveryTestResult("source", 1),
		replacement: replacement,
	}
}

func (f *recoveryTestFixture) apply(t *testing.T) error {
	t.Helper()
	writeRecoveryTestResults(t, f.sourcePath, f.source)
	if f.manifest.Source.ResultsSHA256 == "" {
		sourceSHA, err := sha256File(f.sourcePath)
		if err != nil {
			t.Fatal(err)
		}
		f.manifest.Source.ResultsSHA256 = sourceSHA
	}
	writeRecoveryTestResults(t, f.replacementPath, f.replacement)
	writeRecoveryTestJSON(t, f.manifestPath, f.manifest)
	return applyLongMemEvalRecovery(f.manifestPath, f.outputDir)
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
