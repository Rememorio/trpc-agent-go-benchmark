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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type lmeAnalysisRow struct {
	QuestionID       string
	QuestionType     string
	Backend          string
	Stage            string
	RawStage         string
	ExactMatch       bool
	JudgeAvailable   bool
	JudgeCorrect     bool
	EvaluatedCorrect bool
	F1               float64
	BLEU             float64
	Evidence         string
	Error            string
	Answer           string
	Reference        string
	Question         string
	Diagnosis        string
}

type lmeBackendAnalysis struct {
	Cases          int
	ExactMatches   int
	Correct        int
	JudgedCases    int
	JudgeCorrect   int
	TotalF1        float64
	TotalBLEU      float64
	StageCounts    map[string]int
	EvidenceCounts map[string]int
	ErrorCounts    map[string]int
}

type lmeCompareRow struct {
	QuestionID              string
	QuestionType            string
	Backend                 string
	BaselineStage           string
	CandidateStage          string
	BaselineCorrect         bool
	CandidateCorrect        bool
	BaselineJudgeAvailable  bool
	CandidateJudgeAvailable bool
	BaselineEM              bool
	CandidateEM             bool
	BaselineF1              float64
	CandidateF1             float64
	DeltaF1                 float64
	BaselineBLEU            float64
	CandidateBLEU           float64
	DeltaBLEU               float64
	BaselineError           string
	CandidateError          string
	JudgeDriftIgnored       bool
	Diagnosis               string
	BaselineAnswer          string
	CandidateAnswer         string
	Reference               string
	Question                string
}

type lmeCompareBackendSummary struct {
	Cases             int
	BaselineCorrect   int
	CandidateCorrect  int
	BaselineEM        int
	CandidateEM       int
	TotalDeltaF1      float64
	Improved          int
	Regressed         int
	Unchanged         int
	JudgeDriftIgnored int
}

func analyzeLongMemEvalResults(path, outputDir string) error {
	result, err := loadLongMemEvalResults(path)
	if err != nil {
		return err
	}
	rows := longMemEvalAnalysisRows(result)
	analysis := summarizeLongMemEvalRows(rows)
	if outputDir == "" {
		outputDir = filepath.Dir(path)
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create analysis output dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "bad_cases.tsv"), []byte(formatLongMemEvalBadCases(rows)), 0644); err != nil {
		return fmt.Errorf("write bad_cases.tsv: %w", err)
	}
	report := formatLongMemEvalAnalysisMarkdown(result, rows, analysis)
	if err := os.WriteFile(filepath.Join(outputDir, "analysis.md"), []byte(report), 0644); err != nil {
		return fmt.Errorf("write analysis.md: %w", err)
	}
	fmt.Printf("LongMemEval analysis written to %s\n", outputDir)
	return nil
}

func compareLongMemEvalResults(baselinePath, candidatePath, outputDir string) error {
	baseline, err := loadLongMemEvalResults(baselinePath)
	if err != nil {
		return fmt.Errorf("load baseline results: %w", err)
	}
	candidate, err := loadLongMemEvalResults(candidatePath)
	if err != nil {
		return fmt.Errorf("load candidate results: %w", err)
	}
	if err := validateLongMemEvalComparison(baseline, candidate); err != nil {
		return err
	}
	if outputDir == "" {
		outputDir = filepath.Dir(candidatePath)
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create compare output dir: %w", err)
	}
	rows := compareLongMemEvalRows(
		filterLongMemEvalAnalysisRows(longMemEvalAnalysisRows(baseline), "pgvector"),
		filterLongMemEvalAnalysisRows(longMemEvalAnalysisRows(candidate), "pgvector"),
	)
	if len(rows) == 0 {
		return errors.New("no shared pgvector cases to compare")
	}
	if err := os.WriteFile(filepath.Join(outputDir, "comparison.tsv"), []byte(formatLongMemEvalComparisonTSV(rows)), 0644); err != nil {
		return fmt.Errorf("write comparison.tsv: %w", err)
	}
	report := formatLongMemEvalComparisonMarkdown(
		baselinePath,
		candidatePath,
		baseline,
		candidate,
		rows,
	)
	if err := os.WriteFile(filepath.Join(outputDir, "comparison.md"), []byte(report), 0644); err != nil {
		return fmt.Errorf("write comparison.md: %w", err)
	}
	fmt.Printf("LongMemEval comparison written to %s\n", outputDir)
	return nil
}

