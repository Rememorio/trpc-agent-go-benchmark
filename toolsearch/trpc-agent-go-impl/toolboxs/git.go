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

// --- Git tool input types ---

type gitStatusInput struct {
	Path string `json:"path" jsonschema:"description=Path to the git repository root directory"`
}

type gitDiffInput struct {
	Path   string `json:"path" jsonschema:"description=Path to the git repository root directory"`
	Staged bool   `json:"staged,omitempty" jsonschema:"description=Show staged changes instead of unstaged, defaults to false"`
	File   string `json:"file,omitempty" jsonschema:"description=Limit diff to a specific file path within the repository"`
}

type gitCommitInput struct {
	Path    string `json:"path" jsonschema:"description=Path to the git repository root directory"`
	Message string `json:"message" jsonschema:"description=Commit message describing the changes being committed"`
}

type gitAddInput struct {
	Path  string   `json:"path" jsonschema:"description=Path to the git repository root directory"`
	Files []string `json:"files" jsonschema:"description=List of file paths to stage for the next commit, use '.' to stage all"`
}

type gitLogInput struct {
	Path   string `json:"path" jsonschema:"description=Path to the git repository root directory"`
	Count  int    `json:"count,omitempty" jsonschema:"description=Number of recent commits to show, defaults to 10"`
	Branch string `json:"branch,omitempty" jsonschema:"description=Show log for a specific branch, defaults to current branch"`
}

type gitBranchInput struct {
	Path   string `json:"path" jsonschema:"description=Path to the git repository root directory"`
	Action string `json:"action,omitempty" jsonschema:"description=Operation to perform: list (default), create, or delete"`
	Name   string `json:"name,omitempty" jsonschema:"description=Name of the branch to create or delete, required when action is create or delete"`
}

type gitCheckoutInput struct {
	Path   string `json:"path" jsonschema:"description=Path to the git repository root directory"`
	Branch string `json:"branch" jsonschema:"description=Name of the branch or commit to switch to"`
}

type gitMergeInput struct {
	Path   string `json:"path" jsonschema:"description=Path to the git repository root directory"`
	Branch string `json:"branch" jsonschema:"description=Name of the branch to merge into the current branch"`
}

type gitPushInput struct {
	Path   string `json:"path" jsonschema:"description=Path to the git repository root directory"`
	Remote string `json:"remote,omitempty" jsonschema:"description=Name of the remote to push to, defaults to 'origin'"`
	Branch string `json:"branch,omitempty" jsonschema:"description=Name of the branch to push, defaults to current branch"`
}

type gitPullInput struct {
	Path   string `json:"path" jsonschema:"description=Path to the git repository root directory"`
	Remote string `json:"remote,omitempty" jsonschema:"description=Name of the remote to pull from, defaults to 'origin'"`
	Branch string `json:"branch,omitempty" jsonschema:"description=Name of the branch to pull, defaults to current tracking branch"`
}

type gitStashInput struct {
	Path   string `json:"path" jsonschema:"description=Path to the git repository root directory"`
	Action string `json:"action,omitempty" jsonschema:"description=Stash operation: push (save changes, default), pop (restore and remove), or list (show stashes)"`
}

type gitBlameInput struct {
	Path string `json:"path" jsonschema:"description=Path to the git repository root directory"`
	File string `json:"file" jsonschema:"description=Relative path of the file within the repository to show blame annotations for"`
}

// GitToolbox returns the git deferred-tool namespace.
func GitToolbox() toolsearch.Toolbox {
	return toolsearch.Toolbox{
		Name:        "git",
		Description: "Version control operations on a git repository: view status, diff changes, stage and commit files, manage branches, merge, push, pull, stash, and inspect line-by-line blame history.",
		Tools: []tool.Tool{
			stubTool[gitStatusInput]("git_status",
				"Show the working tree status of a git repository. Displays staged, modified, and untracked files. Roughly equivalent to 'git status'."),
			stubTool[gitDiffInput]("git_diff",
				"Show line-by-line differences of unstaged or staged changes in a git repository. Can be scoped to a single file. Equivalent to 'git diff'."),
			stubTool[gitCommitInput]("git_commit",
				"Create a new commit from the currently staged changes with the provided commit message. Equivalent to 'git commit -m <message>'."),
			stubTool[gitAddInput]("git_add",
				"Stage one or more files for the next commit. Use '.' to stage all changes. Equivalent to 'git add <files>'."),
			stubTool[gitLogInput]("git_log",
				"Show the commit history of the repository. Returns commit hashes, authors, timestamps, and messages. Supports limiting result count and filtering by branch."),
			stubTool[gitBranchInput]("git_branch",
				"List existing branches, create a new branch, or delete an existing branch. Use action='list' (default), 'create', or 'delete'."),
			stubTool[gitCheckoutInput]("git_checkout",
				"Switch to a different branch or restore working tree files by checking out a specific branch name or commit hash."),
			stubTool[gitMergeInput]("git_merge",
				"Merge the specified branch into the current branch. May result in merge conflicts that need manual resolution."),
			stubTool[gitPushInput]("git_push",
				"Push local commits from a branch to a remote repository. Defaults to remote 'origin' and the current branch."),
			stubTool[gitPullInput]("git_pull",
				"Fetch and integrate changes from a remote repository into the current local branch. Equivalent to 'git pull'."),
			stubTool[gitStashInput]("git_stash",
				"Stash away uncommitted changes for later use. Supports push (save), pop (restore and remove), and list operations."),
			stubTool[gitBlameInput]("git_blame",
				"Show line-by-line annotations of who last modified each line in a file, including commit hash, author, and timestamp. Equivalent to 'git blame'."),
		},
	}
}
