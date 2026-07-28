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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

const (
	longMemEvalIntegrityAuditSchemaVersion = 1
	longMemEvalIntegrityAuditFile          = "integrity_audit.json"
	longMemEvalIntegrityAuditIssueLimit    = 100
)

type longMemEvalIntegrityAudit struct {
	SchemaVersion int                               `json:"schema_version"`
	Status        string                            `json:"status"`
	ResultsSHA256 string                            `json:"results_sha256"`
	DatasetSHA256 string                            `json:"dataset_sha256"`
	Checks        longMemEvalIntegrityAuditChecks   `json:"checks"`
	Counts        longMemEvalIntegrityAuditCounts   `json:"counts"`
	IssueCount    int                               `json:"issue_count"`
	Issues        []string                          `json:"issues"`
	Observed      longMemEvalIntegrityAuditObserved `json:"observed"`
}

type longMemEvalIntegrityAuditChecks struct {
	Provenance        bool `json:"provenance"`
	CaseIdentity      bool `json:"case_identity"`
	BackendIsolation  bool `json:"backend_isolation"`
	Replay            bool `json:"replay"`
	Attribution       bool `json:"attribution"`
	ProviderUsage     bool `json:"provider_usage"`
	Retrieval         bool `json:"retrieval"`
	Summary           bool `json:"summary"`
	ErrorFree         bool `json:"error_free"`
	CompleteSnapshots bool `json:"complete_snapshots"`
}

type longMemEvalIntegrityAuditCounts struct {
	Cases                   int `json:"cases"`
	BackendResults          int `json:"backend_results"`
	IngestTraces            int `json:"ingest_traces"`
	FinalMemories           int `json:"final_memories"`
	RetrievalHits           int `json:"retrieval_hits"`
	Mem0ExtractionLLMCalls  int `json:"mem0_extraction_llm_calls"`
	Mem0MalformedRetryCalls int `json:"mem0_malformed_retry_calls"`
}

type longMemEvalIntegrityAuditObserved struct {
	Backends           []string `json:"backends"`
	TopK               int      `json:"top_k"`
	MaxSessions        int      `json:"max_sessions"`
	MaxPairs           int      `json:"max_pairs"`
	UserScopeExplicit  bool     `json:"user_scope_explicit"`
	Mem0Implementation string   `json:"mem0_implementation,omitempty"`
}

type longMemEvalExpectedReplay struct {
	SessionIndex int
	SessionID    string
	Date         string
	PairIndex    int
	HasAnswer    bool
	Messages     []traceMessage
}

func newLongMemEvalIntegrityAudit(
	resultsSHA256 string,
	datasetSHA256 string,
) *longMemEvalIntegrityAudit {
	return &longMemEvalIntegrityAudit{
		SchemaVersion: longMemEvalIntegrityAuditSchemaVersion,
		Status:        "valid",
		ResultsSHA256: resultsSHA256,
		DatasetSHA256: datasetSHA256,
		Checks: longMemEvalIntegrityAuditChecks{
			Provenance:        true,
			CaseIdentity:      true,
			BackendIsolation:  true,
			Replay:            true,
			Attribution:       true,
			ProviderUsage:     true,
			Retrieval:         true,
			Summary:           true,
			ErrorFree:         true,
			CompleteSnapshots: true,
		},
		Issues: []string{},
	}
}

func (a *longMemEvalIntegrityAudit) fail(
	check *bool,
	format string,
	args ...any,
) {
	*check = false
	a.Status = "invalid"
	a.IssueCount++
	if len(a.Issues) < longMemEvalIntegrityAuditIssueLimit {
		a.Issues = append(a.Issues, fmt.Sprintf(format, args...))
	}
}

