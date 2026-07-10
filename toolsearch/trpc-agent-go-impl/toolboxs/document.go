//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolboxs

import (
	"trpc.group/trpc-go/trpc-agent-go/plugin/toolsearch"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// --- Document tool input types ---

type createDocumentInput struct {
	Title   string `json:"title" jsonschema:"description=Title of the new document"`
	Content string `json:"content" jsonschema:"description=Body content of the document in plain text or markdown format"`
	Format  string `json:"format,omitempty" jsonschema:"description=Format of the document content: 'markdown' or 'plaintext', defaults to 'markdown'"`
}

type exportPdfInput struct {
	Path       string `json:"path" jsonschema:"description=Absolute or relative path to the source document file to convert"`
	OutputPath string `json:"output_path" jsonschema:"description=Desired path for the generated PDF output file"`
}

type convertMarkdownToHTMLInput struct {
	Path string `json:"path" jsonschema:"description=Absolute or relative path to the markdown document to convert"`
}

type summarizeDocumentInput struct {
	Path      string `json:"path" jsonschema:"description=Absolute or relative path to the document to summarize"`
	MaxLength int    `json:"max_length,omitempty" jsonschema:"description=Maximum number of words for the generated summary, defaults to 200"`
}

type translateDocumentInput struct {
	Path           string `json:"path" jsonschema:"description=Absolute or relative path to the source document to translate"`
	TargetLanguage string `json:"target_language" jsonschema:"description=Target language code, e.g. 'zh', 'en', 'ja', 'fr', 'de'"`
}

type extractDocumentTextInput struct {
	Path string `json:"path" jsonschema:"description=Absolute or relative path to the PDF or Word document to extract text from"`
}

type mergeDocumentsInput struct {
	Paths      []string `json:"paths" jsonschema:"description=List of file paths to merge, in the desired order"`
	OutputPath string   `json:"output_path" jsonschema:"description=Output file path for the merged result document"`
}

type getDocumentOutlineInput struct {
	Path string `json:"path" jsonschema:"description=Absolute or relative path to the document to extract the heading outline from"`
}

// DocumentToolbox returns the document deferred-tool namespace.
func DocumentToolbox() toolsearch.Toolbox {
	return toolsearch.Toolbox{
		Name:        "document",
		Description: "Create, convert, summarize, translate and export documents and reports. Supports markdown, plain text, PDF, and Word formats. Includes merging, text extraction, and heading outline inspection.",
		Tools: []tool.Tool{
			stubTool[createDocumentInput]("create_document",
				"Create a new document with a title and body content. Supports markdown and plain text formats. Returns the created document's file path."),
			stubTool[exportPdfInput]("export_pdf",
				"Export a markdown or text document to a PDF file at the specified output path. Handles formatting and pagination automatically."),
			stubTool[convertMarkdownToHTMLInput]("convert_markdown_to_html",
				"Convert a markdown document into a self-contained HTML file. Preserves headings, lists, code blocks, links, images, and tables."),
			stubTool[summarizeDocumentInput]("summarize_document",
				"Generate a concise summary of a long document. Returns key points and main conclusions. Max length controls the word count of the summary output."),
			stubTool[translateDocumentInput]("translate_document",
				"Translate the contents of a document into another language. Returns the translated document saved to a new file with the language suffix appended."),
			stubTool[extractDocumentTextInput]("extract_document_text",
				"Extract plain text content from a PDF or Word (.docx) document. Returns the raw text as a string, stripping all formatting and embedded images."),
			stubTool[mergeDocumentsInput]("merge_documents",
				"Combine multiple documents (markdown or text) into a single output file. Documents are merged in the order provided. A separator is inserted between each document."),
			stubTool[getDocumentOutlineInput]("get_document_outline",
				"Extract the heading outline (table of contents) from a markdown or HTML document. Returns a nested list of headings with their levels and corresponding text."),
		},
	}
}
