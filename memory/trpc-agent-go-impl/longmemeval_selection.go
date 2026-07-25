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
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

const lmeSelectionManifestSchemaVersion = 2

type lmePreregisteredSelection struct {
	Manifest       lmeSelectionManifest
	ManifestSHA256 string
}

type lmeResolvedSelection struct {
	Cases               []*lmeInstance
	Preregistered       *lmePreregisteredSelection
	ExcludedQuestionIDs []string
}

func resolveLongMemEvalSelection(
	instances []*lmeInstance,
) (lmeResolvedSelection, error) {
	path := strings.TrimSpace(*flagLMEPreregisteredSelection)
	if path != "" {
		if err := validateLongMemEvalPreregisteredSelectionFlags(); err != nil {
			return lmeResolvedSelection{}, err
		}
	}
	excluded, err := loadLongMemEvalExcludedQuestionIDs(instances)
	if err != nil {
		return lmeResolvedSelection{}, err
	}
	if path == "" {
		return lmeResolvedSelection{
			Cases:               filterCases(instances, excluded),
			ExcludedQuestionIDs: excluded,
		}, nil
	}
	manifest, digest, err := loadLongMemEvalSelectionManifest(path)
	if err != nil {
		return lmeResolvedSelection{}, err
	}
	cases, err := longMemEvalCasesFromSelection(instances, manifest)
	if err != nil {
		return lmeResolvedSelection{}, err
	}
	return lmeResolvedSelection{
		Cases: cases,
		Preregistered: &lmePreregisteredSelection{
			Manifest:       manifest,
			ManifestSHA256: digest,
		},
		ExcludedQuestionIDs: excluded,
	}, nil
}

func loadLongMemEvalExcludedQuestionIDs(
	instances []*lmeInstance,
) ([]string, error) {
	ids := parseCommaList(*flagLMEExcludeQuestionIDs)
	path := strings.TrimSpace(*flagLMEExcludeQuestionIDsFile)
	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf(
				"open LongMemEval question ID exclusion file: %w", err,
			)
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			if id := strings.TrimSpace(scanner.Text()); id != "" {
				ids = append(ids, id)
			}
		}
		scanErr := scanner.Err()
		closeErr := file.Close()
		if scanErr != nil {
			return nil, fmt.Errorf(
				"read LongMemEval question ID exclusion file: %w", scanErr,
			)
		}
		if closeErr != nil {
			return nil, fmt.Errorf(
				"close LongMemEval question ID exclusion file: %w", closeErr,
			)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}

	datasetIDs := make(map[string]struct{}, len(instances))
	for _, instance := range instances {
		if instance == nil {
			continue
		}
		datasetIDs[instance.QuestionID] = struct{}{}
	}
	unique := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, exists := datasetIDs[id]; !exists {
			return nil, fmt.Errorf(
				"excluded LongMemEval question_id %q is absent from the dataset", id,
			)
		}
		unique[id] = struct{}{}
	}
	result := make([]string, 0, len(unique))
	for id := range unique {
		result = append(result, id)
	}
	sort.Strings(result)
	return result, nil
}

func validateLongMemEvalPreregisteredSelectionFlags() error {
	conflicts := make([]string, 0, 5)
	if strings.TrimSpace(*flagLMEQuestionID) != "" {
		conflicts = append(conflicts, "-lme-question-id")
	}
	if strings.TrimSpace(*flagLMEQuestionIDs) != "" {
		conflicts = append(conflicts, "-lme-question-ids")
	}
	if strings.TrimSpace(*flagLMEQuestionTypes) != "" {
		conflicts = append(conflicts, "-lme-question-types")
	}
	if *flagLMEPerType != 0 {
		conflicts = append(conflicts, "-lme-per-type")
	}
	if *flagLMEAbstentionCount != 0 {
		conflicts = append(conflicts, "-lme-abstention-count")
	}
	if *flagMaxTasks != 0 {
		conflicts = append(conflicts, "-max-tasks")
	}
	if len(conflicts) > 0 {
		return fmt.Errorf(
			"preregistered LongMemEval selection cannot be combined with %s",
			strings.Join(conflicts, ", "),
		)
	}
	return nil
}

