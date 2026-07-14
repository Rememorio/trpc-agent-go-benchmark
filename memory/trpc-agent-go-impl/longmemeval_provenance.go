//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import "runtime/debug"

const (
	lmeAgentModulePath    = "trpc.group/trpc-go/trpc-agent-go"
	lmePGVectorModulePath = "trpc.group/trpc-go/trpc-agent-go/memory/pgvector"
)

type lmeBuildProvenance struct {
	GoVersion string                         `json:"go_version,omitempty"`
	Revision  string                         `json:"benchmark_revision,omitempty"`
	Modified  bool                           `json:"benchmark_modified"`
	Modules   map[string]lmeModuleProvenance `json:"modules,omitempty"`
}

type lmeModuleProvenance struct {
	Version            string `json:"version,omitempty"`
	ReplacementPath    string `json:"replacement_path,omitempty"`
	ReplacementVersion string `json:"replacement_version,omitempty"`
	LocalReplacement   bool   `json:"local_replacement,omitempty"`
}

func currentLongMemEvalBuildProvenance() lmeBuildProvenance {
	info, ok := debug.ReadBuildInfo()
	return longMemEvalBuildProvenance(info, ok)
}

func longMemEvalBuildProvenance(info *debug.BuildInfo, ok bool) lmeBuildProvenance {
	if !ok || info == nil {
		return lmeBuildProvenance{}
	}
	result := lmeBuildProvenance{
		GoVersion: info.GoVersion,
		Modules:   make(map[string]lmeModuleProvenance),
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			result.Revision = setting.Value
		case "vcs.modified":
			result.Modified = setting.Value == "true"
		}
	}
	for _, module := range info.Deps {
		if module == nil || (module.Path != lmeAgentModulePath && module.Path != lmePGVectorModulePath) {
			continue
		}
		provenance := lmeModuleProvenance{Version: module.Version}
		if module.Replace != nil {
			if module.Replace.Version == "" {
				provenance.LocalReplacement = true
			} else {
				provenance.ReplacementPath = module.Replace.Path
				provenance.ReplacementVersion = module.Replace.Version
			}
		}
		result.Modules[module.Path] = provenance
	}
	if len(result.Modules) == 0 {
		result.Modules = nil
	}
	return result
}