func validateLongMemEvalComparison(baseline, candidate *runResult) error {
	if baseline == nil || candidate == nil {
		return errors.New("LongMemEval comparison results must not be nil")
	}
	baselineImplementation, ok := lmeMetadataString(baseline.Metadata, "implementation")
	if !ok || baselineImplementation == "" || baselineImplementation == "unspecified" {
		return errors.New("baseline is missing a specific LongMemEval implementation label")
	}
	candidateImplementation, ok := lmeMetadataString(candidate.Metadata, "implementation")
	if !ok || candidateImplementation == "" || candidateImplementation == "unspecified" {
		return errors.New("candidate is missing a specific LongMemEval implementation label")
	}
	if baselineImplementation == candidateImplementation {
		return fmt.Errorf("baseline and candidate use the same implementation label %q", baselineImplementation)
	}
	mem0Implementation, ok := lmeMetadataString(baseline.Metadata, "mem0_implementation")
	if !ok || mem0Implementation == "" || mem0Implementation == "unspecified" {
		return errors.New("baseline is missing a specific Mem0 implementation label")
	}

	required := []string{
		"dataset_sha256",
		"selection_sha256",
		"protocol_version",
		"protocol_sha256",
		"model",
		"model_variant",
		"model_temperature",
		"embedding_model",
		"answer_prompt_version",
		"answer_generation",
		"judge_prompt_version",
		"judge_generation",
	}
	for _, key := range required {
		if err := compareLongMemEvalMetadataValue(baseline.Metadata, candidate.Metadata, key, true); err != nil {
			return err
		}
	}
	for _, key := range []string{
		"reanswer_model",
		"reanswer_model_variant",
		"rerank_model",
		"rerank_model_variant",
		"rerank_prompt_version",
		"rerank_generation",
		"rerank_top_n",
		"judge_model",
		"judge_model_variant",
		"judge_runs",
	} {
		if err := compareLongMemEvalMetadataValue(baseline.Metadata, candidate.Metadata, key, false); err != nil {
			return err
		}
	}
	if err := validateLongMemEvalBuildPair(
		baseline.Metadata,
		candidate.Metadata,
		"build",
		true,
	); err != nil {
		return err
	}
	if longMemEvalMetadataPresent(baseline.Metadata, "reanswer_model") ||
		longMemEvalMetadataPresent(candidate.Metadata, "reanswer_model") {
		if err := validateLongMemEvalBuildPair(
			baseline.Metadata,
			candidate.Metadata,
			"reanswer_build",
			false,
		); err != nil {
			return err
		}
	}
	if longMemEvalMetadataPresent(baseline.Metadata, "rerank_model") ||
		longMemEvalMetadataPresent(candidate.Metadata, "rerank_model") {
		if err := validateLongMemEvalBuildPair(
			baseline.Metadata,
			candidate.Metadata,
			"rerank_build",
			false,
		); err != nil {
			return err
		}
	}
	if longMemEvalMetadataPresent(baseline.Metadata, "judge_runs") ||
		longMemEvalMetadataPresent(candidate.Metadata, "judge_runs") {
		if err := validateLongMemEvalBuildPair(
			baseline.Metadata,
			candidate.Metadata,
			"judge_build",
			false,
		); err != nil {
			return err
		}
	}
	return validateLongMemEvalComparisonArms(baseline, candidate)
}

func validateLongMemEvalBuildPair(
	baseline,
	candidate map[string]any,
	key string,
	requireMemoryModules bool,
) error {
	baseBuild, err := longMemEvalMetadataBuild(baseline, key)
	if err != nil {
		return fmt.Errorf("invalid baseline %s provenance: %w", key, err)
	}
	candidateBuild, err := longMemEvalMetadataBuild(candidate, key)
	if err != nil {
		return fmt.Errorf("invalid candidate %s provenance: %w", key, err)
	}
	for label, build := range map[string]lmeBuildProvenance{
		"baseline":  baseBuild,
		"candidate": candidateBuild,
	} {
		if strings.TrimSpace(build.Revision) == "" {
			return fmt.Errorf("%s %s provenance is missing benchmark_revision", label, key)
		}
		if build.Modified {
			return fmt.Errorf("%s %s provenance records a modified benchmark build", label, key)
		}
		if strings.TrimSpace(build.GoVersion) == "" {
			return fmt.Errorf("%s %s provenance is missing go_version", label, key)
		}
		if build.BuildProfile != "candidate" && build.BuildProfile != "upstream" {
			return fmt.Errorf(
				"%s %s provenance has missing or unsupported build_profile",
				label,
				key,
			)
		}
		if strings.TrimSpace(build.ModuleManifestSHA256) == "" {
			return fmt.Errorf(
				"%s %s provenance is missing module_manifest_sha256",
				label,
				key,
			)
		}
		if strings.TrimSpace(build.ModuleSumSHA256) == "" {
			return fmt.Errorf(
				"%s %s provenance is missing module_sum_sha256",
				label,
				key,
			)
		}
		if requireMemoryModules {
			if err := validateLongMemEvalMemoryModules(build.Modules); err != nil {
				return fmt.Errorf("%s %s provenance: %w", label, key, err)
			}
		}
	}
	if baseBuild.Revision != candidateBuild.Revision {
		return fmt.Errorf(
			"LongMemEval comparison %s benchmark revision mismatch: baseline=%s candidate=%s",
			key,
			baseBuild.Revision,
			candidateBuild.Revision,
		)
	}
	if baseBuild.GoVersion != candidateBuild.GoVersion {
		return fmt.Errorf(
			"LongMemEval comparison %s Go version mismatch: baseline=%s candidate=%s",
			key,
			baseBuild.GoVersion,
			candidateBuild.GoVersion,
		)
	}
	return nil
}

func longMemEvalMetadataBuild(metadata map[string]any, key string) (lmeBuildProvenance, error) {
	var build lmeBuildProvenance
	if metadata == nil {
		return build, fmt.Errorf("missing %s metadata", key)
	}
	value, ok := metadata[key]
	if !ok {
		return build, fmt.Errorf("missing %s metadata", key)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return build, fmt.Errorf("encode metadata: %w", err)
	}
	if err := json.Unmarshal(data, &build); err != nil {
		return build, fmt.Errorf("decode metadata: %w", err)
	}
	return build, nil
}

func longMemEvalMetadataPresent(metadata map[string]any, key string) bool {
	if metadata == nil {
		return false
	}
	_, ok := metadata[key]
	return ok
}

func compareLongMemEvalMetadataValue(
	baseline,
	candidate map[string]any,
	key string,
	required bool,
) error {
	baselineValue, baselineOK, err := canonicalLongMemEvalMetadataValue(baseline, key)
	if err != nil {
		return fmt.Errorf("encode baseline metadata %q: %w", key, err)
	}
	candidateValue, candidateOK, err := canonicalLongMemEvalMetadataValue(candidate, key)
	if err != nil {
		return fmt.Errorf("encode candidate metadata %q: %w", key, err)
	}
	if required && (!baselineOK || !candidateOK) {
		return fmt.Errorf("strict LongMemEval comparison requires metadata %q in both results", key)
	}
	if baselineOK != candidateOK || (baselineOK && baselineValue != candidateValue) {
		return fmt.Errorf(
			"LongMemEval comparison metadata mismatch for %q: baseline=%s candidate=%s",
			key,
			baselineValue,
			candidateValue,
		)
	}
	return nil
}

