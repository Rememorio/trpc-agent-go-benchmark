//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolboxs

import "trpc.group/trpc-go/trpc-agent-go/tool"

// --- Default / General-purpose tool input types ---

type calculatorInput struct {
	Expression string `json:"expression" jsonschema:"description=Arithmetic or mathematical expression to evaluate, e.g. '2 + 3 * 4' or 'sqrt(16) + sin(pi/2)'"`
}

type getCurrentTimeInput struct {
	Timezone string `json:"timezone,omitempty" jsonschema:"description=IANA timezone name, e.g. 'Asia/Shanghai', 'America/New_York', 'UTC'. Defaults to system timezone."`
}

type generateQRCodeInput struct {
	Content string `json:"content" jsonschema:"description=Text or URL to encode into the QR code image"`
	Size    int    `json:"size,omitempty" jsonschema:"description=Width and height of the generated QR code image in pixels, defaults to 256"`
}

type base64EncodeInput struct {
	Input string `json:"input" jsonschema:"description=Plain text string to encode into base64 format"`
}

type base64DecodeInput struct {
	Input string `json:"input" jsonschema:"description=Base64-encoded string to decode back to plain text"`
}

type parseJSONInput struct {
	JSONString string `json:"json_string" jsonschema:"description=Valid JSON string to parse and extract values from"`
	Path       string `json:"path" jsonschema:"description=Dot-separated path to the desired value, e.g. 'user.address.city' or 'items[0].name'"`
}

type formatDateInput struct {
	DateString string `json:"date_string" jsonschema:"description=Input date string to parse and reformat"`
	FromFormat string `json:"from_format" jsonschema:"description=Format of the input date string, using Go-style reference time layout, e.g. '2006-01-02' or '2006-01-02T15:04:05Z07:00'"`
	ToFormat   string `json:"to_format" jsonschema:"description=Desired output date format, using Go-style reference time layout, e.g. 'January 2, 2006' or '02/01/2006 15:04'"`
}

type generateUUIDInput struct{}

// --- Preset tool input types ---

type webSearchInput struct {
	Query      string `json:"query" jsonschema:"description=Search query string for finding relevant web pages"`
	NumResults int    `json:"num_results,omitempty" jsonschema:"description=Maximum number of search results to return, defaults to 5, maximum 20"`
}

// DefaultTools returns general-purpose deferred tools that do NOT belong to any
// business namespace. They are registered via WithDeferredTools (no namespace),
// so the model must find them with keyword search alone. This validates the
// non-toolbox path (keyword → _default namespace fallback).
func DefaultTools() []tool.Tool {
	return []tool.Tool{
		stubTool[calculatorInput]("calculator",
			"Evaluate a mathematical expression and return the computed result. Supports basic arithmetic (+, -, *, /), parentheses, exponentiation (^), and common math functions (sqrt, sin, cos, log, abs)."),
		stubTool[getCurrentTimeInput]("get_current_time",
			"Get the current system date and time, optionally in a specified IANA timezone like 'Asia/Shanghai' or 'America/New_York'. Returns ISO 8601 formatted timestamp."),
		stubTool[generateQRCodeInput]("generate_qrcode",
			"Generate a QR code image (PNG) from text content or a URL. Returns the QR code as a base64-encoded image string ready for display or embedding."),
		stubTool[base64EncodeInput]("base64_encode",
			"Encode a plain text string into its base64 representation. Useful for encoding binary data or for data transmission over text-based protocols."),
		stubTool[base64DecodeInput]("base64_decode",
			"Decode a base64-encoded string back to its original plain text. Returns an error if the input is not valid base64."),
		stubTool[parseJSONInput]("parse_json",
			"Parse a JSON string and extract a value at a specified dot-separated path. Supports array indexing with bracket notation, e.g. 'users[0].name'."),
		stubTool[formatDateInput]("format_date",
			"Convert a date string from one format to another. Uses Go-style reference time layout specifiers. Handles timezone-aware formats like ISO 8601 and RFC 3339."),
		stubTool[generateUUIDInput]("generate_uuid",
			"Generate a random UUID v4 (universally unique identifier). Returns a string in the standard 8-4-4-4-12 hexadecimal format, e.g. '550e8400-e29b-41d4-a716-446655440000'."),
	}
}

// PresetTools are always advertised to the model (never deferred). They stand in
// for the small always-on toolset a real agent keeps loaded.
func PresetTools() []tool.Tool {
	return []tool.Tool{
		stubTool[webSearchInput]("web_search",
			"Search the web for up-to-date information using a search query. Returns a list of relevant results, each including a title, URL, and brief content snippet."),
	}
}
