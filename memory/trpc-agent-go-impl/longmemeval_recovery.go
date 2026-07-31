package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const longMemEvalRecoverySchemaVersion = 1

type longMemEvalRecoveryManifest struct {
	SchemaVersion            int                              `json:"schema_version"`
	Status                   string                           `json:"status"`
	RegisteredAt             string                           `json:"registered_at"`
	Reason                   string                           `json:"reason"`
	Source                   longMemEvalRecoverySource        `json:"source"`
	Replacements             []longMemEvalRecoveryReplacement `json:"replacements"`
	ExpectedUnits            []longMemEvalRecoveryUnit        `json:"expected_units"`
	QualityOutcomesInspected bool                             `json:"quality_outcomes_inspected"`
}

type longMemEvalRecoverySource struct {
	ResultsPath   string `json:"results_path"`
	ResultsSHA256 string `json:"results_sha256"`
}

type longMemEvalRecoveryReplacement struct {
	ResultsPath string `json:"results_path"`
}

type longMemEvalRecoveryUnit struct {
	QuestionIDSHA256 string `json:"question_id_sha256"`
	Backend          string `json:"backend"`
}

type longMemEvalRecoveryRecord struct {
	SchemaVersion            int                                        `json:"schema_version"`
	Status                   string                                     `json:"status"`
	RegisteredAt             string                                     `json:"registered_at"`
	Reason                   string                                     `json:"reason"`
	ManifestSHA256           string                                     `json:"manifest_sha256"`
	SourceResultsSHA256      string                                     `json:"source_results_sha256"`
	ReplacementResultsSHA256 []string                                   `json:"replacement_results_sha256"`
	ReplacedUnits            []longMemEvalRecoveryUnit                  `json:"replaced_units"`
	ReplacementCount         int                                        `json:"replacement_count"`
	QualityOutcomesInspected bool                                       `json:"quality_outcomes_inspected"`
	CacheLineage             map[string]longMemEvalRecoveryCacheLineage `json:"cache_lineage,omitempty"`
	CacheCounterSemantics    string                                     `json:"cache_counter_semantics,omitempty"`
	MergerBuild              lmeBuildProvenance                         `json:"merger_build"`
}

