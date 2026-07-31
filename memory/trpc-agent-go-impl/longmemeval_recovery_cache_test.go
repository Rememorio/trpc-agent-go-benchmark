package main

import "testing"

func TestLongMemEvalRecoveryCacheLineageMergesSequentialSegments(
	t *testing.T,
) {
	source := make(map[string]any)
	first := make(map[string]any)
	second := make(map[string]any)
	for _, prefix := range []string{
		"answer_cache",
		"model_response_cache",
		"embedding_response_cache",
	} {
		setRecoveryTestCachePrefix(source, prefix, 0, 2, 1, 2)
		setRecoveryTestCachePrefix(first, prefix, 2, 3, 2, 1)
		setRecoveryTestCachePrefix(second, prefix, 3, 5, 3, 2)
	}

	lineage, err := newLongMemEvalRecoveryCacheLineage(source, "source")
	if err != nil {
		t.Fatal(err)
	}
	if err := appendLongMemEvalRecoveryCacheLineage(
		source, first, "first", lineage,
	); err != nil {
		t.Fatal(err)
	}
	if err := appendLongMemEvalRecoveryCacheLineage(
		source, second, "second", lineage,
	); err != nil {
		t.Fatal(err)
	}

	for _, spec := range longMemEvalRecoveryCacheSpecs {
		prefix := spec.Prefix
		if source[prefix+"_initial_entries"] != 0 ||
			source[prefix+"_final_entries"] != 5 ||
			source[prefix+"_hits"] != 6 ||
			source[prefix+"_misses"] != 5 {
			t.Fatalf(
				"unexpected merged %s metadata: %#v",
				prefix,
				source,
			)
		}
		got := lineage[prefix]
		if got.LedgerID != prefix+"-ledger" ||
			len(got.Segments) != 3 ||
			got.Segments[0].ResultsSHA256 != "source" ||
			got.Segments[1].ResultsSHA256 != "first" ||
			got.Segments[2].ResultsSHA256 != "second" {
			t.Fatalf("unexpected %s lineage: %#v", prefix, got)
		}
	}
}

func setRecoveryTestCachePrefix(
	metadata map[string]any,
	prefix string,
	initial,
	final,
	hits,
	misses int,
) {
	metadata[prefix+"_format_version"] = "test-cache-v1"
	metadata[prefix+"_shared"] = true
	metadata[prefix+"_ledger_id"] = prefix + "-ledger"
	metadata[prefix+"_initial_entries"] = initial
	metadata[prefix+"_final_entries"] = final
	metadata[prefix+"_hits"] = hits
	metadata[prefix+"_misses"] = misses
	metadata[prefix+"_require_hit"] = false
	if prefix == "answer_cache" {
		metadata[prefix+"_logical_usage_hits"] = hits
		metadata[prefix+"_logical_usage_missing_hits"] = 0
		return
	}
	metadata[prefix+"_errors"] = 0
}
