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

// --- Process tool input types ---

type runCommandInput struct {
	Command    string   `json:"command" jsonschema:"description=The shell command to execute, e.g. 'ls -la' or 'python script.py'"`
	Args       []string `json:"args,omitempty" jsonschema:"description=Optional list of command-line arguments to pass to the command"`
	WorkingDir string   `json:"working_dir,omitempty" jsonschema:"description=Working directory in which to execute the command, defaults to current directory"`
}

type listProcessesInput struct {
	Filter string `json:"filter,omitempty" jsonschema:"description=Optional filter to narrow results by process name or PID substring"`
}

type killProcessInput struct {
	PID    int    `json:"pid" jsonschema:"description=Process ID (PID) of the process to terminate"`
	Signal string `json:"signal,omitempty" jsonschema:"description=Signal to send: 'SIGTERM' (graceful, default) or 'SIGKILL' (force)"`
}

type getEnvVarInput struct {
	Name string `json:"name" jsonschema:"description=Name of the environment variable to read, e.g. 'PATH' or 'HOME'"`
}

type setEnvVarInput struct {
	Name  string `json:"name" jsonschema:"description=Name of the environment variable to set"`
	Value string `json:"value" jsonschema:"description=Value to assign to the environment variable"`
}

// ProcessToolbox returns the process deferred-tool namespace.
func ProcessToolbox() toolsearch.Toolbox {
	return toolsearch.Toolbox{
		Name:        "process",
		Description: "Execute shell commands and manage operating-system processes. Includes listing running processes, terminating processes by PID, and reading or setting environment variables for the current session.",
		Tools: []tool.Tool{
			stubTool[runCommandInput]("run_command",
				"Execute a shell command and capture its combined stdout and stderr output. Returns the exit code and output text. Supports specifying a working directory and command arguments."),
			stubTool[listProcessesInput]("list_processes",
				"List currently running processes on the system. Optionally filter by process name or PID substring. Returns process ID, name, CPU and memory usage for each match."),
			stubTool[killProcessInput]("kill_process",
				"Terminate a running process by its PID. Sends SIGTERM by default for graceful shutdown; use SIGKILL to force-kill an unresponsive process."),
			stubTool[getEnvVarInput]("get_env_var",
				"Read the current value of an environment variable. Returns an empty string if the variable is not set."),
			stubTool[setEnvVarInput]("set_env_var",
				"Set the value of an environment variable for the current session. The variable will be available to subsequently launched child processes."),
		},
	}
}