func canonicalLongMemEvalMetadataValue(metadata map[string]any, key string) (string, bool, error) {
	if metadata == nil {
		return "<missing>", false, nil
	}
	value, ok := metadata[key]
	if !ok {
		return "<missing>", false, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", true, err
	}
	return string(data), true, nil
}

func lmeMetadataString(metadata map[string]any, key string) (string, bool) {
	if metadata == nil {
		return "", false
	}
	value, ok := metadata[key].(string)
	return strings.TrimSpace(value), ok
}

func validateLongMemEvalComparisonArms(baseline, candidate *runResult) error {
	candidateCases := make(map[string]*caseResult, len(candidate.Cases))
	for _, cr := range candidate.Cases {
		if cr != nil {
			candidateCases[cr.QuestionID] = cr
		}
	}
	if len(baseline.Cases) != len(candidate.Cases) {
		return fmt.Errorf(
			"LongMemEval comparison case count mismatch: baseline=%d candidate=%d",
			len(baseline.Cases),
			len(candidate.Cases),
		)
	}
	for _, baseCase := range baseline.Cases {
		if baseCase == nil {
			return errors.New("baseline contains a nil LongMemEval case")
		}
		if baseCase.BackendResults["pgvector"] == nil || baseCase.BackendResults["mem0"] == nil {
			return fmt.Errorf(
				"baseline case %q must contain both pgvector and mem0 arms",
				baseCase.QuestionID,
			)
		}
		candidateCase := candidateCases[baseCase.QuestionID]
		if candidateCase == nil || candidateCase.BackendResults["pgvector"] == nil {
			return fmt.Errorf("candidate case %q is missing the pgvector arm", baseCase.QuestionID)
		}
		if baseCase.QuestionType != candidateCase.QuestionType ||
			baseCase.Question != candidateCase.Question ||
			baseCase.Answer != candidateCase.Answer {
			return fmt.Errorf("LongMemEval case content mismatch for %q", baseCase.QuestionID)
		}
	}
	return nil
}

func filterLongMemEvalAnalysisRows(rows []lmeAnalysisRow, backend string) []lmeAnalysisRow {
	out := make([]lmeAnalysisRow, 0, len(rows))
	for _, row := range rows {
		if row.Backend == backend {
			out = append(out, row)
		}
	}
	return out
}

func loadLongMemEvalResults(path string) (*runResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read results: %w", err)
	}
	var result runResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("decode results: %w", err)
	}
	return &result, nil
}

func longMemEvalAnalysisOutputDir(resultsPath string) string {
	if strings.TrimSpace(*flagOutput) == "" || *flagOutput == "../results" {
		return filepath.Dir(resultsPath)
	}
	return *flagOutput
}

func longMemEvalCompareOutputDir(candidatePath string) string {
	if strings.TrimSpace(*flagOutput) == "" || *flagOutput == "../results" {
		return filepath.Dir(candidatePath)
	}
	return *flagOutput
}

func parseLongMemEvalComparePaths(raw string) (string, string, error) {
	parts := parseCommaList(raw)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("lme-compare-results expects baseline,candidate paths")
	}
	return parts[0], parts[1], nil
}

func longMemEvalAnalysisRows(result *runResult) []lmeAnalysisRow {
	if result == nil {
		return nil
	}
	rows := make([]lmeAnalysisRow, 0)
	for _, cr := range result.Cases {
		if cr == nil {
			continue
		}
		backends := make([]string, 0, len(cr.BackendResults))
		for backend := range cr.BackendResults {
			backends = append(backends, backend)
		}
		sort.Strings(backends)
		for _, backend := range backends {
			br := cr.BackendResults[backend]
			if br == nil {
				continue
			}
			judgeCorrect, judgeAvailable := longMemEvalJudgeCorrect(br)
			evaluatedCorrect := br.ExactMatch
			if judgeAvailable {
				evaluatedCorrect = judgeCorrect
			}
			rawStage := normalizedFailureStage(br)
			rows = append(rows, lmeAnalysisRow{
				QuestionID:       cr.QuestionID,
				QuestionType:     cr.QuestionType,
				Backend:          backend,
				Stage:            evaluatedFailureStage(br, rawStage, judgeCorrect, judgeAvailable),
				RawStage:         rawStage,
				ExactMatch:       br.ExactMatch,
				JudgeAvailable:   judgeAvailable,
				JudgeCorrect:     judgeCorrect,
				EvaluatedCorrect: evaluatedCorrect,
				F1:               br.F1,
				BLEU:             br.BLEU,
				Evidence:         evidenceStatus(br.Evidence),
				Error:            br.Error,
				Answer:           br.Answer,
				Reference:        cr.Answer,
				Question:         cr.Question,
				Diagnosis:        answerGapDiagnosis(cr.QuestionType, br.Answer, cr.Answer),
			})
		}
	}
	return rows
}

