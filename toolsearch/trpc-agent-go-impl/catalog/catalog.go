//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package catalog defines the deferred-tool catalog exercised by the toolsearch
// benchmark. It mirrors the self-defined namespace catalog used by the
// plugin's integration accuracy test (plugin/toolsearch/accuracy_test.go): a set
// of business namespaces (filesystem, git, document, process, network, iam, crm),
// a block of general-purpose no-namespace tools, and a small always-on preset.
//
// Every tool is metadata only — a name plus a one-line description. Execution is
// stubbed (a canned JSON reply), so no tool makes a real call during the
// benchmark. The tool-trajectory metric only checks WHICH tools the model chose
// to call, not their results, so stubs are sufficient and keep the only network
// traffic to the model completions themselves.
package catalog

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/plugin/toolsearch"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// metaInput is the permissive, generic input shared by every stub tool. It lets
// the model pass whatever arguments it likes without a schema mismatch; the
// benchmark never inspects them.
type metaInput struct {
	Path   string `json:"path,omitempty"`
	Query  string `json:"query,omitempty"`
	Target string `json:"target,omitempty"`
	Value  string `json:"value,omitempty"`
}

// metaTool builds a no-op function tool carrying just a name and description.
// Its body returns a stubbed reply so it never performs real work.
//
// The reply is deliberately shaped to look like a SUCCESSFUL result — a
// "success" status plus a plausible-looking result string that names the tool
// and echoes the request. This matters for the benchmark's token/latency
// numbers: an empty or obviously-fake reply (e.g. {"status":"ok"}) leaves the
// model unsatisfied, so it keeps trying other tools in a runaway loop (one turn
// once ballooned to 35 tool calls / 332K tokens). A result that reads as "the
// task is done" lets the model stop after a single call, which is the behavior a
// real tool would produce and keeps the cross-mode comparison clean. The
// tool-trajectory metric only checks WHICH tool was chosen, not the result text,
// so the canned content does not affect correctness scoring.
func metaTool(name, desc string) tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in metaInput) (map[string]any, error) {
			return map[string]any{
				"status": "success",
				"tool":   name,
				"result": fmt.Sprintf("%s completed successfully.", name),
			}, nil
		},
		function.WithName(name),
		function.WithDescription(desc),
	)
}

