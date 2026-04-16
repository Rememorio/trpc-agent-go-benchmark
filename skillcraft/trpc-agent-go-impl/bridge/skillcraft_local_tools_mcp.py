#!/usr/bin/env python3
"""Expose selected SkillCraft local tools as an MCP stdio server."""

from __future__ import annotations

import argparse
import asyncio
import contextlib
import inspect
import io
import json
import os
import sys
from typing import Any


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--skillcraft-root", required=True)
    parser.add_argument("--workspace", required=True)
    parser.add_argument("--toolset", action="append", default=[])
    return parser.parse_args()


def import_skillcraft_tools(skillcraft_root: str):
    skillcraft_root = os.path.abspath(skillcraft_root)
    if skillcraft_root not in sys.path:
        sys.path.insert(0, skillcraft_root)

    captured = io.StringIO()
    try:
        with contextlib.redirect_stdout(captured), contextlib.redirect_stderr(captured):
            from utils.roles.task_agent import local_tool_mappings
            from utils.aux_tools.basic import (
                tool_file_append,
                tool_file_write_json_chunk,
            )
    except Exception as exc:  # pragma: no cover - import errors are fatal
        logs = captured.getvalue().strip()
        message = f"failed to import SkillCraft tool mappings: {exc}"
        if logs:
            message += f"\nCaptured import logs:\n{logs}"
        raise RuntimeError(message) from exc
    return local_tool_mappings, tool_file_append, tool_file_write_json_chunk


def select_tools(
    requested_toolsets: list[str],
    local_tool_mappings: dict[str, Any],
    chunk_tools: list[Any],
) -> list[Any]:
    selected = []

    for toolset_name in requested_toolsets:
        toolset_name = toolset_name.strip()
        if not toolset_name:
            continue
        if toolset_name == "skill_cache":
            # The benchmark uses trpc-agent-go managed skills instead of
            # SkillCraft's native skill cache.
            continue
        if toolset_name not in local_tool_mappings:
            raise ValueError(f"unknown SkillCraft local toolset: {toolset_name}")
        entry = local_tool_mappings[toolset_name]
        if isinstance(entry, list):
            selected.extend(entry)
        else:
            selected.append(entry)

    if "claim_done" in requested_toolsets:
        selected.extend(chunk_tools)

    deduped = []
    seen_names = set()
    for tool in selected:
        name = getattr(tool, "name", "").strip()
        if not name or name in seen_names:
            continue
        deduped.append(tool)
        seen_names.add(name)
    return deduped


def json_safe(value: Any) -> Any:
    return json.loads(json.dumps(value, ensure_ascii=False, default=str))


def normalize_result(value: Any, types_module) -> Any:
    if isinstance(value, dict):
        return json_safe(value)
    if value is None:
        return [types_module.TextContent(type="text", text="null")]
    if isinstance(value, str):
        return [types_module.TextContent(type="text", text=value)]
    return [
        types_module.TextContent(
            type="text",
            text=json.dumps(json_safe(value), ensure_ascii=False, indent=2),
        )
    ]


async def build_and_run_server(args: argparse.Namespace) -> None:
    local_tool_mappings, tool_file_append, tool_file_write_json_chunk = import_skillcraft_tools(
        args.skillcraft_root
    )

    from agents.tool import RunContextWrapper
    from mcp import types
    from mcp.server import Server
    from mcp.server.stdio import stdio_server

    os.makedirs(args.workspace, exist_ok=True)
    tool_defs = select_tools(
        args.toolset,
        local_tool_mappings,
        [tool_file_append, tool_file_write_json_chunk],
    )
    tool_by_name = {tool.name: tool for tool in tool_defs}
    shared_context = {
        "workspace_path": os.path.abspath(args.workspace),
        "_agent_workspace": os.path.abspath(args.workspace),
        "_claim_done_called": False,
    }

    server = Server(
        name="skillcraft-local-tools",
        instructions="Selected SkillCraft local tools exposed over MCP stdio.",
    )

    @server.list_tools()
    async def list_tools():
        return [
            types.Tool(
                name=tool.name,
                description=getattr(tool, "description", "") or "",
                inputSchema=getattr(tool, "params_json_schema", None) or {"type": "object", "properties": {}},
            )
            for tool in tool_defs
        ]

    @server.call_tool(validate_input=True)
    async def call_tool(tool_name: str, arguments: dict[str, Any]):
        tool = tool_by_name[tool_name]
        wrapper = RunContextWrapper(shared_context)
        captured = io.StringIO()
        try:
            with contextlib.redirect_stdout(captured), contextlib.redirect_stderr(captured):
                result = tool.on_invoke_tool(
                    wrapper,
                    json.dumps(arguments or {}, ensure_ascii=False),
                )
                if inspect.isawaitable(result):
                    result = await result
        except Exception as exc:
            logs = captured.getvalue().strip()
            message = str(exc)
            if logs:
                message += f"\nCaptured tool logs:\n{logs[-2000:]}"
            raise RuntimeError(message) from exc
        return normalize_result(result, types)

    async with stdio_server() as (read_stream, write_stream):
        await server.run(
            read_stream,
            write_stream,
            server.create_initialization_options(),
        )


def main() -> None:
    args = parse_args()
    asyncio.run(build_and_run_server(args))


if __name__ == "__main__":
    main()
