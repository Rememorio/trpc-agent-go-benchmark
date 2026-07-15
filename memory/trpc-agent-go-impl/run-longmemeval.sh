#!/usr/bin/env bash
#
# Tencent is pleased to support the open source community by making
# trpc-agent-go available.
#
# Copyright (C) 2025 Tencent.  All rights reserved.
#
# trpc-agent-go is licensed under the Apache License Version 2.0.

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(git -C "${script_dir}" rev-parse --show-toplevel)"
revision="$(git -C "${repo_root}" rev-parse HEAD)"

if [[ -n "$(git -C "${repo_root}" status --porcelain --untracked-files=normal)" ]]; then
  echo "LongMemEval formal runs require a clean benchmark worktree." >&2
  echo "Commit the experiment code or use a clean Git worktree first." >&2
  exit 1
fi

ldflags="-X=main.lmeInjectedBuildRevision=${revision} -X=main.lmeInjectedBuildModified=false"
cd "${script_dir}"
exec go run -ldflags "${ldflags}" . "$@"
