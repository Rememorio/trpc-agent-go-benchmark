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

// --- Filesystem tool input types ---

type readFileInput struct {
	Path string `json:"path" jsonschema:"description=Absolute or relative path to the file to read"`
}

type writeFileInput struct {
	Path    string `json:"path" jsonschema:"description=Absolute or relative path where the file will be written"`
	Content string `json:"content" jsonschema:"description=Text content to write into the file"`
}

type appendFileInput struct {
	Path    string `json:"path" jsonschema:"description=Absolute or relative path to the target file"`
	Content string `json:"content" jsonschema:"description=Text content to append to the end of the file"`
}

type deleteFileInput struct {
	Path string `json:"path" jsonschema:"description=Absolute or relative path to the file to delete permanently"`
}

type moveFileInput struct {
	Source      string `json:"source" jsonschema:"description=Current path of the file to move or rename"`
	Destination string `json:"destination" jsonschema:"description=Target path after the move or rename operation"`
}

type copyFileInput struct {
	Source      string `json:"source" jsonschema:"description=Path of the source file to copy"`
	Destination string `json:"destination" jsonschema:"description=Destination path where the copy will be placed"`
}

type listDirectoryInput struct {
	Path      string `json:"path" jsonschema:"description=Directory path to list contents of"`
	Recursive bool   `json:"recursive,omitempty" jsonschema:"description=Whether to recursively list subdirectories, defaults to false"`
}

type createDirectoryInput struct {
	Path string `json:"path" jsonschema:"description=Directory path to create, including any missing parent directories"`
}

type searchFileContentInput struct {
	Pattern   string `json:"pattern" jsonschema:"description=Text or regex pattern to search for inside files"`
	Path      string `json:"path" jsonschema:"description=Directory path to search within"`
	Recursive bool   `json:"recursive,omitempty" jsonschema:"description=Whether to search recursively in subdirectories, defaults to false"`
}

type findFilesInput struct {
	Path    string `json:"path" jsonschema:"description=Base directory path to start the search from"`
	Pattern string `json:"pattern" jsonschema:"description=File name or glob pattern to match, e.g. '*.go' or 'README.md'"`
}

type getFileInfoInput struct {
	Path string `json:"path" jsonschema:"description=Absolute or relative path to the file or directory to inspect"`
}

// FilesystemToolbox returns the filesystem deferred-tool namespace.
func FilesystemToolbox() toolsearch.Toolbox {
	return toolsearch.Toolbox{
		Name:        "filesystem",
		Description: "Read, write, move, copy, delete and search files and directories on the local disk. Includes directory listing, recursive traversal, content search with grep-style patterns, and file metadata inspection.",
		Tools: []tool.Tool{
			stubTool[readFileInput]("read_file",
				"Read the full contents of a text file at a given path. Returns the file content as a string. Supports UTF-8 encoded files."),
			stubTool[writeFileInput]("write_file",
				"Write or overwrite text content to a file at a given path. Creates parent directories if they do not exist. Existing file content will be replaced."),
			stubTool[appendFileInput]("append_file",
				"Append text content to the end of an existing file. Creates the file if it does not already exist. Useful for logging or incremental writes."),
			stubTool[deleteFileInput]("delete_file",
				"Permanently delete a file from disk at the specified path. This operation cannot be undone. Does not delete directories."),
			stubTool[moveFileInput]("move_file",
				"Move or rename a file from one path to another. Can also be used to move a file into a different directory while keeping its name, or rename it in place."),
			stubTool[copyFileInput]("copy_file",
				"Copy a file from the source path to a new destination path. The original file remains unchanged. Parent directories of the destination are created if needed."),
			stubTool[listDirectoryInput]("list_directory",
				"List the files and subdirectories inside a directory. Supports optional recursive listing of all nested subdirectories. Returns file names, sizes, and types."),
			stubTool[createDirectoryInput]("create_directory",
				"Create a new directory at the specified path, including any missing parent directories (equivalent to mkdir -p). Does nothing if the directory already exists."),
			stubTool[searchFileContentInput]("search_file_content",
				"Search file contents for a text or regex pattern recursively under a directory. Similar to grep -r. Returns matching file paths and the matched line content."),
			stubTool[findFilesInput]("find_files",
				"Find files by name or glob pattern under a directory. Supports wildcard patterns like '*.go', 'test_*', etc. Returns relative file paths of all matches."),
			stubTool[getFileInfoInput]("get_file_info",
				"Get metadata for a file or directory at the given path. Returns size in bytes, permissions (octal), last modified timestamp, and whether the path is a file or directory."),
		},
	}
}