func compareLongMemEvalRows(baselineRows, candidateRows []lmeAnalysisRow) []lmeCompareRow {
	baseline := make(map[string]lmeAnalysisRow, len(baselineRows))
	candidate := make(map[string]lmeAnalysisRow, len(candidateRows))
	for _, row := range baselineRows {
		key := lmeCompareKey(row.QuestionID, row.Backend)
		baseline[key] = row
	}
	for _, row := range candidateRows {
		key := lmeCompareKey(row.QuestionID, row.Backend)
		candidate[key] = row
	}
	out := make([]lmeCompareRow, 0, len(candidate))
	for key, cand := range candidate {
		base, ok := baseline[key]
		if !ok {
			continue
		}
		row := lmeCompareRow{
			QuestionID:              cand.QuestionID,
			QuestionType:            cand.QuestionType,
			Backend:                 cand.Backend,
			BaselineStage:           base.Stage,
			CandidateStage:          cand.Stage,
			BaselineCorrect:         base.EvaluatedCorrect,
			CandidateCorrect:        cand.EvaluatedCorrect,
			BaselineJudgeAvailable:  base.JudgeAvailable,
			CandidateJudgeAvailable: cand.JudgeAvailable,
			BaselineEM:              base.ExactMatch,
			CandidateEM:             cand.ExactMatch,
			BaselineF1:              base.F1,
			CandidateF1:             cand.F1,
			BaselineBLEU:            base.BLEU,
			CandidateBLEU:           cand.BLEU,
			BaselineError:           base.Error,
			CandidateError:          cand.Error,
			Diagnosis:               cand.Diagnosis,
			BaselineAnswer:          base.Answer,
			CandidateAnswer:         cand.Answer,
			Reference:               cand.Reference,
			Question:                cand.Question,
		}
		row.DeltaF1 = row.CandidateF1 - row.BaselineF1
		row.DeltaBLEU = row.CandidateBLEU - row.BaselineBLEU
		if sameLongMemEvalComparisonAnswer(base, cand) &&
			row.BaselineCorrect != row.CandidateCorrect {
			row.CandidateCorrect = row.BaselineCorrect
			row.JudgeDriftIgnored = true
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].QuestionID != out[j].QuestionID {
			return out[i].QuestionID < out[j].QuestionID
		}
		return out[i].Backend < out[j].Backend
	})
	return out
}

func sameLongMemEvalComparisonAnswer(baseline, candidate lmeAnalysisRow) bool {
	return normalizedLongMemEvalComparisonText(baseline.Question) ==
		normalizedLongMemEvalComparisonText(candidate.Question) &&
		normalizedLongMemEvalComparisonText(baseline.Reference) ==
			normalizedLongMemEvalComparisonText(candidate.Reference) &&
		normalizedLongMemEvalComparisonText(baseline.Answer) ==
			normalizedLongMemEvalComparisonText(candidate.Answer)
}

func normalizedLongMemEvalComparisonText(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

func lmeCompareKey(questionID, backend string) string {
	return questionID + "\x00" + backend
}

func summarizeLongMemEvalRows(rows []lmeAnalysisRow) map[string]*lmeBackendAnalysis {
	out := make(map[string]*lmeBackendAnalysis)
	for _, row := range rows {
		a := out[row.Backend]
		if a == nil {
			a = &lmeBackendAnalysis{
				StageCounts:    make(map[string]int),
				EvidenceCounts: make(map[string]int),
				ErrorCounts:    make(map[string]int),
			}
			out[row.Backend] = a
		}
		a.Cases++
		if row.EvaluatedCorrect {
			a.Correct++
		}
		if row.ExactMatch {
			a.ExactMatches++
		}
		if row.JudgeAvailable {
			a.JudgedCases++
			if row.JudgeCorrect {
				a.JudgeCorrect++
			}
		}
		a.TotalF1 += row.F1
		a.TotalBLEU += row.BLEU
		a.StageCounts[row.Stage]++
		a.EvidenceCounts[row.Evidence]++
		if row.Error != "" {
			a.ErrorCounts[row.Error]++
		}
	}
	return out
}

func normalizedFailureStage(br *backendResult) string {
	if br == nil {
		return "missing"
	}
	stage := strings.TrimSpace(br.FailureStage)
	if stage == "" {
		if br.Error != "" {
			return "backend_error"
		}
		return "unknown"
	}
	return stage
}

func evaluatedFailureStage(
	br *backendResult,
	rawStage string,
	judgeCorrect bool,
	judgeAvailable bool,
) string {
	if !judgeAvailable || br == nil || br.Error != "" {
		return rawStage
	}
	switch rawStage {
	case "ok", "ok_abstention", "answer_miss", "abstention_answered":
		if br.Evidence != nil && br.Evidence.IsAbstention {
			if judgeCorrect {
				return "ok_abstention"
			}
			return "abstention_answered"
		}
		if judgeCorrect {
			return "ok"
		}
		return "answer_miss"
	default:
		return rawStage
	}
}

func longMemEvalJudgeCorrect(br *backendResult) (bool, bool) {
	if br == nil || br.Judge == nil || strings.TrimSpace(br.Judge.Error) != "" {
		return false, false
	}
	if !validLongMemEvalJudgeConsensus(br.Judge) {
		return false, false
	}
	correct, err := parseLongMemEvalJudge(br.Judge.Raw)
	if err != nil || correct != br.Judge.Correct {
		return false, false
	}
	return correct, true
}

func validLongMemEvalJudgeConsensus(judge *lmeJudgeResult) bool {
	if judge == nil || judge.RequestedRuns == 0 || judge.RequestedRuns == 1 {
		return true
	}
	if judge.RequestedRuns < 1 || judge.RequestedRuns%2 == 0 ||
		len(judge.Attempts) != judge.RequestedRuns {
		return false
	}
	var yesVotes, noVotes int
	for _, attempt := range judge.Attempts {
		if strings.TrimSpace(attempt.Error) != "" {
			continue
		}
		correct, err := parseLongMemEvalJudge(attempt.Raw)
		if err != nil || correct != attempt.Correct {
			return false
		}
		if correct {
			yesVotes++
		} else {
			noVotes++
		}
	}
	if judge.ValidRuns != yesVotes+noVotes {
		return false
	}
	required := judge.RequestedRuns/2 + 1
	return (yesVotes >= required && judge.Correct) ||
		(noVotes >= required && !judge.Correct)
}

func evidenceStatus(ev *evidenceMetrics) string {
	if ev == nil {
		return "none"
	}
	if ev.IsAbstention {
		return "abstention"
	}
	if !ev.HasEvidenceLabels {
		return "unlabeled"
	}
	if !ev.ExtractRecallAny {
		return "extract_miss"
	}
	if !ev.RetrievalRecallAny {
		return "retrieval_miss"
	}
	if !ev.RetrievalRecallAll {
		return "partial_retrieval"
	}
	return "full_retrieval"
}

func formatLongMemEvalBadCases(rows []lmeAnalysisRow) string {
	var b strings.Builder
	b.WriteString("question_id\tquestion_type\tbackend\tstage\traw_stage\tevaluated_correct\texact_match\tjudge_available\tjudge_correct\tf1\tbleu\tevidence\tdiagnosis\terror\tanswer\treference\tquestion\n")
	for _, row := range rows {
		if row.EvaluatedCorrect {
			continue
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%v\t%v\t%v\t%v\t%.4f\t%.4f\t%s\t%s\t%s\t%s\t%s\t%s\n",
			tsvCell(row.QuestionID),
			tsvCell(row.QuestionType),
			tsvCell(row.Backend),
			tsvCell(row.Stage),
			tsvCell(row.RawStage),
			row.EvaluatedCorrect,
			row.ExactMatch,
			row.JudgeAvailable,
			row.JudgeCorrect,
			row.F1,
			row.BLEU,
			tsvCell(row.Evidence),
			tsvCell(row.Diagnosis),
			tsvCell(row.Error),
			tsvCell(row.Answer),
			tsvCell(row.Reference),
			tsvCell(row.Question),
		)
	}
	return b.String()
}

func formatLongMemEvalComparisonTSV(rows []lmeCompareRow) string {
	var b strings.Builder
	b.WriteString("question_id\tquestion_type\tbackend\tbaseline_stage\tcandidate_stage\tbaseline_correct\tcandidate_correct\tbaseline_judge_available\tcandidate_judge_available\tjudge_drift_ignored\tbaseline_em\tcandidate_em\tbaseline_f1\tcandidate_f1\tdelta_f1\tbaseline_bleu\tcandidate_bleu\tdelta_bleu\tdiagnosis\tbaseline_answer\tcandidate_answer\treference\tquestion\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%v\t%v\t%v\t%v\t%v\t%v\t%v\t%.4f\t%.4f\t%+.4f\t%.4f\t%.4f\t%+.4f\t%s\t%s\t%s\t%s\t%s\n",
			tsvCell(row.QuestionID),
			tsvCell(row.QuestionType),
			tsvCell(row.Backend),
			tsvCell(row.BaselineStage),
			tsvCell(row.CandidateStage),
			row.BaselineCorrect,
			row.CandidateCorrect,
			row.BaselineJudgeAvailable,
			row.CandidateJudgeAvailable,
			row.JudgeDriftIgnored,
			row.BaselineEM,
			row.CandidateEM,
			row.BaselineF1,
			row.CandidateF1,
			row.DeltaF1,
			row.BaselineBLEU,
			row.CandidateBLEU,
			row.DeltaBLEU,
			tsvCell(row.Diagnosis),
			tsvCell(row.BaselineAnswer),
			tsvCell(row.CandidateAnswer),
			tsvCell(row.Reference),
			tsvCell(row.Question),
		)
	}
	return b.String()
}

