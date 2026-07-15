//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package optimization provides the reflective search pipeline used by the
// SkillCraft benchmark runner.
package optimization

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evolution"
	framework "trpc.group/trpc-go/trpc-agent-go/evolution/optimization"
)

const datasetID = "skillcraft-reflective-optimization"

// Config controls one reflective search or frozen comparison experiment.
type Config struct {
	SeedSpecPath          string
	CandidateSpecPath     string
	FeedbackScales        []string
	ValidationScales      []string
	HoldoutScales         []string
	CriticalScales        []string
	Repeats               int
	MaxIterations         int
	ReflectionBatch       int
	MaxMetricCalls        int
	RandomSeed            int64
	EvaluationTemperature float64
	TimeLimit             time.Duration
	TokenBudget           int
}

// Validate checks search invariants that do not depend on the task catalog.
func (c Config) Validate() error {
	if c.Repeats <= 0 {
		return errors.New("repeats must be positive")
	}
	if c.MaxIterations < 0 {
		return errors.New("max iterations must not be negative")
	}
	if c.ReflectionBatch <= 0 {
		return errors.New("reflection batch size must be positive")
	}
	if c.MaxMetricCalls < 0 {
		return errors.New("max metric calls must not be negative")
	}
	if math.IsNaN(c.EvaluationTemperature) ||
		math.IsInf(c.EvaluationTemperature, 0) ||
		c.EvaluationTemperature < 0 || c.EvaluationTemperature > 2 {
		return errors.New("evaluation temperature must be between 0 and 2")
	}
	if c.TokenBudget <= 0 {
		return errors.New("token budget must be positive")
	}
	if len(c.FeedbackScales) == 0 || len(c.ValidationScales) == 0 {
		return errors.New("feedback and validation scale splits must be non-empty")
	}
	if c.CandidateSpecPath != "" && len(c.HoldoutScales) == 0 {
		return errors.New("frozen candidate comparison requires holdout scales")
	}
	seen := make(map[string]string)
	for split, scales := range map[string][]string{
		"feedback":   c.FeedbackScales,
		"validation": c.ValidationScales,
		"holdout":    c.HoldoutScales,
	} {
		for _, scale := range scales {
			if previous, ok := seen[scale]; ok {
				return fmt.Errorf("scale %q appears in both %s and %s", scale, previous, split)
			}
			seen[scale] = split
		}
	}
	holdout := make(map[string]bool, len(c.HoldoutScales))
	for _, scale := range c.HoldoutScales {
		holdout[scale] = true
	}
	for _, scale := range c.CriticalScales {
		if !holdout[scale] {
			return fmt.Errorf("critical scale %q is not in the holdout split", scale)
		}
	}
	return nil
}

// Task is the runtime-neutral portion of one benchmark case needed to
// construct feedback, validation, and holdout datasets.
type Task struct {
	ID          string
	Family      string
	Scale       string
	Input       string
	Expectation string
}

// BuildDataset converts one scaled task family into disjoint optimizer splits.
func BuildDataset(tasks []Task, cfg Config) (framework.Dataset, error) {
	if len(tasks) == 0 {
		return framework.Dataset{}, errors.New("at least one task is required")
	}
	family := tasks[0].Family
	tasksByScale := make(map[string]Task, len(tasks))
	for _, task := range tasks {
		if task.Family != family {
			return framework.Dataset{}, fmt.Errorf(
				"task %q belongs to %q, expected one family %q",
				task.ID, task.Family, family,
			)
		}
		if _, duplicate := tasksByScale[task.Scale]; duplicate {
			return framework.Dataset{}, fmt.Errorf("duplicate scale %q", task.Scale)
		}
		tasksByScale[task.Scale] = task
	}
	criticalScales := make(map[string]bool, len(cfg.CriticalScales))
	for _, scale := range cfg.CriticalScales {
		criticalScales[scale] = true
	}
	makeCases := func(split string, scales []string) ([]framework.Case, error) {
		cases := make([]framework.Case, 0, len(scales)*cfg.Repeats)
		for _, scale := range scales {
			task, ok := tasksByScale[scale]
			if !ok {
				return nil, fmt.Errorf("%s scale %q was not selected", split, scale)
			}
			for repeat := 1; repeat <= cfg.Repeats; repeat++ {
				cases = append(cases, framework.Case{
					ID:       fmt.Sprintf("%s/%s/r%d", split, task.ID, repeat),
					Input:    task.Input,
					Expected: task.Expectation,
					Critical: split == "holdout" && criticalScales[scale],
					Metadata: map[string]string{
						"task_id": task.ID,
						"repeat":  strconv.Itoa(repeat),
						"split":   split,
					},
				})
			}
		}
		return cases, nil
	}
	feedback, err := makeCases("feedback", cfg.FeedbackScales)
	if err != nil {
		return framework.Dataset{}, err
	}
	validation, err := makeCases("validation", cfg.ValidationScales)
	if err != nil {
		return framework.Dataset{}, err
	}
	holdout, err := makeCases("holdout", cfg.HoldoutScales)
	if err != nil {
		return framework.Dataset{}, err
	}
	return framework.Dataset{
		ID:         datasetID + "/" + family,
		Version:    "skillcraft-scaled-v1",
		Feedback:   feedback,
		Validation: validation,
		Holdout:    holdout,
	}, nil
}

// LoadSpec decodes one strict SkillSpec JSON document or an immutable
// evolution Revision document containing a spec. Accepting revision metadata
// lets experiments retain the exact provenance of reviewer-generated seeds.
func LoadSpec(path string) (*evolution.SkillSpec, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("open skill spec: %w", err)
	}
	var probe map[string]json.RawMessage
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&probe); err != nil {
		return nil, fmt.Errorf("decode skill spec: %w", err)
	}
	if _, revision := probe["spec"]; revision {
		var envelope evolution.Revision
		if err := decodeStrictJSON(raw, &envelope); err != nil {
			return nil, fmt.Errorf("decode skill revision: %w", err)
		}
		if envelope.Spec == nil {
			return nil, errors.New("skill revision has no spec")
		}
		return envelope.Spec, nil
	}
	var spec evolution.SkillSpec
	if err := decodeStrictJSON(raw, &spec); err != nil {
		return nil, fmt.Errorf("decode skill spec: %w", err)
	}
	return &spec, nil
}

func decodeStrictJSON(raw []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("contains trailing JSON values")
		}
		return fmt.Errorf("decode trailer: %w", err)
	}
	return nil
}
