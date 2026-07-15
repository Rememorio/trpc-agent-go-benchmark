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
modfile="${script_dir}/go.mod"
sumfile="${script_dir}/go.sum"
go_mod_flags=()
go_build_flags=()
temp_dir=""

cleanup() {
  if [[ -n "${temp_dir}" ]]; then
    rm -rf -- "${temp_dir}"
  fi
}
trap cleanup EXIT

if [[ -n "$(git -C "${repo_root}" status --porcelain --untracked-files=normal)" ]]; then
  echo "LongMemEval formal runs require a clean benchmark worktree." >&2
  echo "Commit the experiment code or use a clean Git worktree first." >&2
  exit 1
fi

build_profile="${LME_AGENT_PROFILE:-}"
if [[ -z "${build_profile}" ]]; then
  if [[ -n "${LME_AGENT_REPLACEMENT:-}" ]]; then
    build_profile="upstream"
  else
    build_profile="candidate"
  fi
fi
case "${build_profile}" in
  candidate)
    ;;
  upstream)
    go_build_flags=(-tags=lme_upstream)
    ;;
  *)
    echo "LME_AGENT_PROFILE must be candidate or upstream." >&2
    exit 1
    ;;
esac

if [[ -n "${LME_AGENT_REPLACEMENT:-}" ]]; then
  replacement="${LME_AGENT_REPLACEMENT}"
  replacement_path="${replacement%@*}"
  replacement_version="${replacement##*@}"
  if [[ "${replacement_path}" == "${replacement}" ||
        -z "${replacement_path}" || -z "${replacement_version}" ]]; then
    echo "LME_AGENT_REPLACEMENT must be a module path and version separated by @." >&2
    exit 1
  fi

  temp_dir="$(mktemp -d)"
  modfile="${temp_dir}/longmemeval.mod"
  sumfile="${temp_dir}/longmemeval.sum"
  cp -- "${script_dir}/go.mod" "${modfile}"
  cp -- "${script_dir}/go.sum" "${sumfile}"
  GOWORK=off go mod edit -modfile="${modfile}" \
    -replace="trpc.group/trpc-go/trpc-agent-go=${replacement_path}@${replacement_version}" \
    -replace="trpc.group/trpc-go/trpc-agent-go/memory/pgvector=${replacement_path}/memory/pgvector@${replacement_version}" \
    -replace="trpc.group/trpc-go/trpc-agent-go/session/pgvector=${replacement_path}/session/pgvector@${replacement_version}" \
    -replace="trpc.group/trpc-go/trpc-agent-go/storage/postgres=${replacement_path}/storage/postgres@${replacement_version}"
  GOWORK=off go mod download -modfile="${modfile}" all
  go_mod_flags=(-modfile="${modfile}")
fi

read -r manifest_sha _ < <(sha256sum "${modfile}")
read -r sum_sha _ < <(sha256sum "${sumfile}")
ldflags="-X=main.lmeInjectedBuildRevision=${revision} -X=main.lmeInjectedBuildModified=false"
ldflags+=" -X=main.lmeInjectedModuleManifestSHA256=${manifest_sha}"
ldflags+=" -X=main.lmeInjectedModuleSumSHA256=${sum_sha}"
ldflags+=" -X=main.lmeInjectedBuildProfile=${build_profile}"
cd "${script_dir}"
GOWORK=off go run -mod=readonly "${go_build_flags[@]}" \
  "${go_mod_flags[@]}" -ldflags "${ldflags}" . "$@"
