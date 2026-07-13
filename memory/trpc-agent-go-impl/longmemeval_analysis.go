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
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type lmeAnalysisRow struct {
	QuestionID   string
	QuestionType string
	Backend      string
	Stage        string
	ExactMatch   bool
	F1           float64
	BLEU         float64
	Evidence     string
	Error        string
	Answer       string
	Reference    string
	Question     string
	Diagnosis    string
}

type lmeBackendAnalysis struct {
	Cases          int
	ExactMatches   int
	TotalF1        float64
	TotalBLEU      float64
	StageCounts    map[string]int
	EvidenceCounts map[string]int
	ErrorCounts    map[string]int
}

type lmeCompareRow struct {
	QuestionID      string
	QuestionType    string
	Backend         string
	BaselineStage   string
	CandidateStage  string
	BaselineEM      bool
	CandidateEM     bool
	BaselineF1      float64
	CandidateF1     float64
	DeltaF1         float64
	BaselineBLEU    float64
	CandidateBLEU   float64
	DeltaBLEU       float64
	BaselineError   string
	CandidateError  string
	Diagnosis       string
	BaselineAnswer  string
	CandidateAnswer string
	Reference       string
	Question        string
}

type lmeCompareBackendSummary struct {
	Cases        int
	BaselineEM   int
	CandidateEM  int
	TotalDeltaF1 float64
	Improved     int
	Regressed    int
	Unchanged    int
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
	if outputDir == "" {
		outputDir = filepath.Dir(candidatePath)
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create compare output dir: %w", err)
	}
	rows := compareLongMemEvalRows(
		longMemEvalAnalysisRows(baseline),
		longMemEvalAnalysisRows(candidate),
	)
	if err := os.WriteFile(filepath.Join(outputDir, "comparison.tsv"), []byte(formatLongMemEvalComparisonTSV(rows)), 0644); err != nil {
		return fmt.Errorf("write comparison.tsv: %w", err)
	}
	report := formatLongMemEvalComparisonMarkdown(baselinePath, candidatePath, rows)
	if err := os.WriteFile(filepath.Join(outputDir, "comparison.md"), []byte(report), 0644); err != nil {
		return fmt.Errorf("write comparison.md: %w", err)
	}
	fmt.Printf("LongMemEval comparison written to %s\n", outputDir)
	return nil
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
			rows = append(rows, lmeAnalysisRow{
				QuestionID:   cr.QuestionID,
				QuestionType: cr.QuestionType,
				Backend:      backend,
				Stage:        normalizedFailureStage(br),
				ExactMatch:   br.ExactMatch,
				F1:           br.F1,
				BLEU:         br.BLEU,
				Evidence:     evidenceStatus(br.Evidence),
				Error:        br.Error,
				Answer:       br.Answer,
				Reference:    cr.Answer,
				Question:     cr.Question,
				Diagnosis:    answerGapDiagnosis(cr.QuestionType, br.Answer, cr.Answer),
			})
		}
	}
	return rows
}