func auditLongMemEvalResults(
	resultsPath string,
	datasetPath string,
	outputDir string,
) error {
	result, err := loadLongMemEvalResults(resultsPath)
	if err != nil {
		return err
	}
	instances, err := loadLongMemEval(datasetPath)
	if err != nil {
		return fmt.Errorf("load audit dataset: %w", err)
	}
	resultsSHA256, err := longMemEvalFileSHA256(resultsPath)
	if err != nil {
		return fmt.Errorf("hash audit results: %w", err)
	}
	datasetSHA256, err := longMemEvalFileSHA256(datasetPath)
	if err != nil {
		return fmt.Errorf("hash audit dataset: %w", err)
	}
	audit := buildLongMemEvalIntegrityAudit(
		result,
		instances,
		resultsSHA256,
		datasetSHA256,
	)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create audit output directory: %w", err)
	}
	outputPath := filepath.Join(outputDir, longMemEvalIntegrityAuditFile)
	if err := writeLongMemEvalIntegrityAudit(outputPath, audit); err != nil {
		return err
	}
	if audit.Status != "valid" {
		return fmt.Errorf(
			"LongMemEval integrity audit failed with %d issue(s); see %s",
			audit.IssueCount,
			outputPath,
		)
	}
	log.Printf(
		"LongMemEval integrity audit passed: cases=%d backends=%d traces=%d output=%s",
		audit.Counts.Cases,
		audit.Counts.BackendResults,
		audit.Counts.IngestTraces,
		outputPath,
	)
	return nil
}

func buildLongMemEvalIntegrityAudit(
	result *runResult,
	instances []*lmeInstance,
	resultsSHA256 string,
	datasetSHA256 string,
) *longMemEvalIntegrityAudit {
	audit := newLongMemEvalIntegrityAudit(resultsSHA256, datasetSHA256)
	if result == nil {
		audit.fail(&audit.Checks.CaseIdentity, "missing run result")
		return audit
	}

	metadata := result.Metadata
	auditLongMemEvalProvenance(audit, metadata, datasetSHA256)
	backends, ok := longMemEvalAuditStringSlice(metadata["backends"])
	if !ok || len(backends) == 0 {
		audit.fail(
			&audit.Checks.Provenance,
			"metadata.backends is missing or invalid",
		)
	}
	normalizedBackends := sortedUniqueStrings(backends)
	if len(normalizedBackends) != len(backends) {
		audit.fail(
			&audit.Checks.Provenance,
			"metadata.backends contains an empty or duplicate backend",
		)
	}
	backends = normalizedBackends
	audit.Observed.Backends = backends

	topK, ok := longMemEvalMetadataInt(metadata["top_k"])
	if !ok || topK < 1 {
		audit.fail(&audit.Checks.Provenance, "metadata.top_k is missing or invalid")
	}
	audit.Observed.TopK = topK
	maxSessions, ok := longMemEvalMetadataInt(metadata["max_sessions"])
	if !ok || maxSessions < 0 {
		audit.fail(
			&audit.Checks.Provenance,
			"metadata.max_sessions is missing or invalid",
		)
		maxSessions = 0
	}
	audit.Observed.MaxSessions = maxSessions
	maxPairs, ok := longMemEvalMetadataInt(metadata["max_pairs"])
	if !ok || maxPairs < 0 {
		audit.fail(
			&audit.Checks.Provenance,
			"metadata.max_pairs is missing or invalid",
		)
		maxPairs = 0
	}
	audit.Observed.MaxPairs = maxPairs
	userScope, userScopeOK := lmeMetadataString(metadata, "user_scope")
	explicit, explicitOK := metadata["user_scope_explicit"].(bool)
	audit.Observed.UserScopeExplicit = explicit
	if !userScopeOK || strings.TrimSpace(userScope) == "" ||
		!explicitOK || !explicit {
		audit.fail(
			&audit.Checks.BackendIsolation,
			"formal result requires a non-empty explicit user scope",
		)
	}
	if value, ok := lmeMetadataString(metadata, "mem0_implementation"); ok {
		audit.Observed.Mem0Implementation = value
	}
	if containsString(backends, "mem0") &&
		strings.TrimSpace(audit.Observed.Mem0Implementation) == "" {
		audit.fail(
			&audit.Checks.Provenance,
			"mem0 backend is missing metadata.mem0_implementation",
		)
	}

	instanceByID := make(map[string]*lmeInstance, len(instances))
	for _, instance := range instances {
		if instance == nil || strings.TrimSpace(instance.QuestionID) == "" {
			continue
		}
		if _, exists := instanceByID[instance.QuestionID]; exists {
			audit.fail(
				&audit.Checks.CaseIdentity,
				"dataset has duplicate question_id %q",
				instance.QuestionID,
			)
			continue
		}
		instanceByID[instance.QuestionID] = instance
	}

	selectedIDs, selectedOK := longMemEvalAuditStringSlice(
		metadata["selected_question_ids"],
	)
	if !selectedOK {
		audit.fail(
			&audit.Checks.Provenance,
			"metadata.selected_question_ids is missing or invalid",
		)
	}
	if len(result.Cases) == 0 {
		audit.fail(
			&audit.Checks.CaseIdentity,
			"result contains no cases",
		)
	}
	resultIDs := make([]string, 0, len(result.Cases))
	seenCases := make(map[string]struct{}, len(result.Cases))
	seenUsers := make(map[string]string)
	for caseIndex, resultCase := range result.Cases {
		audit.Counts.Cases++
		caseLabel := fmt.Sprintf("case-%04d", caseIndex+1)
		if resultCase == nil {
			audit.fail(
				&audit.Checks.CaseIdentity,
				"%s is nil",
				caseLabel,
			)
			continue
		}
		resultIDs = append(resultIDs, resultCase.QuestionID)
		if _, exists := seenCases[resultCase.QuestionID]; exists {
			audit.fail(
				&audit.Checks.CaseIdentity,
				"%s duplicates a prior question_id",
				caseLabel,
			)
		}
		seenCases[resultCase.QuestionID] = struct{}{}
		instance := instanceByID[resultCase.QuestionID]
		if instance == nil {
			audit.fail(
				&audit.Checks.CaseIdentity,
				"%s question_id is absent from the audit dataset",
				caseLabel,
			)
			continue
		}
		auditLongMemEvalCaseIdentity(audit, caseLabel, resultCase, instance)
		expectedReplay := expectedLongMemEvalReplay(
			instance,
			maxSessions,
			maxPairs,
		)
		auditLongMemEvalCaseBackends(
			audit,
			caseLabel,
			resultCase,
			instance,
			backends,
			userScope,
			topK,
			expectedReplay,
			seenUsers,
		)
	}
	if !reflect.DeepEqual(resultIDs, selectedIDs) {
		audit.fail(
			&audit.Checks.CaseIdentity,
			"result case order does not match metadata.selected_question_ids",
		)
	}
	auditLongMemEvalSummary(audit, result)
	return audit
}