func loadLongMemEvalSelectionManifest(
	path string,
) (lmeSelectionManifest, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return lmeSelectionManifest{}, "", fmt.Errorf(
			"open LongMemEval selection manifest: %w", err,
		)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var manifest lmeSelectionManifest
	if err := decoder.Decode(&manifest); err != nil {
		return lmeSelectionManifest{}, "", fmt.Errorf(
			"decode LongMemEval selection manifest: %w", err,
		)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return lmeSelectionManifest{}, "", errors.New(
				"decode LongMemEval selection manifest: multiple JSON values",
			)
		}
		return lmeSelectionManifest{}, "", fmt.Errorf(
			"decode trailing LongMemEval selection data: %w", err,
		)
	}
	digest, err := longMemEvalFileSHA256(path)
	if err != nil {
		return lmeSelectionManifest{}, "", fmt.Errorf(
			"hash LongMemEval selection manifest: %w", err,
		)
	}
	return manifest, digest, nil
}

func longMemEvalCasesFromSelection(
	instances []*lmeInstance,
	manifest lmeSelectionManifest,
) ([]*lmeInstance, error) {
	if len(manifest.Cases) == 0 {
		return nil, errors.New("LongMemEval selection manifest has no cases")
	}
	byID := make(map[string]*lmeInstance, len(instances))
	for _, instance := range instances {
		if instance == nil {
			continue
		}
		id := strings.TrimSpace(instance.QuestionID)
		if id == "" {
			return nil, errors.New("LongMemEval dataset contains an empty question_id")
		}
		if _, exists := byID[id]; exists {
			return nil, fmt.Errorf(
				"LongMemEval dataset contains duplicate question_id %q", id,
			)
		}
		byID[id] = instance
	}

	selected := make([]*lmeInstance, 0, len(manifest.Cases))
	seen := make(map[string]struct{}, len(manifest.Cases))
	for _, selectedCase := range manifest.Cases {
		id := strings.TrimSpace(selectedCase.QuestionID)
		if id == "" {
			return nil, errors.New(
				"LongMemEval selection manifest contains an empty question_id",
			)
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf(
				"LongMemEval selection manifest contains duplicate question_id %q",
				id,
			)
		}
		seen[id] = struct{}{}
		instance, exists := byID[id]
		if !exists {
			return nil, fmt.Errorf(
				"LongMemEval selection question_id %q is absent from the dataset",
				id,
			)
		}
		if instance.QuestionType != selectedCase.QuestionType {
			return nil, fmt.Errorf(
				"LongMemEval selection question_id %q type is %q, dataset has %q",
				id, selectedCase.QuestionType, instance.QuestionType,
			)
		}
		if abstention := isAbstentionQuestion(instance); abstention != selectedCase.Abstention {
			return nil, fmt.Errorf(
				"LongMemEval selection question_id %q abstention is %t, dataset has %t",
				id, selectedCase.Abstention, abstention,
			)
		}
		selected = append(selected, instance)
	}
	return selected, nil
}

