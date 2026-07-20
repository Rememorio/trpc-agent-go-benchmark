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
	"strings"
	"testing"
)

func TestResolveLongMemEvalPreregisteredSelection(t *testing.T) {
	originalManifest := *flagLMEPreregisteredSelection
	originalSelectionOnly := *flagLMESelectionOnly
	originalQuestionID := *flagLMEQuestionID
	originalQuestionIDs := *flagLMEQuestionIDs
	originalExcluded := *flagLMEExcludeQuestionIDs
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
	*flagLMEQuestionTypes = ""
	*flagLMEPerType = 0
	*flagLMEAbstentionCount = 0
	*flagMaxTasks = 0
	instances := []*lmeInstance{
		{QuestionID: "q1", QuestionType: "single-session-user"},
		{QuestionID: "q2_abs", QuestionType: "temporal-reasoning"},
	}

	cases, selection, err := resolveLongMemEvalPreregisteredSelection(instances)
	if err != nil {
		t.Fatalf("resolve ordinary selection: %v", err)
	}
	if selection != nil || len(cases) != 1 || cases[0].QuestionID != "q1" {
		t.Fatalf("unexpected ordinary selection: cases=%v selection=%+v",
			questionIDs(cases), selection)
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
	cases, selection, err = resolveLongMemEvalPreregisteredSelection(instances)
	if err != nil {
		t.Fatalf("resolve preregistered selection: %v", err)
	}
	if selection == nil || selection.ManifestSHA256 == "" ||
		len(cases) != 2 || cases[0].QuestionID != "q2_abs" {
		t.Fatalf("unexpected preregistered selection: cases=%v selection=%+v",
			questionIDs(cases), selection)
	}

	*flagLMEPerType = 1
	if _, _, err := resolveLongMemEvalPreregisteredSelection(instances); err == nil ||
		!strings.Contains(err.Error(), "-lme-per-type") {
		t.Fatalf("conflicting flags error = %v", err)
	}
	*flagLMEPerType = 0
	*flagLMEPreregisteredSelection = filepath.Join(t.TempDir(), "missing.json")
	if _, _, err := resolveLongMemEvalPreregisteredSelection(instances); err == nil ||
		!strings.Contains(err.Error(), "open LongMemEval selection manifest") {
		t.Fatalf("missing manifest error = %v", err)
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
	originalExcluded := *flagLMEExcludeQuestionIDs
	t.Cleanup(func() { *flagLMEExcludeQuestionIDs = originalExcluded })
	*flagLMEExcludeQuestionIDs = "excluded-b,excluded-a"

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
		"dataset-digest",
		"selection-digest",
		manifest.ProtocolSHA256,
		manifest.Build,
	); err != nil {
		t.Fatalf("validate selection: %v", err)
	}
	if err := validateLongMemEvalPreregisteredSelection(
		nil, nil, "", "", "", lmeBuildProvenance{},
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
			name: "schema drift",
			mutate: func(m *lmeSelectionManifest, _ *lmeBuildProvenance) {
				m.SchemaVersion++
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
			*flagLMEExcludeQuestionIDs = test.exclusions
			if test.exclusions == "" {
				*flagLMEExcludeQuestionIDs = "excluded-b,excluded-a"
			}
			err := validateLongMemEvalPreregisteredSelection(
				&lmePreregisteredSelection{Manifest: candidate},
				instances,
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
	*flagLMEExcludeQuestionIDs = "q1"
	if err := validateLongMemEvalPreregisteredSelection(
		&lmePreregisteredSelection{Manifest: selectedExcluded},
		instances,
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
	protocol := lmeProtocolProvenance{Version: lmeProtocolVersion}
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