func formatLongMemEvalComparisonMarkdown(
	baselinePath,
	candidatePath string,
	baseline,
	candidate *runResult,
	rows []lmeCompareRow,
) string {
	summary := summarizeLongMemEvalCompareRows(rows)
	var b strings.Builder
	b.WriteString("# LongMemEval Comparison\n\n")
	fmt.Fprintf(&b, "- Baseline: `%s`\n", baselinePath)
	fmt.Fprintf(&b, "- Candidate: `%s`\n", candidatePath)
	fmt.Fprintf(&b, "- Baseline implementation: `%s`\n", longMemEvalResultImplementation(baseline))
	fmt.Fprintf(&b, "- Candidate implementation: `%s`\n", longMemEvalResultImplementation(candidate))
	b.WriteString("- Correctness uses the semantic judge when available and falls back to exact match; no model calls are made.\n")
	b.WriteString("- Pgvector deltas use only cases present in both runs. Mem0 is frozen from the baseline run and is not rerun or delta-compared.\n")
	b.WriteString("- Identical normalized questions, references, and answers are treated as unchanged; conflicting judge verdicts are counted as ignored judge drift.\n\n")

	writeLongMemEvalComparisonArms(&b, baseline, candidate)

	b.WriteString("## Backend Delta Summary\n\n")
	b.WriteString("| Backend | Cases | Correct Baseline | Correct Candidate | EM Baseline | EM Candidate | Avg Delta F1 | Improved | Regressed | Unchanged | Judge Drift Ignored |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, backend := range sortedCompareBackends(summary) {
		s := summary[backend]
		avgDelta := 0.0
		if s.Cases > 0 {
			avgDelta = s.TotalDeltaF1 / float64(s.Cases)
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d | %+.4f | %d | %d | %d | %d |\n",
			mdCell(backend), s.Cases, s.BaselineCorrect, s.CandidateCorrect,
			s.BaselineEM, s.CandidateEM,
			avgDelta, s.Improved, s.Regressed, s.Unchanged, s.JudgeDriftIgnored)
	}

	b.WriteString("\n## Top Improvements\n\n")
	writeCompareRowsTable(&b, topCompareRows(rows, true, 20))
	b.WriteString("\n## Top Regressions\n\n")
	writeCompareRowsTable(&b, topCompareRows(rows, false, 20))
	return b.String()
}

func longMemEvalResultImplementation(result *runResult) string {
	if result == nil {
		return "unknown"
	}
	if value, ok := lmeMetadataString(result.Metadata, "implementation"); ok && value != "" {
		return value
	}
	return "unknown"
}