func validateLongMemEvalPreregisteredSelection(
	selection *lmePreregisteredSelection,
	cases []*lmeInstance,
	excluded []string,
	datasetDigest string,
	selectionDigest string,
	protocolDigest string,
	currentBuild lmeBuildProvenance,
) error {
	if selection == nil {
		return nil
	}
	manifest := selection.Manifest
	if manifest.SchemaVersion != lmeSelectionManifestSchemaVersion {
		return fmt.Errorf(
			"LongMemEval preregistration schema version is %d, current version is %d",
			manifest.SchemaVersion, lmeSelectionManifestSchemaVersion,
		)
	}
	if manifest.DatasetSHA256 != datasetDigest {
		return fmt.Errorf(
			"LongMemEval preregistration dataset digest is %q, current dataset is %q",
			manifest.DatasetSHA256, datasetDigest,
		)
	}
	if manifest.SelectionSHA256 != selectionDigest {
		return fmt.Errorf(
			"LongMemEval preregistration selection digest is %q, current selection is %q",
			manifest.SelectionSHA256, selectionDigest,
		)
	}
	if manifest.ProtocolVersion != lmeProtocolVersion {
		return fmt.Errorf(
			"LongMemEval preregistration protocol version is %q, current version is %q",
			manifest.ProtocolVersion, lmeProtocolVersion,
		)
	}
	if manifest.Protocol.Version != manifest.ProtocolVersion {
		return fmt.Errorf(
			"LongMemEval preregistration protocol payload version is %q, declared version is %q",
			manifest.Protocol.Version, manifest.ProtocolVersion,
		)
	}
	if err := validateLongMemEvalProtocol(manifest.Protocol); err != nil {
		return fmt.Errorf("invalid LongMemEval preregistration protocol: %w", err)
	}
	manifestProtocolDigest, err := longMemEvalJSONSHA256(manifest.Protocol)
	if err != nil {
		return fmt.Errorf("hash LongMemEval preregistration protocol: %w", err)
	}
	if manifest.ProtocolSHA256 != manifestProtocolDigest {
		return fmt.Errorf(
			"LongMemEval preregistration protocol payload digest is %q, declared digest is %q",
			manifestProtocolDigest, manifest.ProtocolSHA256,
		)
	}
	if manifest.ProtocolSHA256 != protocolDigest {
		return fmt.Errorf(
			"LongMemEval preregistration protocol digest is %q, current protocol is %q",
			manifest.ProtocolSHA256, protocolDigest,
		)
	}
	if manifest.SamplePerType < 0 || manifest.AbstentionCount < 0 ||
		manifest.ExcludedCount < 0 {
		return errors.New(
			"LongMemEval preregistration contains negative sampling metadata",
		)
	}
	excludedDigest, err := longMemEvalJSONSHA256(excluded)
	if err != nil {
		return fmt.Errorf("hash excluded LongMemEval question IDs: %w", err)
	}
	if manifest.ExcludedCount != len(excluded) ||
		manifest.ExcludedSHA256 != excludedDigest {
		return errors.New(
			"LongMemEval preregistration exclusion set does not match the current exclusion set",
		)
	}
	excludedSet := make(map[string]struct{}, len(excluded))
	for _, id := range excluded {
		excludedSet[id] = struct{}{}
	}
	for _, instance := range cases {
		if instance == nil {
			continue
		}
		if _, exists := excludedSet[instance.QuestionID]; exists {
			return fmt.Errorf(
				"LongMemEval preregistration selected excluded question_id %q",
				instance.QuestionID,
			)
		}
	}
	if issue := longMemEvalBuildProvenanceIssue(manifest.Build); issue != "" {
		return fmt.Errorf(
			"LongMemEval preregistration build is not suitable for strict comparison: %s",
			issue,
		)
	}
	if issue := longMemEvalBuildProvenanceIssue(currentBuild); issue != "" {
		return fmt.Errorf(
			"current LongMemEval build is not suitable for strict comparison: %s",
			issue,
		)
	}
	if manifest.Build.Revision != currentBuild.Revision {
		return fmt.Errorf(
			"LongMemEval preregistration benchmark revision is %q, current revision is %q",
			manifest.Build.Revision, currentBuild.Revision,
		)
	}
	return nil
}

func (selection *lmePreregisteredSelection) metadata() map[string]any {
	if selection == nil {
		return nil
	}
	manifest := selection.Manifest
	return map[string]any{
		"schema_version":               manifest.SchemaVersion,
		"manifest_sha256":              selection.ManifestSHA256,
		"benchmark_revision":           manifest.Build.Revision,
		"dataset_sha256":               manifest.DatasetSHA256,
		"selection_sha256":             manifest.SelectionSHA256,
		"protocol_version":             manifest.ProtocolVersion,
		"protocol_sha256":              manifest.ProtocolSHA256,
		"protocol":                     manifest.Protocol,
		"sample_per_type":              manifest.SamplePerType,
		"sample_abstention_count":      manifest.AbstentionCount,
		"sample_seed":                  manifest.SampleSeed,
		"excluded_question_id_count":   manifest.ExcludedCount,
		"excluded_question_ids_sha256": manifest.ExcludedSHA256,
		"case_count":                   len(manifest.Cases),
	}
}
