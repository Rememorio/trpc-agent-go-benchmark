//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"testing"
)

func TestLongMemEvalBuildImplementation(t *testing.T) {
	base := lmeBuildProvenance{
		Revision:             "benchmark-revision",
		BuildProfile:         "candidate",
		ModuleManifestSHA256: "manifest",
		ModuleSumSHA256:      "sum",
		Modules: map[string]lmeModuleProvenance{
			lmeAgentModulePath: {
				Version:            "v1.7.0",
				ReplacementVersion: "v0.0.0-candidate",
			},
			lmePGVectorModulePath: {
				Version:            "v1.7.0",
				ReplacementVersion: "v0.0.0-candidate",
			},
		},
	}

	if got, want := longMemEvalBuildImplementation(base),
		"candidate@v0.0.0-candidate"; got != want {
		t.Fatalf("build implementation = %q, want %q", got, want)
	}

	distinct := base
	distinct.Modules = map[string]lmeModuleProvenance{
		lmeAgentModulePath: {
			Version:            "v1.7.0",
			ReplacementVersion: "v0.0.0-agent",
		},
		lmePGVectorModulePath: {
			Version:            "v1.7.0",
			ReplacementVersion: "v0.0.0-pgvector",
		},
	}
	if got, want := longMemEvalBuildImplementation(distinct),
		"candidate@agent=v0.0.0-agent,pgvector=v0.0.0-pgvector"; got != want {
		t.Fatalf("distinct build implementation = %q, want %q", got, want)
	}

	invalid := base
	invalid.Modified = true
	if got := longMemEvalBuildImplementation(invalid); got != "" {
		t.Fatalf("invalid build implementation = %q", got)
	}
}

func TestLongMemEvalImplementationExplicitPrecedence(t *testing.T) {
	previous := *flagLMEImplementation
	t.Cleanup(func() {
		*flagLMEImplementation = previous
	})
	t.Setenv("LME_IMPLEMENTATION", "environment-label")

	*flagLMEImplementation = "flag-label"
	if got := longMemEvalImplementation(); got != "flag-label" {
		t.Fatalf("flag implementation = %q", got)
	}

	*flagLMEImplementation = ""
	if got := longMemEvalImplementation(); got != "environment-label" {
		t.Fatalf("environment implementation = %q", got)
	}
}