func writeLongMemEvalComparisonArms(b *strings.Builder, baseline, candidate *runResult) {
	b.WriteString("## Three-Arm Summary\n\n")
	b.WriteString("| Arm | Implementation | Backend | Cases | Correct | EM | Avg F1 | Memories | LLM Calls | LLM Tokens | Cached | Embedding Calls | Embedding Tokens | Ingest (s) |\n")
	b.WriteString("|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	writeLongMemEvalComparisonArm(
		b,
		"upstream baseline",
		longMemEvalResultImplementation(baseline),
		"pgvector",
		baseline,
	)
	writeLongMemEvalComparisonArm(
		b,
		"candidate",
		longMemEvalResultImplementation(candidate),
		"pgvector",
		candidate,
	)
	writeLongMemEvalComparisonArm(
		b,
		"reference",
		longMemEvalReferenceImplementation(baseline),
		"mem0",
		baseline,
	)
	b.WriteByte('\n')
}

func longMemEvalReferenceImplementation(result *runResult) string {
	if result != nil {
		if implementation, ok := lmeMetadataString(result.Metadata, "mem0_implementation"); ok && implementation != "" {
			return implementation
		}
		if mode, ok := lmeMetadataString(result.Metadata, "mem0_mode"); ok && mode != "" {
			return "Mem0 " + mode
		}
	}
	return "self-hosted Mem0"
}

func writeLongMemEvalComparisonArm(
	b *strings.Builder,
	arm,
	implementation,
	backend string,
	result *runResult,
) {
	analysis := summarizeLongMemEvalRows(longMemEvalAnalysisRows(result))[backend]
	if analysis == nil {
		return
	}
	avgF1 := 0.0
	if analysis.Cases > 0 {
		avgF1 = analysis.TotalF1 / float64(analysis.Cases)
	}
	usage := backendSummary{}
	if result != nil && result.Summary != nil && result.Summary.BackendSummaries[backend] != nil {
		usage = *result.Summary.BackendSummaries[backend]
	}
	ingestDurationMs := int64(0)
	if result != nil {
		for _, cr := range result.Cases {
			if cr != nil && cr.BackendResults[backend] != nil {
				ingestDurationMs += cr.BackendResults[backend].IngestDuration
			}
		}
	}
	fmt.Fprintf(
		b,
		"| %s | %s | %s | %d | %d | %d | %.4f | %d | %d | %d | %d | %d | %d | %.1f |\n",
		mdCell(arm),
		mdCell(implementation),
		mdCell(backend),
		analysis.Cases,
		analysis.Correct,
		analysis.ExactMatches,
		avgF1,
		usage.TotalMemories,
		usage.TokenUsage.LLMCalls,
		usage.TokenUsage.TotalTokens,
		usage.TokenUsage.CachedTokens,
		usage.EmbeddingUsage.Calls,
		usage.EmbeddingUsage.TotalTokens,
		float64(ingestDurationMs)/1000,
	)
}

func summarizeLongMemEvalCompareRows(rows []lmeCompareRow) map[string]*lmeCompareBackendSummary {
	out := make(map[string]*lmeCompareBackendSummary)
	for _, row := range rows {
		s := out[row.Backend]
		if s == nil {
			s = &lmeCompareBackendSummary{}
			out[row.Backend] = s
		}
		s.Cases++
		if row.BaselineCorrect {
			s.BaselineCorrect++
		}
		if row.CandidateCorrect {
			s.CandidateCorrect++
		}
		if row.BaselineEM {
			s.BaselineEM++
		}
		if row.CandidateEM {
			s.CandidateEM++
		}
		s.TotalDeltaF1 += row.DeltaF1
		if row.JudgeDriftIgnored {
			s.JudgeDriftIgnored++
		}
		switch {
		case !row.BaselineCorrect && row.CandidateCorrect:
			s.Improved++
		case row.BaselineCorrect && !row.CandidateCorrect:
			s.Regressed++
		default:
			s.Unchanged++
		}
	}
	return out
}

func sortedCompareBackends(summary map[string]*lmeCompareBackendSummary) []string {
	out := make([]string, 0, len(summary))
	for backend := range summary {
		out = append(out, backend)
	}
	sort.Strings(out)
	return out
}

func topCompareRows(rows []lmeCompareRow, improvements bool, limit int) []lmeCompareRow {
	filtered := make([]lmeCompareRow, 0, len(rows))
	for _, row := range rows {
		changedAsRequested := !row.BaselineCorrect && row.CandidateCorrect
		if !improvements {
			changedAsRequested = row.BaselineCorrect && !row.CandidateCorrect
		}
		if !changedAsRequested {
			continue
		}
		filtered = append(filtered, row)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].DeltaF1 != filtered[j].DeltaF1 {
			if improvements {
				return filtered[i].DeltaF1 > filtered[j].DeltaF1
			}
			return filtered[i].DeltaF1 < filtered[j].DeltaF1
		}
		if filtered[i].QuestionID != filtered[j].QuestionID {
			return filtered[i].QuestionID < filtered[j].QuestionID
		}
		return filtered[i].Backend < filtered[j].Backend
	})
	if limit > 0 && len(filtered) > limit {
		return filtered[:limit]
	}
	return filtered
}

func writeCompareRowsTable(b *strings.Builder, rows []lmeCompareRow) {
	b.WriteString("| Question | Type | Backend | Correctness | Delta F1 | Baseline | Candidate | Diagnosis |\n")
	b.WriteString("|---|---|---|---|---:|---|---|---|\n")
	for _, row := range rows {
		fmt.Fprintf(b, "| %s | %s | %s | %v -> %v | %+.4f | %s | %s | %s |\n",
			mdCell(row.QuestionID),
			mdCell(row.QuestionType),
			mdCell(row.Backend),
			row.BaselineCorrect,
			row.CandidateCorrect,
			row.DeltaF1,
			mdCell(compareAnswerCell(row.BaselineStage, row.BaselineF1, row.BaselineAnswer)),
			mdCell(compareAnswerCell(row.CandidateStage, row.CandidateF1, row.CandidateAnswer)),
			mdCell(truncate(row.Diagnosis, 120)),
		)
	}
}