func applyLongMemEvalRecovery(manifestPath, outputDir string) error {
	manifestData, manifest, err := loadLongMemEvalRecoveryManifest(manifestPath)
	if err != nil {
		return err
	}
	manifestDir := filepath.Dir(manifestPath)
	sourcePath := resolveLongMemEvalRecoveryPath(
		manifestDir, manifest.Source.ResultsPath,
	)
	sourceSHA, err := sha256File(sourcePath)
	if err != nil {
		return fmt.Errorf("hash recovery source results: %w", err)
	}
	if sourceSHA != manifest.Source.ResultsSHA256 {
		return fmt.Errorf(
			"recovery source results hash mismatch: got %s, want %s",
			sourceSHA, manifest.Source.ResultsSHA256,
		)
	}
	result, err := loadLongMemEvalResults(sourcePath)
	if err != nil {
		return fmt.Errorf("load recovery source results: %w", err)
	}
	if err := validateLongMemEvalRecoverySource(result); err != nil {
		return err
	}
	cacheLineage, err := newLongMemEvalRecoveryCacheLineage(
		result.Metadata, sourceSHA,
	)
	if err != nil {
		return fmt.Errorf("validate recovery source cache metadata: %w", err)
	}

	expected, err := indexLongMemEvalRecoveryUnits(manifest.ExpectedUnits)
	if err != nil {
		return err
	}
	sourceCases, err := indexLongMemEvalRecoveryCases(result.Cases)
	if err != nil {
		return fmt.Errorf("index recovery source cases: %w", err)
	}
	replaced := make(map[string]bool, len(expected))
	replacementSHAs := make([]string, 0, len(manifest.Replacements))

	for _, replacement := range manifest.Replacements {
		replacementPath := resolveLongMemEvalRecoveryPath(
			manifestDir, replacement.ResultsPath,
		)
		replacementSHA, err := sha256File(replacementPath)
		if err != nil {
			return fmt.Errorf("hash replacement results: %w", err)
		}
		replacementSHAs = append(replacementSHAs, replacementSHA)
		replacementResult, err := loadLongMemEvalResults(replacementPath)
		if err != nil {
			return fmt.Errorf("load replacement results: %w", err)
		}
		if err := validateLongMemEvalRecoveryCompatibility(
			result, replacementResult,
		); err != nil {
			return err
		}
		if err := appendLongMemEvalRecoveryCacheLineage(
			result.Metadata,
			replacementResult.Metadata,
			replacementSHA,
			cacheLineage,
		); err != nil {
			return fmt.Errorf(
				"merge replacement cache metadata: %w",
				err,
			)
		}
		for _, replacementCase := range replacementResult.Cases {
			if replacementCase == nil {
				return errors.New("replacement results contain a nil case")
			}
			sourceCase := sourceCases[replacementCase.QuestionID]
			if sourceCase == nil {
				return fmt.Errorf(
					"replacement case is absent from source results",
				)
			}
			if err := validateLongMemEvalRecoveryCase(
				sourceCase, replacementCase,
			); err != nil {
				return err
			}
			if len(replacementCase.BackendResults) == 0 {
				return errors.New(
					"replacement case contains no backend results",
				)
			}
			for backend, backendResult := range replacementCase.BackendResults {
				unit := longMemEvalRecoveryUnit{
					QuestionIDSHA256: sha256String(
						replacementCase.QuestionID,
					),
					Backend: backend,
				}
				key := longMemEvalRecoveryUnitKey(unit)
				if _, ok := expected[key]; !ok {
					return fmt.Errorf(
						"replacement results contain an unregistered unit",
					)
				}
				if replaced[key] {
					return fmt.Errorf(
						"replacement unit appears more than once",
					)
				}
				if backendResult == nil {
					return errors.New(
						"replacement unit has a nil backend result",
					)
				}
				if backendResult.Backend != backend {
					return fmt.Errorf(
						"replacement backend key %q does not match result backend %q",
						backend, backendResult.Backend,
					)
				}
				if backendResult.Error != "" ||
					backendResult.AnswerError != "" ||
					backendResult.ProviderUsageError != "" ||
					backendResult.FailureStage == "backend_error" {
					return fmt.Errorf(
						"replacement unit %q contains a runtime error",
						backend,
					)
				}
				if sourceCase.BackendResults[backend] == nil {
					return fmt.Errorf(
						"replacement backend %q is absent from source case",
						backend,
					)
				}
				sourceCase.BackendResults[backend] = backendResult
				replaced[key] = true
			}
		}
	}
	if len(replaced) != len(expected) {
		return fmt.Errorf(
			"replaced %d unit(s), want %d",
			len(replaced), len(expected),
		)
	}

	result.Summary = buildLongMemEvalSummary(result.Cases)
	record := longMemEvalRecoveryRecord{
		SchemaVersion:            longMemEvalRecoverySchemaVersion,
		Status:                   "applied",
		RegisteredAt:             manifest.RegisteredAt,
		Reason:                   manifest.Reason,
		ManifestSHA256:           sha256Bytes(manifestData),
		SourceResultsSHA256:      sourceSHA,
		ReplacementResultsSHA256: replacementSHAs,
		ReplacedUnits:            append([]longMemEvalRecoveryUnit(nil), manifest.ExpectedUnits...),
		ReplacementCount:         len(replaced),
		QualityOutcomesInspected: false,
		CacheLineage:             cacheLineage,
		CacheCounterSemantics:    longMemEvalRecoveryCacheCounterSemantics(cacheLineage),
		MergerBuild:              currentLongMemEvalBuildProvenance(),
	}
	if result.Metadata == nil {
		result.Metadata = make(map[string]any)
	}
	result.Metadata["recovery"] = record

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create recovery output directory: %w", err)
	}
	if err := writeLongMemEvalResults(
		filepath.Join(outputDir, "results.json"), result,
	); err != nil {
		return err
	}
	recordData, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal recovery record: %w", err)
	}
	recordData = append(recordData, '\n')
	if err := os.WriteFile(
		filepath.Join(outputDir, "recovery.json"), recordData, 0644,
	); err != nil {
		return fmt.Errorf("write recovery record: %w", err)
	}
	return nil
}

func loadLongMemEvalRecoveryManifest(
	path string,
) ([]byte, *longMemEvalRecoveryManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read recovery manifest: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest longMemEvalRecoveryManifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, nil, fmt.Errorf("decode recovery manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, nil, errors.New(
			"decode recovery manifest: multiple JSON values",
		)
	}
	if manifest.SchemaVersion != longMemEvalRecoverySchemaVersion {
		return nil, nil, fmt.Errorf(
			"recovery manifest schema_version = %d, want %d",
			manifest.SchemaVersion, longMemEvalRecoverySchemaVersion,
		)
	}
	if manifest.Status != "registered-before-provider-calls" {
		return nil, nil, fmt.Errorf(
			"recovery manifest status %q is not preregistered",
			manifest.Status,
		)
	}
	if _, err := time.Parse(time.RFC3339, manifest.RegisteredAt); err != nil {
		return nil, nil, fmt.Errorf("parse recovery registered_at: %w", err)
	}
	if strings.TrimSpace(manifest.Reason) == "" {
		return nil, nil, errors.New("recovery manifest reason is empty")
	}
	if manifest.QualityOutcomesInspected {
		return nil, nil, errors.New(
			"recovery manifest reports inspected quality outcomes",
		)
	}
	if strings.TrimSpace(manifest.Source.ResultsPath) == "" ||
		strings.TrimSpace(manifest.Source.ResultsSHA256) == "" {
		return nil, nil, errors.New(
			"recovery manifest source is incomplete",
		)
	}
	if len(manifest.Replacements) == 0 ||
		len(manifest.ExpectedUnits) == 0 {
		return nil, nil, errors.New(
			"recovery manifest has no replacements",
		)
	}
	return data, &manifest, nil
}

