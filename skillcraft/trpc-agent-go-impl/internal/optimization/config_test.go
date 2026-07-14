//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package optimization

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigValidate(t *testing.T) {
	valid := Config{
		FeedbackScales:   []string{"e1"},
		ValidationScales: []string{"e2"},
		HoldoutScales:    []string{"e3"},
		Repeats:          1,
		MaxIterations:    1,
		ReflectionBatch:  1,
		MaxMetricCalls:   10,
		TokenBudget:      1000,
	}
	require.NoError(t, valid.Validate())

	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"repeats", func(c *Config) { c.Repeats = 0 }, "repeats must be positive"},
		{"iterations", func(c *Config) { c.MaxIterations = -1 }, "iterations must not be negative"},
		{"batch", func(c *Config) { c.ReflectionBatch = 0 }, "batch size must be positive"},
		{"metric calls", func(c *Config) { c.MaxMetricCalls = -1 }, "metric calls must not be negative"},
		{"token budget", func(c *Config) { c.TokenBudget = 0 }, "token budget must be positive"},
		{"empty split", func(c *Config) { c.HoldoutScales = nil }, "splits must be non-empty"},
		{"overlap", func(c *Config) { c.HoldoutScales = []string{"e1"} }, "appears in both"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			test.mutate(&cfg)
			require.ErrorContains(t, cfg.Validate(), test.want)
		})
	}
}

func TestBuildDataset(t *testing.T) {
	tasks := []Task{
		{ID: "weather/e1", Family: "weather", Scale: "e1", Input: "one", Expectation: "first"},
		{ID: "weather/e2", Family: "weather", Scale: "e2", Input: "two", Expectation: "second"},
		{ID: "weather/e3", Family: "weather", Scale: "e3", Input: "three", Expectation: "third"},
	}
	cfg := Config{
		FeedbackScales:   []string{"e1"},
		ValidationScales: []string{"e2"},
		HoldoutScales:    []string{"e3"},
		Repeats:          2,
	}
	dataset, err := BuildDataset(tasks, cfg)
	require.NoError(t, err)
	require.Len(t, dataset.Feedback, 2)
	require.Len(t, dataset.Validation, 2)
	require.Len(t, dataset.Holdout, 2)
	require.Equal(t, "feedback/weather/e1/r1", dataset.Feedback[0].ID)
	require.Equal(t, "weather/e1", dataset.Feedback[0].Metadata["task_id"])
	require.True(t, dataset.Holdout[0].Critical)

	_, err = BuildDataset(nil, cfg)
	require.ErrorContains(t, err, "at least one task")
	_, err = BuildDataset([]Task{tasks[0], {
		ID: "recipe/e2", Family: "recipe", Scale: "e2",
	}}, cfg)
	require.ErrorContains(t, err, "expected one family")
	_, err = BuildDataset([]Task{tasks[0], {
		ID: "weather/copy", Family: "weather", Scale: "e1",
	}}, cfg)
	require.ErrorContains(t, err, "duplicate scale")
	_, err = BuildDataset(tasks[:1], cfg)
	require.ErrorContains(t, err, "was not selected")
}

func TestLoadSeed(t *testing.T) {
	dir := t.TempDir()
	validPath := filepath.Join(dir, "valid.json")
	spec := `{"name":"Weather","description":"desc","when_to_use":"when","steps":["step"]}`
	require.NoError(t, os.WriteFile(validPath, []byte(spec), 0o644))
	loaded, err := LoadSeed(validPath)
	require.NoError(t, err)
	require.Equal(t, "Weather", loaded.Name)

	trailingPath := filepath.Join(dir, "trailing.json")
	require.NoError(t, os.WriteFile(trailingPath, []byte(spec+` {}`), 0o644))
	_, err = LoadSeed(trailingPath)
	require.ErrorContains(t, err, "trailing JSON")

	unknownPath := filepath.Join(dir, "unknown.json")
	require.NoError(t, os.WriteFile(unknownPath, []byte(`{"unknown":true}`), 0o644))
	_, err = LoadSeed(unknownPath)
	require.ErrorContains(t, err, "unknown field")

	_, err = LoadSeed(filepath.Join(dir, "missing.json"))
	require.ErrorContains(t, err, "open seed spec")
}