func compareAnswerCell(stage string, f1 float64, answer string) string {
	return fmt.Sprintf("stage=%s F1=%.4f answer=%s", stage, f1, truncate(answer, 80))
}

func formatLongMemEvalAnalysisMarkdown(
	result *runResult,
	rows []lmeAnalysisRow,
	analysis map[string]*lmeBackendAnalysis,
) string {
	var b strings.Builder
	b.WriteString("# LongMemEval Memory Analysis\n\n")
	if result != nil && result.Summary != nil {
		fmt.Fprintf(&b, "- Total cases: %d\n", result.Summary.TotalCases)
	}
	b.WriteString("- Failure stages are computed from saved `results.json`; no model calls are made. " +
		"Answer-stage labels use a valid semantic-judge verdict when available. `results.json` retains the pre-judge `failure_stage`, which is also exposed as `raw_stage` for bad cases.\n\n")

	b.WriteString("## Backend Summary\n\n")
	b.WriteString("| Backend | Cases | EM | Judge | Avg F1 | Avg BLEU | LLM Calls | LLM Tokens | Cached | Cache Hit | Embedding Calls | Embedding Tokens | Provider Usage |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, backend := range sortedAnalysisBackends(analysis) {
		a := analysis[backend]
		avgF1, avgBLEU := 0.0, 0.0
		if a.Cases > 0 {
			avgF1 = a.TotalF1 / float64(a.Cases)
			avgBLEU = a.TotalBLEU / float64(a.Cases)
		}
		judge := "-"
		if a.JudgedCases > 0 {
			judge = fmt.Sprintf("%d/%d", a.JudgeCorrect, a.JudgedCases)
		}
		var usage backendSummary
		if result != nil && result.Summary != nil &&
			result.Summary.BackendSummaries[backend] != nil {
			usage = *result.Summary.BackendSummaries[backend]
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %s | %.4f | %.4f | %d | %d | %d | %.4f | %d | %d | %d/%d |\n",
			backend, a.Cases, a.ExactMatches, judge, avgF1, avgBLEU,
			usage.TokenUsage.LLMCalls,
			usage.TokenUsage.TotalTokens,
			usage.TokenUsage.CachedTokens,
			usage.TokenUsage.CacheHitRate,
			usage.EmbeddingUsage.Calls,
			usage.EmbeddingUsage.TotalTokens,
			usage.ProviderUsageCases,
			a.Cases)
	}

	b.WriteString("\n## Failure Stages\n\n")
	for _, backend := range sortedAnalysisBackends(analysis) {
		fmt.Fprintf(&b, "### %s\n\n", backend)
		writeCountTable(&b, "Stage", analysis[backend].StageCounts)
	}

	b.WriteString("\n## Evidence Status\n\n")
	for _, backend := range sortedAnalysisBackends(analysis) {
		fmt.Fprintf(&b, "### %s\n\n", backend)
		writeCountTable(&b, "Evidence", analysis[backend].EvidenceCounts)
	}

	b.WriteString("\n## Error Summary\n\n")
	for _, backend := range sortedAnalysisBackends(analysis) {
		if len(analysis[backend].ErrorCounts) == 0 {
			continue
		}
		fmt.Fprintf(&b, "### %s\n\n", backend)
		writeCountTable(&b, "Error", analysis[backend].ErrorCounts)
	}

	b.WriteString("\n## Backend Disagreements\n\n")
	b.WriteString("| Question | Type | mem0 | pgvector | Reference |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, d := range longMemEvalBackendDisagreements(result) {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
			mdCell(d.QuestionID),
			mdCell(d.QuestionType),
			mdCell(d.Mem0),
			mdCell(d.PGVector),
			mdCell(d.Reference),
		)
	}

	b.WriteString("\n## Lowest F1 Bad Cases\n\n")
	b.WriteString("| Question | Type | Backend | Stage | F1 | Evidence | Diagnosis | Answer | Reference |\n")
	b.WriteString("|---|---|---|---|---:|---|---|---|---|\n")
	for _, row := range lowestF1Rows(rows, 20) {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %.4f | %s | %s | %s | %s |\n",
			mdCell(row.QuestionID),
			mdCell(row.QuestionType),
			mdCell(row.Backend),
			mdCell(row.Stage),
			row.F1,
			mdCell(row.Evidence),
			mdCell(truncate(row.Diagnosis, 120)),
			mdCell(truncate(row.Answer, 120)),
			mdCell(truncate(row.Reference, 120)),
		)
	}
	return b.String()
}

func sortedAnalysisBackends(analysis map[string]*lmeBackendAnalysis) []string {
	out := make([]string, 0, len(analysis))
	for backend := range analysis {
		out = append(out, backend)
	}
	sort.Strings(out)
	return out
}

func writeCountTable(b *strings.Builder, label string, counts map[string]int) {
	b.WriteString("| " + label + " | Count |\n")
	b.WriteString("|---|---:|\n")
	for _, item := range sortedCounts(counts) {
		fmt.Fprintf(b, "| %s | %d |\n", mdCell(item.Key), item.Count)
	}
	b.WriteByte('\n')
}

type lmeCount struct {
	Key   string
	Count int
}