func validateLongMemEvalRecoverySource(result *runResult) error {
	if result == nil || len(result.Cases) == 0 {
		return errors.New("recovery source results contain no cases")
	}
	if result.Metadata == nil {
		return errors.New("recovery source results have no metadata")
	}
	if result.Summary == nil {
		return errors.New("recovery source results have no summary")
	}
	rebuilt := buildLongMemEvalSummary(result.Cases)
	if !jsonValuesEqual(result.Summary, rebuilt) {
		return errors.New(
			"recovery source summary does not match its case records",
		)
	}
	return nil
}

func validateLongMemEvalRecoveryCompatibility(
	source, replacement *runResult,
) error {
	if replacement == nil || len(replacement.Cases) == 0 {
		return errors.New("replacement results contain no cases")
	}
	if replacement.Metadata == nil {
		return errors.New("replacement results have no metadata")
	}
	if replacement.Summary == nil ||
		!jsonValuesEqual(
			replacement.Summary,
			buildLongMemEvalSummary(replacement.Cases),
		) {
		return errors.New(
			"replacement summary does not match its case records",
		)
	}
	for _, key := range []string{
		"dataset_sha256",
		"protocol_sha256",
	} {
		sourceValue := source.Metadata[key]
		replacementValue := replacement.Metadata[key]
		if sourceValue == nil || replacementValue == nil ||
			sourceValue != replacementValue {
			return fmt.Errorf(
				"replacement metadata %q does not match source",
				key,
			)
		}
	}
	if !jsonValuesEqual(
		source.Metadata["build"], replacement.Metadata["build"],
	) {
		return errors.New(
			"replacement build metadata does not match source",
		)
	}
	return nil
}

func validateLongMemEvalRecoveryCase(source, replacement *caseResult) error {
	if source == nil || replacement == nil {
		return errors.New("cannot compare a nil recovery case")
	}
	if source.QuestionID != replacement.QuestionID ||
		source.QuestionType != replacement.QuestionType ||
		source.Question != replacement.Question ||
		source.QuestionDate != replacement.QuestionDate ||
		source.Answer != replacement.Answer ||
		source.NumSessions != replacement.NumSessions ||
		!slices.Equal(
			source.AnswerSessionIDs, replacement.AnswerSessionIDs,
		) {
		return errors.New(
			"replacement case metadata does not match source",
		)
	}
	return nil
}

func indexLongMemEvalRecoveryCases(
	cases []*caseResult,
) (map[string]*caseResult, error) {
	indexed := make(map[string]*caseResult, len(cases))
	for _, item := range cases {
		if item == nil || strings.TrimSpace(item.QuestionID) == "" {
			return nil, errors.New("results contain a nil or unidentified case")
		}
		if indexed[item.QuestionID] != nil {
			return nil, errors.New("results contain a duplicate case")
		}
		indexed[item.QuestionID] = item
	}
	return indexed, nil
}

func indexLongMemEvalRecoveryUnits(
	units []longMemEvalRecoveryUnit,
) (map[string]longMemEvalRecoveryUnit, error) {
	indexed := make(map[string]longMemEvalRecoveryUnit, len(units))
	for _, unit := range units {
		unit.QuestionIDSHA256 = strings.ToLower(
			strings.TrimSpace(unit.QuestionIDSHA256),
		)
		unit.Backend = strings.TrimSpace(unit.Backend)
		if len(unit.QuestionIDSHA256) != sha256.Size*2 {
			return nil, errors.New(
				"recovery unit question_id_sha256 is invalid",
			)
		}
		if _, err := hex.DecodeString(unit.QuestionIDSHA256); err != nil {
			return nil, errors.New(
				"recovery unit question_id_sha256 is invalid",
			)
		}
		if unit.Backend == "" {
			return nil, errors.New("recovery unit backend is empty")
		}
		key := longMemEvalRecoveryUnitKey(unit)
		if _, exists := indexed[key]; exists {
			return nil, errors.New(
				"recovery manifest contains a duplicate unit",
			)
		}
		indexed[key] = unit
	}
	return indexed, nil
}

func longMemEvalRecoveryUnitKey(unit longMemEvalRecoveryUnit) string {
	return unit.QuestionIDSHA256 + "\x00" + unit.Backend
}

func resolveLongMemEvalRecoveryPath(baseDir, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(baseDir, path))
}

func sha256File(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return sha256Bytes(data), nil
}

func sha256String(value string) string {
	return sha256Bytes([]byte(value))
}

func sha256Bytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func jsonValuesEqual(a, b any) bool {
	aJSON, aErr := json.Marshal(a)
	bJSON, bErr := json.Marshal(b)
	return aErr == nil && bErr == nil && bytes.Equal(aJSON, bJSON)
}
