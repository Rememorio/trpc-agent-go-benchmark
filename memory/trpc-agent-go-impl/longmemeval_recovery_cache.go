package main

import (
	"fmt"
	"slices"
	"strings"
)

const longMemEvalRecoveryCacheCounterNote = "Merged cache counters describe " +
	"physical ledger operations, including superseded attempts; canonical " +
	"provider cost remains case-trace-derived."

type longMemEvalRecoveryCacheLineage struct {
	LedgerID string                                `json:"ledger_id"`
	Segments []longMemEvalRecoveryCacheLineagePart `json:"segments"`
}

type longMemEvalRecoveryCacheLineagePart struct {
	ResultsSHA256           string `json:"results_sha256"`
	InitialEntries          int    `json:"initial_entries"`
	FinalEntries            int    `json:"final_entries"`
	Hits                    int    `json:"hits"`
	Misses                  int    `json:"misses"`
	Errors                  *int   `json:"errors,omitempty"`
	LogicalUsageHits        *int   `json:"logical_usage_hits,omitempty"`
	LogicalUsageMissingHits *int   `json:"logical_usage_missing_hits,omitempty"`
}

type longMemEvalRecoveryCacheSpec struct {
	Prefix          string
	OptionalSums    []string
	RequiredSums    []string
	RequiredEntries []string
}

var longMemEvalRecoveryCacheSpecs = []longMemEvalRecoveryCacheSpec{
	{
		Prefix:          "answer_cache",
		OptionalSums:    []string{"logical_usage_hits", "logical_usage_missing_hits"},
		RequiredSums:    []string{"hits", "misses"},
		RequiredEntries: []string{"initial_entries", "final_entries"},
	},
	{
		Prefix:          "model_response_cache",
		OptionalSums:    []string{"errors"},
		RequiredSums:    []string{"hits", "misses"},
		RequiredEntries: []string{"initial_entries", "final_entries"},
	},
	{
		Prefix:          "embedding_response_cache",
		OptionalSums:    []string{"errors"},
		RequiredSums:    []string{"hits", "misses"},
		RequiredEntries: []string{"initial_entries", "final_entries"},
	},
}

func longMemEvalRecoveryCacheCounterSemantics(
	lineage map[string]longMemEvalRecoveryCacheLineage,
) string {
	if len(lineage) == 0 {
		return ""
	}
	return longMemEvalRecoveryCacheCounterNote
}

func newLongMemEvalRecoveryCacheLineage(
	metadata map[string]any,
	resultsSHA string,
) (map[string]longMemEvalRecoveryCacheLineage, error) {
	lineage := make(map[string]longMemEvalRecoveryCacheLineage)
	for _, spec := range longMemEvalRecoveryCacheSpecs {
		if !longMemEvalRecoveryCacheMetadataPresent(metadata, spec.Prefix) {
			continue
		}
		part, err := longMemEvalRecoveryCacheLineagePartFromMetadata(
			metadata, spec, resultsSHA,
		)
		if err != nil {
			return nil, err
		}
		ledgerID, err := longMemEvalRecoveryCacheString(
			metadata, spec.Prefix+"_ledger_id",
		)
		if err != nil {
			return nil, err
		}
		lineage[spec.Prefix] = longMemEvalRecoveryCacheLineage{
			LedgerID: ledgerID,
			Segments: []longMemEvalRecoveryCacheLineagePart{part},
		}
	}
	return lineage, nil
}

func appendLongMemEvalRecoveryCacheLineage(
	mergedMetadata,
	replacementMetadata map[string]any,
	replacementSHA string,
	lineage map[string]longMemEvalRecoveryCacheLineage,
) error {
	for _, spec := range longMemEvalRecoveryCacheSpecs {
		mergedPresent := longMemEvalRecoveryCacheMetadataPresent(
			mergedMetadata, spec.Prefix,
		)
		replacementPresent := longMemEvalRecoveryCacheMetadataPresent(
			replacementMetadata, spec.Prefix,
		)
		if mergedPresent != replacementPresent {
			return fmt.Errorf(
				"%s presence does not match recovery source",
				strings.ReplaceAll(spec.Prefix, "_", " "),
			)
		}
		if !mergedPresent {
			continue
		}
		if err := validateLongMemEvalRecoveryCacheSettings(
			mergedMetadata, replacementMetadata, spec,
		); err != nil {
			return err
		}
		currentFinal, err := longMemEvalRecoveryCacheInt(
			mergedMetadata, spec.Prefix+"_final_entries",
		)
		if err != nil {
			return err
		}
		replacementPart, err :=
			longMemEvalRecoveryCacheLineagePartFromMetadata(
				replacementMetadata, spec, replacementSHA,
			)
		if err != nil {
			return err
		}
		if replacementPart.InitialEntries != currentFinal {
			return fmt.Errorf(
				"%s cache timeline is discontinuous: replacement "+
					"initial_entries=%d, previous final_entries=%d",
				strings.ReplaceAll(spec.Prefix, "_", " "),
				replacementPart.InitialEntries,
				currentFinal,
			)
		}
		mergedMetadata[spec.Prefix+"_final_entries"] =
			replacementPart.FinalEntries
		for _, suffix := range append(
			append([]string(nil), spec.RequiredSums...),
			spec.OptionalSums...,
		) {
			key := spec.Prefix + "_" + suffix
			replacementValue, replacementOK :=
				longMemEvalMetadataInt(replacementMetadata[key])
			if !replacementOK {
				if slices.Contains(spec.OptionalSums, suffix) {
					continue
				}
				return fmt.Errorf(
					"%s metadata %q is missing or not an integer",
					strings.ReplaceAll(spec.Prefix, "_", " "),
					key,
				)
			}
			mergedValue, mergedOK :=
				longMemEvalMetadataInt(mergedMetadata[key])
			if !mergedOK {
				return fmt.Errorf(
					"%s metadata %q is missing or not an integer",
					strings.ReplaceAll(spec.Prefix, "_", " "),
					key,
				)
			}
			mergedMetadata[key] = mergedValue + replacementValue
		}
		cacheLineage := lineage[spec.Prefix]
		cacheLineage.Segments = append(
			cacheLineage.Segments, replacementPart,
		)
		lineage[spec.Prefix] = cacheLineage
	}
	return nil
}