func auditLongMemEvalProvenance(
	audit *longMemEvalIntegrityAudit,
	metadata map[string]any,
	datasetSHA256 string,
) {
	if metadata == nil {
		audit.fail(&audit.Checks.Provenance, "result metadata is missing")
		return
	}
	recordedDatasetSHA, ok := lmeMetadataString(metadata, "dataset_sha256")
	if !ok || recordedDatasetSHA != datasetSHA256 {
		audit.fail(
			&audit.Checks.Provenance,
			"metadata.dataset_sha256 does not match the audit dataset",
		)
	}
	build, err := longMemEvalMetadataBuild(metadata, "build")
	if err != nil {
		audit.fail(
			&audit.Checks.Provenance,
			"metadata.build is invalid: %v",
			err,
		)
	} else if issue := longMemEvalBuildProvenanceIssue(build); issue != "" {
		audit.fail(
			&audit.Checks.Provenance,
			"metadata.build is unsuitable for strict comparison: %s",
			issue,
		)
	}
	for _, key := range []string{
		"protocol_version",
		"protocol_sha256",
		"implementation",
		"memory_attribution_version",
	} {
		value, present := lmeMetadataString(metadata, key)
		if !present || strings.TrimSpace(value) == "" {
			audit.fail(
				&audit.Checks.Provenance,
				"metadata.%s is missing",
				key,
			)
		}
	}
}

func auditLongMemEvalCaseIdentity(
	audit *longMemEvalIntegrityAudit,
	caseLabel string,
	resultCase *caseResult,
	instance *lmeInstance,
) {
	if resultCase.QuestionType != instance.QuestionType ||
		resultCase.Question != instance.Question ||
		resultCase.QuestionDate != instance.QuestionDate ||
		resultCase.Answer != instance.Answer.String() ||
		resultCase.NumSessions != len(instance.HaystackSessions) ||
		!reflect.DeepEqual(
			resultCase.AnswerSessionIDs,
			instance.AnswerSessionIDs,
		) {
		audit.fail(
			&audit.Checks.CaseIdentity,
			"%s fields do not match the source dataset",
			caseLabel,
		)
	}
}

