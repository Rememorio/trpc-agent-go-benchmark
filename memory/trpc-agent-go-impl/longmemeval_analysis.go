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
			})
		}
	}
	return rows
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
	b.WriteString("question_id\tquestion_type\tbackend\tstage\texact_match\tf1\tbleu\tevidence\terror\tanswer\treference\tquestion\n")
	for _, row := range rows {
		if row.ExactMatch && row.Stage == "ok" {
			continue
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%v\t%.4f\t%.4f\t%s\t%s\t%s\t%s\t%s\n",
			tsvCell(row.QuestionID),
			tsvCell(row.QuestionType),
			tsvCell(row.Backend),
			tsvCell(row.Stage),
			row.ExactMatch,
			row.F1,
			row.BLEU,
			tsvCell(row.Evidence),
			tsvCell(row.Error),
			tsvCell(row.Answer),
			tsvCell(row.Reference),
			tsvCell(row.Question),
		)
	}
	return b.String()
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
	b.WriteString("| Question | Type | Backend | Stage | F1 | Evidence | Answer | Reference |\n")
	b.WriteString("|---|---|---|---|---:|---|---|---|\n")
	for _, row := range lowestF1Rows(rows, 20) {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %.4f | %s | %s | %s |\n",
			mdCell(row.QuestionID),
			mdCell(row.QuestionType),
			mdCell(row.Backend),
			mdCell(row.Stage),
			row.F1,
			mdCell(row.Evidence),
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