func compareLongMemEvalRows(baselineRows, candidateRows []lmeAnalysisRow) []lmeCompareRow {
	baseline := make(map[string]lmeAnalysisRow, len(baselineRows))
	candidate := make(map[string]lmeAnalysisRow, len(candidateRows))
	keys := make(map[string]bool)
	for _, row := range baselineRows {
		key := lmeCompareKey(row.QuestionID, row.Backend)
		baseline[key] = row
		keys[key] = true
	}
	for _, row := range candidateRows {
		key := lmeCompareKey(row.QuestionID, row.Backend)
		candidate[key] = row
		keys[key] = true
	}
	out := make([]lmeCompareRow, 0, len(keys))
	for key := range keys {
		base, hasBase := baseline[key]
		cand, hasCand := candidate[key]
		row := lmeCompareRow{}
		if hasCand {
			row.QuestionID = cand.QuestionID
			row.QuestionType = cand.QuestionType
			row.Backend = cand.Backend
			row.CandidateStage = cand.Stage
			row.CandidateEM = cand.ExactMatch
			row.CandidateF1 = cand.F1
			row.CandidateBLEU = cand.BLEU
			row.CandidateError = cand.Error
			row.Diagnosis = cand.Diagnosis
			row.CandidateAnswer = cand.Answer
			row.Reference = cand.Reference
			row.Question = cand.Question
		}
		if hasBase {
			if row.QuestionID == "" {
				row.QuestionID = base.QuestionID
				row.QuestionType = base.QuestionType
				row.Backend = base.Backend
				row.Reference = base.Reference
				row.Question = base.Question
			}
			row.BaselineStage = base.Stage
			row.BaselineEM = base.ExactMatch
			row.BaselineF1 = base.F1
			row.BaselineBLEU = base.BLEU
			row.BaselineError = base.Error
			row.BaselineAnswer = base.Answer
		}
		row.DeltaF1 = row.CandidateF1 - row.BaselineF1
		row.DeltaBLEU = row.CandidateBLEU - row.BaselineBLEU
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
		if row.ExactMatch {
			a.ExactMatches++
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
	b.WriteString("question_id\tquestion_type\tbackend\tstage\texact_match\tf1\tbleu\tevidence\tdiagnosis\terror\tanswer\treference\tquestion\n")
	for _, row := range rows {
		if row.ExactMatch && row.Stage == "ok" {
			continue
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%v\t%.4f\t%.4f\t%s\t%s\t%s\t%s\t%s\t%s\n",
			tsvCell(row.QuestionID),
			tsvCell(row.QuestionType),
			tsvCell(row.Backend),
			tsvCell(row.Stage),
			row.ExactMatch,
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
	b.WriteString("question_id\tquestion_type\tbackend\tbaseline_stage\tcandidate_stage\tbaseline_em\tcandidate_em\tbaseline_f1\tcandidate_f1\tdelta_f1\tbaseline_bleu\tcandidate_bleu\tdelta_bleu\tdiagnosis\tbaseline_answer\tcandidate_answer\treference\tquestion\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%v\t%v\t%.4f\t%.4f\t%+.4f\t%.4f\t%.4f\t%+.4f\t%s\t%s\t%s\t%s\t%s\n",
			tsvCell(row.QuestionID),
			tsvCell(row.QuestionType),
			tsvCell(row.Backend),
			tsvCell(row.BaselineStage),
			tsvCell(row.CandidateStage),
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

func formatLongMemEvalComparisonMarkdown(baselinePath, candidatePath string, rows []lmeCompareRow) string {
	summary := summarizeLongMemEvalCompareRows(rows)
	var b strings.Builder
	b.WriteString("# LongMemEval Comparison\n\n")
	fmt.Fprintf(&b, "- Baseline: `%s`\n", baselinePath)
	fmt.Fprintf(&b, "- Candidate: `%s`\n", candidatePath)
	b.WriteString("- Comparison uses saved `results.json`; no model calls are made.\n\n")

	b.WriteString("## Backend Delta Summary\n\n")
	b.WriteString("| Backend | Cases | EM Baseline | EM Candidate | Avg Delta F1 | Improved | Regressed | Unchanged |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, backend := range sortedCompareBackends(summary) {
		s := summary[backend]
		avgDelta := 0.0
		if s.Cases > 0 {
			avgDelta = s.TotalDeltaF1 / float64(s.Cases)
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %+.4f | %d | %d | %d |\n",
			mdCell(backend), s.Cases, s.BaselineEM, s.CandidateEM,
			avgDelta, s.Improved, s.Regressed, s.Unchanged)
	}

	b.WriteString("\n## Top Improvements\n\n")
	writeCompareRowsTable(&b, topCompareRows(rows, true, 20))
	b.WriteString("\n## Top Regressions\n\n")
	writeCompareRowsTable(&b, topCompareRows(rows, false, 20))
	return b.String()
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
		if row.BaselineEM {
			s.BaselineEM++
		}
		if row.CandidateEM {
			s.CandidateEM++
		}
		s.TotalDeltaF1 += row.DeltaF1
		switch {
		case row.DeltaF1 > 0:
			s.Improved++
		case row.DeltaF1 < 0:
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
		if improvements && row.DeltaF1 <= 0 {
			continue
		}
		if !improvements && row.DeltaF1 >= 0 {
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
	b.WriteString("| Question | Type | Backend | Delta F1 | Baseline | Candidate | Diagnosis |\n")
	b.WriteString("|---|---|---|---:|---|---|---|\n")
	for _, row := range rows {
		fmt.Fprintf(b, "| %s | %s | %s | %+.4f | %s | %s | %s |\n",
			mdCell(row.QuestionID),
			mdCell(row.QuestionType),
			mdCell(row.Backend),
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
	b.WriteString("- Failure stages are computed from saved `results.json`; no model calls are made.\n\n")

	b.WriteString("## Backend Summary\n\n")
	b.WriteString("| Backend | Cases | EM | Avg F1 | Avg BLEU |\n")
	b.WriteString("|---|---:|---:|---:|---:|\n")
	for _, backend := range sortedAnalysisBackends(analysis) {
		a := analysis[backend]
		avgF1, avgBLEU := 0.0, 0.0
		if a.Cases > 0 {
			avgF1 = a.TotalF1 / float64(a.Cases)
			avgBLEU = a.TotalBLEU / float64(a.Cases)
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %.4f | %.4f |\n",
			backend, a.Cases, a.ExactMatches, avgF1, avgBLEU)
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
		if mem0 == nil || pgv == nil || mem0.ExactMatch == pgv.ExactMatch {
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
	return fmt.Sprintf("EM=%v stage=%s answer=%s", br.ExactMatch, normalizedFailureStage(br), truncate(br.Answer, 80))
}

func lowestF1Rows(rows []lmeAnalysisRow, limit int) []lmeAnalysisRow {
	filtered := make([]lmeAnalysisRow, 0, len(rows))
	for _, row := range rows {
		if row.ExactMatch && row.Stage == "ok" {
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