func auditLongMemEvalCaseBackends(
	audit *longMemEvalIntegrityAudit,
	caseLabel string,
	resultCase *caseResult,
	instance *lmeInstance,
	backends []string,
	userScope string,
	topK int,
	expectedReplay []longMemEvalExpectedReplay,
	seenUsers map[string]string,
) {
	actualBackends := make([]string, 0, len(resultCase.BackendResults))
	for name := range resultCase.BackendResults {
		actualBackends = append(actualBackends, name)
	}
	sort.Strings(actualBackends)
	if !reflect.DeepEqual(actualBackends, backends) {
		audit.fail(
			&audit.Checks.CaseIdentity,
			"%s backend set does not match metadata.backends",
			caseLabel,
		)
	}

	sourceSessions := sortedSessions(instance)
	validSessions := make(map[string]struct{}, len(sourceSessions))
	for _, sourceSession := range sourceSessions {
		validSessions[sourceSession.ID] = struct{}{}
	}
	for _, backendName := range backends {
		br := resultCase.BackendResults[backendName]
		if br == nil {
			audit.fail(
				&audit.Checks.CaseIdentity,
				"%s/%s backend result is missing",
				caseLabel,
				backendName,
			)
			continue
		}
		audit.Counts.BackendResults++
		if br.Backend != backendName {
			audit.fail(
				&audit.Checks.CaseIdentity,
				"%s/%s records backend=%q",
				caseLabel,
				backendName,
				br.Backend,
			)
		}
		expectedUserID := fmt.Sprintf(
			"%s-%s-%s",
			backendName,
			resultCase.QuestionID,
			userScope,
		)
		expectedSessionID := fmt.Sprintf(
			"%s-%s",
			backendName,
			resultCase.QuestionID,
		)
		if br.UserID != expectedUserID || br.SessionID != expectedSessionID {
			audit.fail(
				&audit.Checks.BackendIsolation,
				"%s/%s user or session identity does not match the frozen scope",
				caseLabel,
				backendName,
			)
		}
		if prior, exists := seenUsers[br.UserID]; exists {
			audit.fail(
				&audit.Checks.BackendIsolation,
				"%s/%s reuses a user ID already used by %s",
				caseLabel,
				backendName,
				prior,
			)
		} else {
			seenUsers[br.UserID] = caseLabel + "/" + backendName
		}
		if br.Error != "" || br.AnswerError != "" ||
			br.RerankError != "" ||
			(br.Judge != nil && br.Judge.Error != "") {
			audit.fail(
				&audit.Checks.ErrorFree,
				"%s/%s contains a backend, answer, rerank, or judge error",
				caseLabel,
				backendName,
			)
		}
		if br.Judge != nil &&
			(br.Judge.RequestedRuns < 1 ||
				br.Judge.RequestedRuns%2 == 0 ||
				br.Judge.ValidRuns != br.Judge.RequestedRuns) {
			audit.fail(
				&audit.Checks.ErrorFree,
				"%s/%s has an incomplete or even-sized judge vote",
				caseLabel,
				backendName,
			)
		}
		if br.SnapshotTruncated {
			audit.fail(
				&audit.Checks.CompleteSnapshots,
				"%s/%s has a truncated final snapshot",
				caseLabel,
				backendName,
			)
		}
		auditLongMemEvalReplay(
			audit,
			caseLabel,
			backendName,
			br,
			expectedReplay,
		)
		auditLongMemEvalAttribution(
			audit,
			caseLabel,
			backendName,
			br,
		)
		auditLongMemEvalRetrieval(
			audit,
			caseLabel,
			backendName,
			br,
			topK,
			validSessions,
		)
		if backendName == "mem0" {
			if err := validateLongMemEvalMem0ProviderUsage(br); err != nil {
				audit.fail(
					&audit.Checks.ProviderUsage,
					"%s/mem0 provider usage is invalid: %v",
					caseLabel,
					err,
				)
			}
			for _, trace := range br.IngestTraces {
				if trace.TokenUsage == nil {
					continue
				}
				audit.Counts.Mem0ExtractionLLMCalls +=
					trace.TokenUsage.LLMCalls
				if trace.TokenUsage.LLMCalls > 1 {
					audit.Counts.Mem0MalformedRetryCalls +=
						trace.TokenUsage.LLMCalls - 1
				}
			}
		}
		audit.Counts.FinalMemories += len(br.FinalMemories)
		audit.Counts.RetrievalHits += len(br.Retrieval)
	}
}

