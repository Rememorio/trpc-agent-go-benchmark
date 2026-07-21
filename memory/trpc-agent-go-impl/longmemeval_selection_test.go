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
	"reflect"
	"strings"
	"testing"
)

func TestResolveLongMemEvalPreregisteredSelection(t *testing.T) {
	originalManifest := *flagLMEPreregisteredSelection
	originalSelectionOnly := *flagLMESelectionOnly
	originalQuestionID := *flagLMEQuestionID
	originalQuestionIDs := *flagLMEQuestionIDs
	originalExcluded := *flagLMEExcludeQuestionIDs
	originalExcludedFile := *flagLMEExcludeQuestionIDsFile
	originalTypes := *flagLMEQuestionTypes
	originalPerType := *flagLMEPerType
	originalAbstention := *flagLMEAbstentionCount
	originalMaxTasks := *flagMaxTasks
	t.Cleanup(func() {
		*flagLMEPreregisteredSelection = originalManifest
		*flagLMESelectionOnly = originalSelectionOnly
		*flagLMEQuestionID = originalQuestionID
		*flagLMEQuestionIDs = originalQuestionIDs
		*flagLMEExcludeQuestionIDs = originalExcluded
		*flagLMEExcludeQuestionIDsFile = originalExcludedFile
		*flagLMEQuestionTypes = originalTypes
		*flagLMEPerType = originalPerType
		*flagLMEAbstentionCount = originalAbstention
		*flagMaxTasks = originalMaxTasks
	})
	*flagLMEPreregisteredSelection = ""
	*flagLMESelectionOnly = false
	*flagLMEQuestionID = "q1"
	*flagLMEQuestionIDs = ""
	*flagLMEExcludeQuestionIDs = ""
	*flagLMEExcludeQuestionIDsFile = ""
	*flagLMEQuestionTypes = ""
	*flagLMEPerType = 0
	*flagLMEAbstentionCount = 0
	*flagMaxTasks = 0
	instances := []*lmeInstance{
		{QuestionID: "q1", QuestionType: "single-session-user"},
		{QuestionID: "q2_abs", QuestionType: "temporal-reasoning"},
	}

	resolved, err := resolveLongMemEvalSelection(instances)
	if err != nil {
		t.Fatalf("resolve ordinary selection: %v", err)
	}
	if resolved.Preregistered != nil || len(resolved.Cases) != 1 ||
		resolved.Cases[0].QuestionID != "q1" {
		t.Fatalf("unexpected ordinary selection: cases=%v selection=%+v",
			questionIDs(resolved.Cases), resolved.Preregistered)
	}

	*flagLMEQuestionID = ""
	manifest := testLongMemEvalSelectionManifest(t, instances)
	manifest.Cases[0], manifest.Cases[1] = manifest.Cases[1], manifest.Cases[0]
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	path := filepath.Join(t.TempDir(), "selection.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	*flagLMEPreregisteredSelection = path
	resolved, err = resolveLongMemEvalSelection(instances)
	if err != nil {
		t.Fatalf("resolve preregistered selection: %v", err)
	}
	if resolved.Preregistered == nil ||
		resolved.Preregistered.ManifestSHA256 == "" ||
		len(resolved.Cases) != 2 || resolved.Cases[0].QuestionID != "q2_abs" {
		t.Fatalf("unexpected preregistered selection: cases=%v selection=%+v",
			questionIDs(resolved.Cases), resolved.Preregistered)
	}

	*flagLMEPerType = 1
	if _, err := resolveLongMemEvalSelection(instances); err == nil ||
		!strings.Contains(err.Error(), "-lme-per-type") {
		t.Fatalf("conflicting flags error = %v", err)
	}
	*flagLMEPerType = 0
	*flagLMEPreregisteredSelection = filepath.Join(t.TempDir(), "missing.json")
	if _, err := resolveLongMemEvalSelection(instances); err == nil ||
		!strings.Contains(err.Error(), "open LongMemEval selection manifest") {
		t.Fatalf("missing manifest error = %v", err)
	}
}

func TestLoadLongMemEvalExcludedQuestionIDs(t *testing.T) {
	originalIDs := *flagLMEExcludeQuestionIDs
	originalFile := *flagLMEExcludeQuestionIDsFile
	t.Cleanup(func() {
		*flagLMEExcludeQuestionIDs = originalIDs
		*flagLMEExcludeQuestionIDsFile = originalFile
	})
	instances := []*lmeInstance{
		{QuestionID: "q1"},
		{QuestionID: "q2_abs"},
		{QuestionID: "q3"},
		nil,
	}
	path := filepath.Join(t.TempDir(), "excluded.txt")
	if err := os.WriteFile(path, []byte("q2_abs\n q3 \nq2_abs\n\n"), 0644); err != nil {
		t.Fatalf("write exclusion file: %v", err)
	}
	*flagLMEExcludeQuestionIDs = "q3,q1"
	*flagLMEExcludeQuestionIDsFile = path
	got, err := loadLongMemEvalExcludedQuestionIDs(instances)
	if err != nil {
		t.Fatalf("load exclusions: %v", err)
	}
	want := []string{"q1", "q2_abs", "q3"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("exclusions = %v, want %v", got, want)
	}

	*flagLMEExcludeQuestionIDs = "missing"
	*flagLMEExcludeQuestionIDsFile = ""
	if _, err := loadLongMemEvalExcludedQuestionIDs(instances); err == nil ||
		!strings.Contains(err.Error(), "absent from the dataset") {
		t.Fatalf("unknown exclusion error = %v", err)
	}
	*flagLMEExcludeQuestionIDs = ""
	*flagLMEExcludeQuestionIDsFile = filepath.Join(t.TempDir(), "missing.txt")
	if _, err := loadLongMemEvalExcludedQuestionIDs(instances); err == nil ||
		!strings.Contains(err.Error(), "open LongMemEval question ID exclusion file") {
		t.Fatalf("missing exclusion file error = %v", err)
	}
}

func TestLongMemEvalCasesFromSelection(t *testing.T) {
	instances := []*lmeInstance{
		{QuestionID: "q1", QuestionType: "single-session-user"},
		{QuestionID: "q2_abs", QuestionType: "temporal-reasoning"},
	}
	manifest := testLongMemEvalSelectionManifest(t, instances)
	manifest.Cases[0], manifest.Cases[1] = manifest.Cases[1], manifest.Cases[0]

	cases, err := longMemEvalCasesFromSelection(instances, manifest)
	if err != nil {
		t.Fatalf("resolve selection: %v", err)
	}
	if len(cases) != 2 || cases[0].QuestionID != "q2_abs" ||
		cases[1].QuestionID != "q1" {
		t.Fatalf("selection order was not preserved: %+v", questionIDs(cases))
	}

	tests := []struct {
		name      string
		instances []*lmeInstance
		mutate    func(*lmeSelectionManifest)
		want      string
	}{
		{
			name:      "empty manifest",
			instances: instances,
			mutate:    func(m *lmeSelectionManifest) { m.Cases = nil },
			want:      "has no cases",
		},
		{
			name: "empty dataset id",
			instances: []*lmeInstance{
				{QuestionID: "", QuestionType: "single-session-user"},
			},
			want: "empty question_id",
		},
		{
			name: "duplicate dataset id",
			instances: []*lmeInstance{
				{QuestionID: "q1", QuestionType: "single-session-user"},
				{QuestionID: "q1", QuestionType: "single-session-user"},
			},
			want: "dataset contains duplicate",
		},
		{
			name:      "empty selected id",
			instances: instances,
			mutate: func(m *lmeSelectionManifest) {
				m.Cases[0].QuestionID = ""
			},
			want: "manifest contains an empty",
		},
		{
			name:      "duplicate selected id",
			instances: instances,
			mutate: func(m *lmeSelectionManifest) {
				m.Cases[1] = m.Cases[0]
			},
			want: "manifest contains duplicate",
		},
		{
			name:      "missing selected id",
			instances: instances,
			mutate: func(m *lmeSelectionManifest) {
				m.Cases[0].QuestionID = "missing"
			},
			want: "absent from the dataset",
		},
		{
			name:      "type drift",
			instances: instances,
			mutate: func(m *lmeSelectionManifest) {
				m.Cases[0].QuestionType = "multi-session"
			},
			want: "type is",
		},
		{
			name:      "abstention drift",
			instances: instances,
			mutate: func(m *lmeSelectionManifest) {
				m.Cases[0].Abstention = true
			},
			want: "abstention is",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := testLongMemEvalSelectionManifest(t, instances)
			if test.mutate != nil {
				test.mutate(&candidate)
			}
			_, err := longMemEvalCasesFromSelection(test.instances, candidate)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestLoadLongMemEvalSelectionManifest(t *testing.T) {
	manifest := testLongMemEvalSelectionManifest(t, []*lmeInstance{{
		QuestionID: "q1", QuestionType: "single-session-user",
	}})
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	path := filepath.Join(t.TempDir(), "selection.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	got, digest, err := loadLongMemEvalSelectionManifest(path)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	wantDigest, err := longMemEvalFileSHA256(path)
	if err != nil {
		t.Fatalf("hash manifest: %v", err)
	}
	if got.SelectionSHA256 != manifest.SelectionSHA256 || digest != wantDigest {
		t.Fatalf("loaded manifest = %+v digest=%q", got, digest)
	}

	unknownPath := filepath.Join(t.TempDir(), "unknown.json")
	if err := os.WriteFile(unknownPath, []byte(`{"unknown":true}`), 0644); err != nil {
		t.Fatalf("write unknown manifest: %v", err)
	}
	if _, _, err := loadLongMemEvalSelectionManifest(unknownPath); err == nil ||
		!strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}

	trailingPath := filepath.Join(t.TempDir(), "trailing.json")
	if err := os.WriteFile(trailingPath, append(data, []byte("\n{}")...), 0644); err != nil {
		t.Fatalf("write trailing manifest: %v", err)
	}
	if _, _, err := loadLongMemEvalSelectionManifest(trailingPath); err == nil ||
		!strings.Contains(err.Error(), "multiple JSON values") {
		t.Fatalf("trailing data error = %v", err)
	}
}

func TestValidateLongMemEvalPreregisteredSelection(t *testing.T) {
	instances := []*lmeInstance{
		{QuestionID: "q1", QuestionType: "single-session-user"},
		{QuestionID: "q2_abs", QuestionType: "temporal-reasoning"},
	}
	manifest := testLongMemEvalSelectionManifest(t, instances)
	excludedDigest, err := longMemEvalJSONSHA256(
		[]string{"excluded-a", "excluded-b"},
	)
	if err != nil {
		t.Fatalf("hash exclusions: %v", err)
	}
	manifest.ExcludedCount = 2
	manifest.ExcludedSHA256 = excludedDigest
	selection := &lmePreregisteredSelection{
		Manifest:       manifest,
		ManifestSHA256: "manifest-digest",
	}

	if err := validateLongMemEvalPreregisteredSelection(
		selection,
		instances,
		[]string{"excluded-a", "excluded-b"},
		"dataset-digest",
		"selection-digest",
		manifest.ProtocolSHA256,
		manifest.Build,
	); err != nil {
		t.Fatalf("validate selection: %v", err)
	}
	if err := validateLongMemEvalPreregisteredSelection(
		nil, nil, nil, "", "", "", lmeBuildProvenance{},
	); err != nil {
		t.Fatalf("nil selection: %v", err)
	}
	metadata := selection.metadata()
	if metadata["manifest_sha256"] != "manifest-digest" ||
		metadata["case_count"] != 2 || metadata["excluded_question_id_count"] != 2 {
		t.Fatalf("unexpected selection metadata: %+v", metadata)
	}
	if (*lmePreregisteredSelection)(nil).metadata() != nil {
		t.Fatal("nil selection returned metadata")
	}

	tests := []struct {
		name       string
		mutate     func(*lmeSelectionManifest, *lmeBuildProvenance)
		dataset    string
		selection  string
		protocol   string
		exclusions string
		want       string
	}{
		{
			name: "legacy schema",
			mutate: func(m *lmeSelectionManifest, _ *lmeBuildProvenance) {
				m.SchemaVersion = 1
			},
			want: "schema version",
		},
		{
			name:    "dataset drift",
			dataset: "other-dataset",
			want:    "dataset digest",
		},
		{
			name:      "selection drift",
			selection: "other-selection",
			want:      "selection digest",
		},
		{
			name: "protocol version drift",
			mutate: func(m *lmeSelectionManifest, _ *lmeBuildProvenance) {
				m.ProtocolVersion = "other-version"
			},
			want: "protocol version",
		},
		{
			name:     "protocol digest drift",
			protocol: "other-protocol",
			want:     "protocol digest",
		},
		{
			name: "protocol payload version drift",
			mutate: func(m *lmeSelectionManifest, _ *lmeBuildProvenance) {
				m.Protocol.Version = "other-version"
			},
			want: "protocol payload version",
		},
		{
			name: "protocol payload digest drift",
			mutate: func(m *lmeSelectionManifest, _ *lmeBuildProvenance) {
				m.Protocol.TopK++
			},
			want: "protocol payload digest",
		},
		{
			name: "missing answer model",
			mutate: func(m *lmeSelectionManifest, _ *lmeBuildProvenance) {
				m.Protocol.AnswerModel = ""
			},
			want: "answer model is missing",
		},
		{
			name: "invalid judge runs",
			mutate: func(m *lmeSelectionManifest, _ *lmeBuildProvenance) {
				m.Protocol.JudgeRuns = 2
			},
			want: "positive odd number",
		},
		{
			name: "invalid answer generation",
			mutate: func(m *lmeSelectionManifest, _ *lmeBuildProvenance) {
				m.Protocol.AnswerGeneration.MaxAttempts = 0
			},
			want: "answer generation contract",
		},
		{
			name:       "negative sampling metadata",
			exclusions: "excluded-b,excluded-a",
			mutate: func(m *lmeSelectionManifest, _ *lmeBuildProvenance) {
				m.SamplePerType = -1
			},
			want: "negative sampling",
		},
		{
			name:       "exclusion drift",
			exclusions: "different-exclusion",
			want:       "exclusion set",
		},
		{
			name:       "invalid preregistration build",
			exclusions: "excluded-b,excluded-a",
			mutate: func(m *lmeSelectionManifest, _ *lmeBuildProvenance) {
				m.Build.Modified = true
			},
			want: "preregistration build",
		},
		{
			name:       "invalid current build",
			exclusions: "excluded-b,excluded-a",
			mutate: func(_ *lmeSelectionManifest, b *lmeBuildProvenance) {
				b.Modified = true
			},
			want: "current LongMemEval build",
		},
		{
			name:       "benchmark revision drift",
			exclusions: "excluded-b,excluded-a",
			mutate: func(_ *lmeSelectionManifest, b *lmeBuildProvenance) {
				b.Revision = "other-revision"
			},
			want: "benchmark revision",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := manifest
			build := manifest.Build
			if test.mutate != nil {
				test.mutate(&candidate, &build)
			}
			dataset := test.dataset
			if dataset == "" {
				dataset = "dataset-digest"
			}
			selected := test.selection
			if selected == "" {
				selected = "selection-digest"
			}
			protocol := test.protocol
			if protocol == "" {
				protocol = manifest.ProtocolSHA256
			}
			exclusions := []string{"excluded-a", "excluded-b"}
			if test.exclusions == "different-exclusion" {
				exclusions = []string{"different-exclusion"}
			}
			err := validateLongMemEvalPreregisteredSelection(
				&lmePreregisteredSelection{Manifest: candidate},
				instances,
				exclusions,
				dataset,
				selected,
				protocol,
				build,
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}

	selectedExcluded := manifest
	selectedExcluded.ExcludedCount = 1
	selectedExcluded.ExcludedSHA256, err = longMemEvalJSONSHA256([]string{"q1"})
	if err != nil {
		t.Fatalf("hash selected exclusion: %v", err)
	}
	if err := validateLongMemEvalPreregisteredSelection(
		&lmePreregisteredSelection{Manifest: selectedExcluded},
		instances,
		[]string{"q1"},
		"dataset-digest",
		"selection-digest",
		manifest.ProtocolSHA256,
		manifest.Build,
	); err == nil || !strings.Contains(err.Error(), "selected excluded") {
		t.Fatalf("selected exclusion error = %v", err)
	}
}

func TestValidateLongMemEvalPreregisteredSelectionFlags(t *testing.T) {
	originalSelectionOnly := *flagLMESelectionOnly
	originalQuestionID := *flagLMEQuestionID
	originalQuestionIDs := *flagLMEQuestionIDs
	originalTypes := *flagLMEQuestionTypes
	originalPerType := *flagLMEPerType
	originalAbstention := *flagLMEAbstentionCount
	originalMaxTasks := *flagMaxTasks
	t.Cleanup(func() {
		*flagLMESelectionOnly = originalSelectionOnly
		*flagLMEQuestionID = originalQuestionID
		*flagLMEQuestionIDs = originalQuestionIDs
		*flagLMEQuestionTypes = originalTypes
		*flagLMEPerType = originalPerType
		*flagLMEAbstentionCount = originalAbstention
		*flagMaxTasks = originalMaxTasks
	})
	*flagLMESelectionOnly = false
	*flagLMEQuestionID = ""
	*flagLMEQuestionIDs = ""
	*flagLMEQuestionTypes = ""
	*flagLMEPerType = 0
	*flagLMEAbstentionCount = 0
	*flagMaxTasks = 0
	if err := validateLongMemEvalPreregisteredSelectionFlags(); err != nil {
		t.Fatalf("default flags: %v", err)
	}

	*flagLMESelectionOnly = true
	*flagLMEQuestionID = "q1"
	*flagLMEQuestionIDs = "q2"
	*flagLMEQuestionTypes = "single-session-user"
	*flagLMEPerType = 2
	*flagLMEAbstentionCount = 4
	*flagMaxTasks = 1
	err := validateLongMemEvalPreregisteredSelectionFlags()
	for _, flagName := range []string{
		"-lme-selection-only",
		"-lme-question-id",
		"-lme-question-ids",
		"-lme-question-types",
		"-lme-per-type",
		"-lme-abstention-count",
		"-max-tasks",
	} {
		if err == nil || !strings.Contains(err.Error(), flagName) {
			t.Fatalf("conflict error %v omitted %s", err, flagName)
		}
	}
}

func testLongMemEvalSelectionManifest(
	t *testing.T,
	instances []*lmeInstance,
) lmeSelectionManifest {
	t.Helper()
	protocol := testLongMemEvalProtocol()
	protocolDigest, err := longMemEvalJSONSHA256(protocol)
	if err != nil {
		t.Fatalf("hash protocol: %v", err)
	}
	cases := make([]lmeSelectionCase, 0, len(instances))
	for _, instance := range instances {
		if instance == nil {
			continue
		}
		cases = append(cases, lmeSelectionCase{
			QuestionID:   instance.QuestionID,
			QuestionType: instance.QuestionType,
			Abstention:   isAbstentionQuestion(instance),
		})
	}
	return lmeSelectionManifest{
		SchemaVersion:   lmeSelectionManifestSchemaVersion,
		Build:           testLongMemEvalBuildProvenance("benchmark-revision"),
		DatasetSHA256:   "dataset-digest",
		SelectionSHA256: "selection-digest",
		ProtocolVersion: lmeProtocolVersion,
		ProtocolSHA256:  protocolDigest,
		Protocol:        protocol,
		SamplePerType:   2,
		AbstentionCount: 4,
		SampleSeed:      271,
		Cases:           cases,
	}
}

func testLongMemEvalProtocol() lmeProtocolProvenance {
	return lmeProtocolProvenance{
		Version:              lmeProtocolVersion,
		AnswerModel:          "answer-model",
		AnswerModelVariant:   "glm",
		AnswerPromptVersion:  lmeAnswerPromptVersion,
		AnswerGeneration:     currentLongMemEvalAnswerGeneration(),
		EmbeddingModel:       "embedding-model",
		JudgeModel:           "judge-model",
		JudgeModelVariant:    "glm",
		JudgeRuns:            3,
		JudgePromptVersion:   lmeJudgePromptVersion,
		JudgeProtocolVersion: lmeJudgeProtocolVersion,
		JudgeGeneration:      currentLongMemEvalJudgeGeneration(),
		TopK:                 30,
		MaxSessions:          0,
		MaxPairs:             0,
	}
}

func TestCurrentLongMemEvalProtocolBindsEvaluationConfiguration(t *testing.T) {
	originalModel := *flagModel
	originalVariant := *flagModelVariant
	originalEvalModel := *flagEvalModel
	originalEmbedModel := *flagEmbedModel
	originalJudgeRuns := *flagLMEJudgeRuns
	t.Cleanup(func() {
		*flagModel = originalModel
		*flagModelVariant = originalVariant
		*flagEvalModel = originalEvalModel
		*flagEmbedModel = originalEmbedModel
		*flagLMEJudgeRuns = originalJudgeRuns
	})
	*flagModel = "answer-model-v2"
	*flagModelVariant = "glm"
	*flagEvalModel = "judge-model-v2"
	*flagEmbedModel = "embedding-model-v2"
	*flagLMEJudgeRuns = 3

	protocol := currentLongMemEvalProtocol()
	if protocol.AnswerModel != *flagModel ||
		protocol.AnswerModelVariant != *flagModelVariant ||
		protocol.EmbeddingModel != *flagEmbedModel ||
		protocol.JudgeModel != *flagEvalModel ||
		protocol.JudgeModelVariant != *flagModelVariant ||
		protocol.JudgeRuns != *flagLMEJudgeRuns ||
		protocol.AnswerPromptVersion != lmeAnswerPromptVersion ||
		protocol.JudgePromptVersion != lmeJudgePromptVersion ||
		protocol.JudgeProtocolVersion != lmeJudgeProtocolVersion ||
		!reflect.DeepEqual(protocol.AnswerGeneration,
			currentLongMemEvalAnswerGeneration()) ||
		!reflect.DeepEqual(protocol.JudgeGeneration,
			currentLongMemEvalJudgeGeneration()) {
		t.Fatalf("protocol omitted evaluation configuration: %+v", protocol)
	}
	if err := validateLongMemEvalProtocol(protocol); err != nil {
		t.Fatalf("validate protocol: %v", err)
	}

	digest, err := longMemEvalJSONSHA256(protocol)
	if err != nil {
		t.Fatalf("hash protocol: %v", err)
	}
	for name, mutate := range map[string]func(*lmeProtocolProvenance){
		"answer model":    func(p *lmeProtocolProvenance) { p.AnswerModel = "other" },
		"embedding model": func(p *lmeProtocolProvenance) { p.EmbeddingModel = "other" },
		"judge runs":      func(p *lmeProtocolProvenance) { p.JudgeRuns = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			changed := protocol
			mutate(&changed)
			changedDigest, hashErr := longMemEvalJSONSHA256(changed)
			if hashErr != nil {
				t.Fatalf("hash changed protocol: %v", hashErr)
			}
			if changedDigest == digest {
				t.Fatalf("%s did not change protocol digest", name)
			}
		})
	}
}

func TestValidateLongMemEvalResultProtocol(t *testing.T) {
	protocol := testLongMemEvalProtocol()
	digest, err := longMemEvalJSONSHA256(protocol)
	if err != nil {
		t.Fatalf("hash protocol: %v", err)
	}
	newMetadata := func(t *testing.T) map[string]any {
		t.Helper()
		data, marshalErr := json.Marshal(protocol)
		if marshalErr != nil {
			t.Fatalf("marshal protocol: %v", marshalErr)
		}
		var payload map[string]any
		if unmarshalErr := json.Unmarshal(data, &payload); unmarshalErr != nil {
			t.Fatalf("unmarshal protocol: %v", unmarshalErr)
		}
		return map[string]any{
			"protocol":         payload,
			"protocol_version": protocol.Version,
			"protocol_sha256":  digest,
		}
	}

	if err := validateLongMemEvalResultProtocol(
		newMetadata(t), protocol,
	); err != nil {
		t.Fatalf("validate matching result protocol: %v", err)
	}
	migrated := protocol
	migrated.AnswerEnabled = !protocol.AnswerEnabled
	migrated.AnswerPromptVersion = "new-answer-prompt"
	migrated.AnswerGeneration.RetryMaxTokens++
	migrated.JudgePromptVersion = "new-judge-prompt"
	migrated.JudgeGeneration.RepairMaxTokens++
	metadata := newMetadata(t)
	sourceDigest, err := validateLongMemEvalReanswerSourceProtocol(
		metadata, migrated,
	)
	if err != nil || sourceDigest != digest {
		t.Fatalf(
			"validate re-answer migration digest = %q, err = %v",
			sourceDigest, err,
		)
	}
	newDigest, err := replaceLongMemEvalResultProtocol(metadata, migrated)
	if err != nil || newDigest == digest {
		t.Fatalf(
			"replace re-answer protocol digest = %q, err = %v",
			newDigest, err,
		)
	}
	if err := validateLongMemEvalResultProtocol(metadata, migrated); err != nil {
		t.Fatalf("validate replaced re-answer protocol: %v", err)
	}
	drifted := migrated
	drifted.TopK++
	if _, err := validateLongMemEvalReanswerSourceProtocol(
		newMetadata(t), drifted,
	); err == nil || !strings.Contains(err.Error(), "outside the answer/judge contract") {
		t.Fatalf("re-answer source drift error = %v", err)
	}
	if err := validateLongMemEvalResultProtocol(nil, protocol); err == nil ||
		!strings.Contains(err.Error(), "metadata is missing") {
		t.Fatalf("missing metadata error = %v", err)
	}

	tests := []struct {
		name    string
		current lmeProtocolProvenance
		mutate  func(map[string]any)
		want    string
	}{
		{
			name:    "missing protocol",
			current: protocol,
			mutate:  func(metadata map[string]any) { delete(metadata, "protocol") },
			want:    "protocol is missing",
		},
		{
			name:    "unencodable protocol",
			current: protocol,
			mutate: func(metadata map[string]any) {
				metadata["protocol"] = make(chan int)
			},
			want: "marshal LongMemEval result protocol",
		},
		{
			name:    "unknown protocol field",
			current: protocol,
			mutate: func(metadata map[string]any) {
				metadata["protocol"].(map[string]any)["unknown"] = true
			},
			want: "unknown field",
		},
		{
			name:    "declared version drift",
			current: protocol,
			mutate: func(metadata map[string]any) {
				metadata["protocol_version"] = "other-version"
			},
			want: "payload version",
		},
		{
			name:    "declared digest drift",
			current: protocol,
			mutate: func(metadata map[string]any) {
				metadata["protocol_sha256"] = "other-digest"
			},
			want: "payload digest",
		},
		{
			name:    "invalid recorded protocol",
			current: protocol,
			mutate: func(metadata map[string]any) {
				metadata["protocol"].(map[string]any)["answer_model"] = ""
			},
			want: "invalid recorded LongMemEval protocol",
		},
		{
			name: "current protocol drift",
			current: func() lmeProtocolProvenance {
				changed := protocol
				changed.AnswerModel = "other-answer-model"
				return changed
			}(),
			want: "result requires",
		},
		{
			name: "invalid current protocol",
			current: func() lmeProtocolProvenance {
				changed := protocol
				changed.TopK = 0
				return changed
			}(),
			want: "invalid current LongMemEval protocol",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata := newMetadata(t)
			if test.mutate != nil {
				test.mutate(metadata)
			}
			err := validateLongMemEvalResultProtocol(metadata, test.current)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestValidateLongMemEvalProtocolRejectsInvalidContracts(t *testing.T) {
	valid := testLongMemEvalProtocol()
	if err := validateLongMemEvalProtocol(valid); err != nil {
		t.Fatalf("validate protocol: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*lmeProtocolProvenance)
		want   string
	}{
		{
			name:   "version",
			mutate: func(p *lmeProtocolProvenance) { p.Version = "legacy" },
			want:   "protocol version",
		},
		{
			name:   "missing embedding model",
			mutate: func(p *lmeProtocolProvenance) { p.EmbeddingModel = "" },
			want:   "embedding model is missing",
		},
		{
			name:   "judge runs",
			mutate: func(p *lmeProtocolProvenance) { p.JudgeRuns = 0 },
			want:   "positive odd number",
		},
		{
			name:   "top-k",
			mutate: func(p *lmeProtocolProvenance) { p.TopK = 0 },
			want:   "top-k must be positive",
		},
		{
			name:   "negative session limit",
			mutate: func(p *lmeProtocolProvenance) { p.MaxSessions = -1 },
			want:   "must not be negative",
		},
		{
			name: "answer generation",
			mutate: func(p *lmeProtocolProvenance) {
				p.AnswerGeneration.PrimaryMaxTokens = 0
			},
			want: "answer generation contract",
		},
		{
			name: "judge generation",
			mutate: func(p *lmeProtocolProvenance) {
				p.JudgeGeneration.RepairMaxTokens = 0
			},
			want: "judge generation contract",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			protocol := valid
			test.mutate(&protocol)
			err := validateLongMemEvalProtocol(protocol)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}
