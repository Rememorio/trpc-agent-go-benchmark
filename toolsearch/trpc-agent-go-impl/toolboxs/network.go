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

// --- Network tool input types ---

type httpGetInput struct {
	URL     string            `json:"url" jsonschema:"description=Full URL to send the GET request to, including scheme (http/https)"`
	Headers map[string]string `json:"headers,omitempty" jsonschema:"description=Optional map of HTTP header names to values to include in the request"`
}

type httpPostInput struct {
	URL         string            `json:"url" jsonschema:"description=Full URL to send the POST request to, including scheme (http/https)"`
	Body        string            `json:"body" jsonschema:"description=Request body content as a string, typically JSON encoded"`
	ContentType string            `json:"content_type,omitempty" jsonschema:"description=Content-Type header value, e.g. 'application/json' or 'application/x-www-form-urlencoded'"`
	Headers     map[string]string `json:"headers,omitempty" jsonschema:"description=Optional map of additional HTTP header names to values"`
}

type downloadFileInput struct {
	URL        string `json:"url" jsonschema:"description=URL of the file to download"`
	OutputPath string `json:"output_path" jsonschema:"description=Local file path where the downloaded content will be saved"`
}

type uploadFileInput struct {
	Path string `json:"path" jsonschema:"description=Local file path of the file to upload"`
	URL  string `json:"url" jsonschema:"description=Remote URL endpoint to upload the file to, must accept multipart/form-data"`
}

type checkURLStatusInput struct {
	URL string `json:"url" jsonschema:"description=Full URL to check for reachability, including scheme (http/https)"`
}

// NetworkToolbox returns the network deferred-tool namespace.
func NetworkToolbox() toolsearch.Toolbox {
	return toolsearch.Toolbox{
		Name:        "network",
		Description: "Make HTTP requests, call REST APIs, upload and download files over the internet, and check URL reachability. Supports GET and POST with custom headers and content types.",
		Tools: []tool.Tool{
			stubTool[httpGetInput]("http_get",
				"Send an HTTP GET request to a URL and return the response status code, headers, and body. Supports custom request headers for authentication or content negotiation."),
			stubTool[httpPostInput]("http_post",
				"Send an HTTP POST request with a string body to a URL. Supports setting Content-Type (e.g. application/json) and custom headers. Returns the response status code, headers, and body."),
			stubTool[downloadFileInput]("download_file",
				"Download a file from a remote URL and save it to a specified local path. Handles large files with streaming. Returns the local file path and size on success."),
			stubTool[uploadFileInput]("upload_file",
				"Upload a local file to a remote URL endpoint via multipart/form-data POST. Returns the server response status and body."),
			stubTool[checkURLStatusInput]("check_url_status",
				"Check whether a URL is reachable by sending an HTTP HEAD request. Returns the HTTP status code, response time in milliseconds, and whether the URL is accessible."),
		},
	}
}