func auditLongMemEvalReplay(
	audit *longMemEvalIntegrityAudit,
	caseLabel string,
	backendName string,
	br *backendResult,
	expected []longMemEvalExpectedReplay,
) {
	audit.Counts.IngestTraces += len(br.IngestTraces)
	if br.IngestedPairs != len(expected) ||
		len(br.IngestTraces) != len(expected) {
		audit.fail(
			&audit.Checks.Replay,
			"%s/%s replay count differs from the source dataset: ingested=%d traces=%d expected=%d",
			caseLabel,
			backendName,
			br.IngestedPairs,
			len(br.IngestTraces),
			len(expected),
		)
		return
	}
	for index, want := range expected {
		got := br.IngestTraces[index]
		if got.SessionIndex != want.SessionIndex ||
			got.SessionID != want.SessionID ||
			got.Date != want.Date ||
			got.PairIndex != want.PairIndex ||
			got.HasAnswer != want.HasAnswer ||
			!reflect.DeepEqual(got.Messages, want.Messages) {
			audit.fail(
				&audit.Checks.Replay,
				"%s/%s trace-%04d does not match the canonical source replay",
				caseLabel,
				backendName,
				index+1,
			)
		}
		if _, ok := parseLMEDate(got.Date); !ok {
			audit.fail(
				&audit.Checks.Replay,
				"%s/%s trace-%04d has an invalid session date",
				caseLabel,
				backendName,
				index+1,
			)
		}
		if got.Error != "" || got.ProviderUsageError != "" {
			audit.fail(
				&audit.Checks.ErrorFree,
				"%s/%s trace-%04d contains an error",
				caseLabel,
				backendName,
				index+1,
			)
		}
		if got.SnapshotTruncated {
			audit.fail(
				&audit.Checks.CompleteSnapshots,
				"%s/%s trace-%04d has a truncated snapshot",
				caseLabel,
				backendName,
				index+1,
			)
		}
		if len(got.NewMemories) > got.MemoryCount {
			audit.fail(
				&audit.Checks.Replay,
				"%s/%s trace-%04d reports more changed memories than total memories",
				caseLabel,
				backendName,
				index+1,
			)
		}
	}
}

func auditLongMemEvalAttribution(
	audit *longMemEvalIntegrityAudit,
	caseLabel string,
	backendName string,
	br *backendResult,
) {
	for traceIndex, trace := range br.IngestTraces {
		for memoryIndex, memory := range trace.NewMemories {
			if !validLongMemEvalAttribution(memory.AttributedTo) {
				audit.fail(
					&audit.Checks.Attribution,
					"%s/%s trace-%04d memory-%04d has invalid attribution",
					caseLabel,
					backendName,
					traceIndex+1,
					memoryIndex+1,
				)
			}
		}
	}
	for index, memory := range br.FinalMemories {
		if !validLongMemEvalAttribution(memory.AttributedTo) {
			audit.fail(
				&audit.Checks.Attribution,
				"%s/%s final-memory-%04d has invalid attribution",
				caseLabel,
				backendName,
				index+1,
			)
		}
	}
	for index, hit := range br.Retrieval {
		if !validLongMemEvalAttribution(hit.AttributedTo) {
			audit.fail(
				&audit.Checks.Attribution,
				"%s/%s retrieval-%04d has invalid attribution",
				caseLabel,
				backendName,
				index+1,
			)
		}
	}
	for index, hit := range br.PreRerankRetrieval {
		if !validLongMemEvalAttribution(hit.AttributedTo) {
			audit.fail(
				&audit.Checks.Attribution,
				"%s/%s pre-rerank-retrieval-%04d has invalid attribution",
				caseLabel,
				backendName,
				index+1,
			)
		}
	}
}

