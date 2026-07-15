//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package catalog defines the deferred-tool catalog exercised by the toolsearch
// benchmark. It mirrors the self-defined namespace catalog used by the
// plugin's integration accuracy test (plugin/toolsearch/accuracy_test.go): a set
// of business namespaces (filesystem, git, document, process, network, iam, crm),
// a block of general-purpose no-namespace tools, and a small always-on preset.
//
// Each toolbox is defined in its own file with typed input parameters per tool.
// Execution is stubbed (a canned JSON reply), so no tool makes a real call
// during the benchmark. The tool-trajectory metric only checks WHICH tools the
// model chose to call, not their results, so stubs are sufficient and keep the
// only network traffic to the model completions themselves.
package toolboxs

import (
	"trpc.group/trpc-go/trpc-agent-go/plugin/toolsearch"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Toolboxes returns the deferred-tool namespaces under test. Each toolbox is
// defined in its own file with per-tool typed input schemas and detailed
// descriptions. The tools are intentionally distinct in capability but share
// generic verbs (read, list, search, get, create, delete, update) across
// namespaces, so namespace scoping is exercised — e.g. delete_user (iam) vs
// delete_customer (crm).
func Toolboxes() []toolsearch.Toolbox {
	return []toolsearch.Toolbox{
		FilesystemToolbox(),
		GitToolbox(),
		DocumentToolbox(),
		ProcessToolbox(),
		NetworkToolbox(),
		IamToolbox(),
		CrmToolbox(),
	}
}

// AllTools returns every tool in the catalog — preset, default, and all toolbox
// tools — flattened into a single slice. The `none` (baseline) mode hands this
// entire set directly to the agent, with no tool search.
func AllTools() []tool.Tool {
	var all []tool.Tool
	all = append(all, PresetTools()...)
	all = append(all, DefaultTools()...)
	for _, box := range Toolboxes() {
		all = append(all, box.Tools...)
	}
	return all
}