// Toolboxes returns the deferred-tool namespaces under test. The tools are
// intentionally distinct in capability but share generic verbs (read, list,
// search, get, create, delete, update) across namespaces, so namespace scoping
// is exercised — e.g. delete_user (iam) vs delete_customer (crm).
func Toolboxes() []toolsearch.Toolbox {
	return []toolsearch.Toolbox{
		{
			Name:        "filesystem",
			Description: "read, write, move and search files and directories on the local disk",
			Tools: []tool.Tool{
				metaTool("read_file", "Read the full contents of a file at a given path."),
				metaTool("write_file", "Write or overwrite text content to a file at a given path."),
				metaTool("append_file", "Append text content to the end of an existing file."),
				metaTool("delete_file", "Permanently delete a file from disk."),
				metaTool("move_file", "Move or rename a file from one path to another."),
				metaTool("copy_file", "Copy a file to a new location."),
				metaTool("list_directory", "List the files and subdirectories inside a directory."),
				metaTool("create_directory", "Create a new directory, including parent directories."),
				metaTool("search_file_content", "Search file contents for a text pattern (grep-style)."),
				metaTool("find_files", "Find files by name or glob pattern under a directory."),
				metaTool("get_file_info", "Get metadata for a file: size, permissions, modified time."),
			},
		},
		{
			Name:        "git",
			Description: "version control operations on a git repository: status, commits, branches, history",
			Tools: []tool.Tool{
				metaTool("git_status", "Show the working tree status: staged, modified and untracked files."),
				metaTool("git_diff", "Show the diff of unstaged or staged changes."),
				metaTool("git_commit", "Create a commit from the staged changes with a message."),
				metaTool("git_add", "Stage files for the next commit."),
				metaTool("git_log", "Show the commit history of the repository."),
				metaTool("git_branch", "List, create or delete branches."),
				metaTool("git_checkout", "Switch branches or restore working tree files."),
				metaTool("git_merge", "Merge another branch into the current branch."),
				metaTool("git_push", "Push local commits to a remote repository."),
				metaTool("git_pull", "Fetch and integrate changes from a remote repository."),
				metaTool("git_stash", "Stash away uncommitted changes for later."),
				metaTool("git_blame", "Show who last modified each line of a file."),
			},
		},
		{
			Name:        "document",
			Description: "create, convert, summarize and export documents and reports",
			Tools: []tool.Tool{
				metaTool("create_document", "Create a new text or markdown document."),
				metaTool("export_pdf", "Export a document to a PDF file."),
				metaTool("convert_markdown_to_html", "Convert a markdown document into HTML."),
				metaTool("summarize_document", "Generate a concise summary of a long document."),
				metaTool("translate_document", "Translate a document into another language."),
				metaTool("extract_document_text", "Extract plain text from a PDF or Word document."),
				metaTool("merge_documents", "Combine multiple documents into a single file."),
				metaTool("get_document_outline", "Extract the heading outline of a document."),
			},
		},
		{
			Name:        "process",
			Description: "run shell commands and manage operating-system processes",
			Tools: []tool.Tool{
				metaTool("run_command", "Execute a shell command and capture its output."),
				metaTool("list_processes", "List currently running processes."),
				metaTool("kill_process", "Terminate a running process by its PID."),
				metaTool("get_env_var", "Read the value of an environment variable."),
				metaTool("set_env_var", "Set the value of an environment variable for the session."),
			},
		},
		{
			Name:        "network",
			Description: "make HTTP requests, call APIs, upload and download files over the internet, check URL reachability",
			Tools: []tool.Tool{
				metaTool("http_get", "Send an HTTP GET request to a URL and return the response."),
				metaTool("http_post", "Send an HTTP POST request with a body to a URL."),
				metaTool("download_file", "Download a file from a URL to a local path."),
				metaTool("upload_file", "Upload a local file to a remote URL."),
				metaTool("check_url_status", "Check whether a URL is reachable and its status code."),
			},
		},
		{
			Name:        "iam",
			Description: "identity and access management: manage user accounts, roles and permissions",
			Tools: []tool.Tool{
				metaTool("create_user", "Create a new user account in the identity system."),
				metaTool("delete_user", "Permanently delete a user account from the identity system."),
				metaTool("list_users", "List all user accounts in the identity system."),
				metaTool("update_user", "Update properties of an existing user account."),
				metaTool("get_user", "Get details of a specific user account."),
				metaTool("grant_role", "Grant a role to a user account."),
				metaTool("revoke_role", "Revoke a role from a user account."),
			},
		},
		{
			Name:        "crm",
			Description: "customer relationship management: manage customers, contacts and sales leads",
			Tools: []tool.Tool{
				metaTool("create_customer", "Create a new customer record in the CRM system."),
				metaTool("delete_customer", "Permanently delete a customer record from the CRM system."),
				metaTool("list_customers", "List all customer records in the CRM system."),
				metaTool("update_customer", "Update properties of an existing customer record."),
				metaTool("get_customer", "Get details of a specific customer record."),
				metaTool("add_contact", "Add a new contact person to a customer record."),
				metaTool("remove_contact", "Remove a contact person from a customer record."),
			},
		},
	}
}

// DefaultTools returns general-purpose deferred tools that do NOT belong to any
// business namespace. They are registered via WithDeferredTools (no namespace),
// so the model must find them with keyword search alone. This validates the
// non-toolbox path (keyword → _default namespace fallback).
func DefaultTools() []tool.Tool {
	return []tool.Tool{
		metaTool("calculator", "Evaluate an arithmetic expression and return the result."),
		metaTool("get_current_time", "Get the current system time in a specified timezone."),
		metaTool("generate_qrcode", "Generate a QR code image from text or a URL."),
		metaTool("base64_encode", "Encode a string to base64."),
		metaTool("base64_decode", "Decode a base64-encoded string back to plain text."),
		metaTool("parse_json", "Parse a JSON string and extract values by path."),
		metaTool("format_date", "Format a date string from one format to another."),
		metaTool("generate_uuid", "Generate a random UUID v4."),
	}
}

// PresetTools are always advertised to the model (never deferred). They stand in
// for the small always-on toolset a real agent keeps loaded.
func PresetTools() []tool.Tool {
	return []tool.Tool{
		metaTool("web_search", "Search the web for up-to-date information and return relevant results."),
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