func auditLongMemEvalRetrieval(
	audit *longMemEvalIntegrityAudit,
	caseLabel string,
	backendName string,
	br *backendResult,
	topK int,
	validSessions map[string]struct{},
) {
	if topK > 0 && len(br.Retrieval) > topK {
		audit.fail(
			&audit.Checks.Retrieval,
			"%s/%s returned %d retrieval hits for top_k=%d",
			caseLabel,
			backendName,
			len(br.Retrieval),
			topK,
		)
	}
	if topK > 0 && len(br.PreRerankRetrieval) > topK {
		audit.fail(
			&audit.Checks.Retrieval,
			"%s/%s preserved %d pre-rerank hits for top_k=%d",
			caseLabel,
			backendName,
			len(br.PreRerankRetrieval),
			topK,
		)
	}
	if br.Evidence != nil && br.Evidence.TopK != topK {
		audit.fail(
			&audit.Checks.Retrieval,
			"%s/%s evidence top_k=%d differs from metadata top_k=%d",
			caseLabel,
			backendName,
			br.Evidence.TopK,
			topK,
		)
	}
	for index, memory := range br.FinalMemories {
		auditLongMemEvalSourceSessions(
			audit,
			caseLabel,
			backendName,
			fmt.Sprintf("final-memory-%04d", index+1),
			memory.SourceSessions,
			validSessions,
		)
	}
	for index, hit := range br.Retrieval {
		auditLongMemEvalSourceSessions(
			audit,
			caseLabel,
			backendName,
			fmt.Sprintf("retrieval-%04d", index+1),
			hit.SourceSessions,
			validSessions,
		)
	}
	for index, hit := range br.PreRerankRetrieval {
		auditLongMemEvalSourceSessions(
			audit,
			caseLabel,
			backendName,
			fmt.Sprintf("pre-rerank-retrieval-%04d", index+1),
			hit.SourceSessions,
			validSessions,
		)
	}
}

func auditLongMemEvalSourceSessions(
	audit *longMemEvalIntegrityAudit,
	caseLabel string,
	backendName string,
	itemLabel string,
	sourceSessions []string,
	validSessions map[string]struct{},
) {
	for _, sessionID := range sourceSessions {
		if _, ok := validSessions[sessionID]; ok {
			continue
		}
		audit.fail(
			&audit.Checks.Retrieval,
			"%s/%s %s references an unknown source session",
			caseLabel,
			backendName,
			itemLabel,
		)
	}
}

func auditLongMemEvalSummary(
	audit *longMemEvalIntegrityAudit,
	result *runResult,
) {
	if result.Summary == nil {
		audit.fail(&audit.Checks.Summary, "result summary is missing")
		return
	}
	want := buildLongMemEvalSummary(result.Cases)
	gotJSON, gotErr := json.Marshal(result.Summary)
	wantJSON, wantErr := json.Marshal(want)
	if gotErr != nil || wantErr != nil || !bytes.Equal(gotJSON, wantJSON) {
		audit.fail(
			&audit.Checks.Summary,
			"result summary does not equal a recomputation from case results",
		)
	}
}

func expectedLongMemEvalReplay(
	instance *lmeInstance,
	maxSessions int,
	maxPairs int,
) []longMemEvalExpectedReplay {
	if instance == nil {
		return nil
	}
	expected := make([]longMemEvalExpectedReplay, 0)
	pairsSeen := 0
	for sessionOffset, sourceSession := range sortedSessions(instance) {
		if maxSessions > 0 && sessionOffset >= maxSessions {
			break
		}
		for pairIndex, pair := range pairTurns(sourceSession.Turns) {
			if maxPairs > 0 && pairsSeen >= maxPairs {
				return expected
			}
			expected = append(expected, longMemEvalExpectedReplay{
				SessionIndex: sourceSession.OriginalIndex,
				SessionID:    sourceSession.ID,
				Date:         sourceSession.Date,
				PairIndex:    pairIndex,
				HasAnswer:    pair.HasAnswer,
				Messages:     traceMessages(pair.Messages),
			})
			pairsSeen++
		}
	}
	return expected
}

func validLongMemEvalAttribution(value string) bool {
	return value == lmeAttributionUser || value == lmeAttributionAssistant
}

func longMemEvalAuditStringSlice(value any) ([]string, bool) {
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...), true
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			text, ok := value.(string)
			if !ok {
				return nil, false
			}
			out = append(out, text)
		}
		return out, true
	default:
		return nil, false
	}
}

func sortedUniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func writeLongMemEvalIntegrityAudit(
	path string,
	audit *longMemEvalIntegrityAudit,
) error {
	if audit == nil {
		return errors.New("missing LongMemEval integrity audit")
	}
	data, err := json.MarshalIndent(audit, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal LongMemEval integrity audit: %w", err)
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write temporary LongMemEval integrity audit: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace LongMemEval integrity audit: %w", err)
	}
	return nil
}