func validateLongMemEvalRecoveryCacheSettings(
	mergedMetadata,
	replacementMetadata map[string]any,
	spec longMemEvalRecoveryCacheSpec,
) error {
	mutable := make(map[string]bool)
	for _, suffix := range append(
		append(
			append([]string(nil), spec.RequiredEntries...),
			spec.RequiredSums...,
		),
		spec.OptionalSums...,
	) {
		mutable[spec.Prefix+"_"+suffix] = true
	}
	keys := make(map[string]bool)
	for key := range mergedMetadata {
		if strings.HasPrefix(key, spec.Prefix+"_") && !mutable[key] {
			keys[key] = true
		}
	}
	for key := range replacementMetadata {
		if strings.HasPrefix(key, spec.Prefix+"_") && !mutable[key] {
			keys[key] = true
		}
	}
	for key := range keys {
		mergedValue, mergedOK := mergedMetadata[key]
		replacementValue, replacementOK := replacementMetadata[key]
		if !mergedOK || !replacementOK ||
			!jsonValuesEqual(mergedValue, replacementValue) {
			return fmt.Errorf(
				"%s metadata %q does not match recovery source",
				strings.ReplaceAll(spec.Prefix, "_", " "),
				key,
			)
		}
	}
	for _, suffix := range spec.OptionalSums {
		key := spec.Prefix + "_" + suffix
		_, mergedOK := mergedMetadata[key]
		_, replacementOK := replacementMetadata[key]
		if mergedOK != replacementOK {
			return fmt.Errorf(
				"%s metadata %q presence does not match recovery source",
				strings.ReplaceAll(spec.Prefix, "_", " "),
				key,
			)
		}
	}
	return nil
}

func longMemEvalRecoveryCacheLineagePartFromMetadata(
	metadata map[string]any,
	spec longMemEvalRecoveryCacheSpec,
	resultsSHA string,
) (longMemEvalRecoveryCacheLineagePart, error) {
	value := func(suffix string) (int, error) {
		return longMemEvalRecoveryCacheInt(
			metadata, spec.Prefix+"_"+suffix,
		)
	}
	initial, err := value("initial_entries")
	if err != nil {
		return longMemEvalRecoveryCacheLineagePart{}, err
	}
	final, err := value("final_entries")
	if err != nil {
		return longMemEvalRecoveryCacheLineagePart{}, err
	}
	hits, err := value("hits")
	if err != nil {
		return longMemEvalRecoveryCacheLineagePart{}, err
	}
	misses, err := value("misses")
	if err != nil {
		return longMemEvalRecoveryCacheLineagePart{}, err
	}
	if final < initial {
		return longMemEvalRecoveryCacheLineagePart{}, fmt.Errorf(
			"%s final_entries=%d is less than initial_entries=%d",
			strings.ReplaceAll(spec.Prefix, "_", " "),
			final,
			initial,
		)
	}
	part := longMemEvalRecoveryCacheLineagePart{
		ResultsSHA256:  resultsSHA,
		InitialEntries: initial,
		FinalEntries:   final,
		Hits:           hits,
		Misses:         misses,
	}
	for _, suffix := range spec.OptionalSums {
		key := spec.Prefix + "_" + suffix
		if _, ok := metadata[key]; !ok {
			continue
		}
		counter, err := longMemEvalRecoveryCacheInt(metadata, key)
		if err != nil {
			return longMemEvalRecoveryCacheLineagePart{}, err
		}
		switch suffix {
		case "errors":
			part.Errors = &counter
		case "logical_usage_hits":
			part.LogicalUsageHits = &counter
		case "logical_usage_missing_hits":
			part.LogicalUsageMissingHits = &counter
		}
	}
	return part, nil
}

func longMemEvalRecoveryCacheMetadataPresent(
	metadata map[string]any,
	prefix string,
) bool {
	for key := range metadata {
		if strings.HasPrefix(key, prefix+"_") {
			return true
		}
	}
	return false
}

func longMemEvalRecoveryCacheString(
	metadata map[string]any,
	key string,
) (string, error) {
	value, ok := lmeMetadataString(metadata, key)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("cache metadata %q is missing or empty", key)
	}
	return value, nil
}

func longMemEvalRecoveryCacheInt(
	metadata map[string]any,
	key string,
) (int, error) {
	value, ok := longMemEvalMetadataInt(metadata[key])
	if !ok || value < 0 {
		return 0, fmt.Errorf(
			"cache metadata %q is missing or not a non-negative integer",
			key,
		)
	}
	return value, nil
}