func sortedCounts(counts map[string]int) []lmeCount {
	out := make([]lmeCount, 0, len(counts))
	for key, count := range counts {
		out = append(out, lmeCount{Key: key, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key < out[j].Key
	})
	return out
}

type lmeBackendDisagreement struct {
	QuestionID   string
	QuestionType string
	Mem0         string
	PGVector     string
	Reference    string
}

func longMemEvalBackendDisagreements(result *runResult) []lmeBackendDisagreement {
	if result == nil {
		return nil
	}
	out := make([]lmeBackendDisagreement, 0)
	for _, cr := range result.Cases {
		if cr == nil {
			continue
		}
		mem0 := cr.BackendResults["mem0"]
		pgv := cr.BackendResults["pgvector"]
		if mem0 == nil || pgv == nil || evaluatedLongMemEvalCorrect(mem0) == evaluatedLongMemEvalCorrect(pgv) {
			continue
		}
		out = append(out, lmeBackendDisagreement{
			QuestionID:   cr.QuestionID,
			QuestionType: cr.QuestionType,
			Mem0:         disagreementCell(mem0),
			PGVector:     disagreementCell(pgv),
			Reference:    cr.Answer,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].QuestionID < out[j].QuestionID
	})
	return out
}

func disagreementCell(br *backendResult) string {
	if br == nil {
		return "missing"
	}
	if correct, ok := longMemEvalJudgeCorrect(br); ok {
		stage := evaluatedFailureStage(br, normalizedFailureStage(br), correct, true)
		return fmt.Sprintf("judge=%v EM=%v stage=%s answer=%s", correct, br.ExactMatch, stage, truncate(br.Answer, 80))
	}
	return fmt.Sprintf("EM=%v stage=%s answer=%s", br.ExactMatch, normalizedFailureStage(br), truncate(br.Answer, 80))
}

func evaluatedLongMemEvalCorrect(br *backendResult) bool {
	if correct, ok := longMemEvalJudgeCorrect(br); ok {
		return correct
	}
	return br != nil && br.ExactMatch
}

func lowestF1Rows(rows []lmeAnalysisRow, limit int) []lmeAnalysisRow {
	filtered := make([]lmeAnalysisRow, 0, len(rows))
	for _, row := range rows {
		if row.EvaluatedCorrect {
			continue
		}
		filtered = append(filtered, row)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].F1 != filtered[j].F1 {
			return filtered[i].F1 < filtered[j].F1
		}
		if filtered[i].QuestionID != filtered[j].QuestionID {
			return filtered[i].QuestionID < filtered[j].QuestionID
		}
		return filtered[i].Backend < filtered[j].Backend
	})
	if limit > 0 && len(filtered) > limit {
		return filtered[:limit]
	}
	return filtered
}

func answerGapDiagnosis(questionType, answer, reference string) string {
	parts := make([]string, 0, 2)
	missing := missingReferenceKeywords(answer, reference, 8)
	if len(missing) > 0 {
		parts = append(parts, "missing="+strings.Join(missing, ","))
	}
	slots := missingAnswerSlots(questionType, answer, reference)
	if len(slots) > 0 {
		parts = append(parts, "slots="+strings.Join(slots, ","))
	}
	return strings.Join(parts, "; ")
}

func missingReferenceKeywords(answer, reference string, limit int) []string {
	answerSet := tokenFrequency(answer)
	refFreq := tokenFrequency(reference)
	type scoredToken struct {
		Token string
		Count int
	}
	missing := make([]scoredToken, 0)
	for token, count := range refFreq {
		if answerSet[token] == 0 {
			missing = append(missing, scoredToken{Token: token, Count: count})
		}
	}
	sort.Slice(missing, func(i, j int) bool {
		if missing[i].Count != missing[j].Count {
			return missing[i].Count > missing[j].Count
		}
		return missing[i].Token < missing[j].Token
	})
	if limit > 0 && len(missing) > limit {
		missing = missing[:limit]
	}
	out := make([]string, 0, len(missing))
	for _, item := range missing {
		out = append(out, item.Token)
	}
	return out
}

func tokenFrequency(text string) map[string]int {
	out := make(map[string]int)
	for _, token := range normalizedTokens(text) {
		if isAnalysisStopword(token) {
			continue
		}
		out[token]++
	}
	return out
}

func normalizedTokens(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if len(field) < 3 {
			continue
		}
		out = append(out, field)
	}
	return out
}

func isAnalysisStopword(token string) bool {
	_, ok := analysisStopwords[token]
	return ok
}

var analysisStopwords = map[string]struct{}{
	"about": {}, "after": {}, "also": {}, "and": {}, "any": {}, "are": {},
	"because": {}, "been": {}, "but": {}, "can": {}, "could": {}, "did": {},
	"does": {}, "for": {}, "from": {}, "had": {}, "has": {}, "have": {},
	"her": {}, "him": {}, "his": {}, "how": {}, "into": {}, "may": {},
	"not": {}, "off": {}, "one": {}, "only": {}, "out": {}, "over": {},
	"own": {}, "should": {}, "some": {}, "such": {}, "than": {}, "that": {},
	"the": {}, "their": {}, "them": {}, "there": {}, "these": {}, "they": {},
	"this": {}, "those": {}, "through": {}, "too": {}, "user": {}, "was": {},
	"were": {}, "what": {}, "when": {}, "where": {}, "which": {}, "while": {},
	"who": {}, "why": {}, "will": {}, "with": {}, "would": {}, "you": {},
	"your": {},
}

func missingAnswerSlots(questionType, answer, reference string) []string {
	if !strings.Contains(questionType, "preference") {
		return nil
	}
	answer = strings.ToLower(answer)
	reference = strings.ToLower(reference)
	slots := make([]string, 0)
	if strings.Contains(reference, "would prefer") && !strings.Contains(answer, "would prefer") {
		slots = append(slots, "positive_preference")
	}
	if containsAny(reference, []string{"would also appreciate", "appreciate"}) &&
		!containsAny(answer, []string{"would also appreciate", "appreciate"}) {
		slots = append(slots, "secondary_preference")
	}
	if containsAny(reference, []string{"would not prefer", "may not prefer", "not prefer"}) &&
		!containsAny(answer, []string{"would not prefer", "may not prefer", "not prefer"}) {
		slots = append(slots, "negative_preference")
	}
	return slots
}

func containsAny(text string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func tsvCell(s string) string {
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

func mdCell(s string) string {
	s = tsvCell(s)
	s = strings.ReplaceAll(s, "|", "\\|")
	if s == "" {
		return " "
	}
	return s
}
