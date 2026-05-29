#!/usr/bin/env bash
#
# Download LongMemEval data for summary benchmarking.
#

set -euo pipefail

DATASET_SELECTOR="${1:-longmemeval}"
DATA_DIR="${2:-.}"

print_usage() {
    cat <<'EOF'
Usage:
  ./download_datasets.sh [longmemeval|lme] [data_dir]

Examples:
  ./download_datasets.sh
  ./download_datasets.sh longmemeval
  ./download_datasets.sh lme ./data
EOF
}

normalize_selector() {
    case "${1,,}" in
        longmemeval|lme|all|"")
            echo "longmemeval"
            ;;
        *)
            return 1
            ;;
    esac
}

download_longmemeval() {
    local target_dir="$1/longmemeval-cleaned"
    if [ -f "$target_dir/longmemeval_s_cleaned.json" ] && [ "$(wc -c < "$target_dir/longmemeval_s_cleaned.json")" -gt 1000 ]; then
        echo "LongMemEval already exists at $target_dir, skipping."
        return
    fi

    echo "=== Downloading LongMemEval ==="
    mkdir -p "$target_dir"

    local base_url="https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned/resolve/main"
    local files=("longmemeval_s_cleaned.json" "longmemeval_oracle.json")

    for file in "${files[@]}"; do
        echo "  Downloading $file..."
        if ! wget -q "$base_url/$file" -O "$target_dir/$file"; then
            echo "Warning: failed to download $file"
            rm -f "$target_dir/$file"
        fi
    done

    if [ -f "$target_dir/longmemeval_s_cleaned.json" ]; then
        echo "LongMemEval downloaded to $target_dir"
    else
        echo "Warning: failed to download LongMemEval automatically."
        echo "Manual source: https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned"
    fi
}

print_dataset_info() {
    local base_dir="$1"

    if [ -f "$base_dir/longmemeval-cleaned/longmemeval_s_cleaned.json" ]; then
        echo
        echo "LongMemEval:"
        echo "  Location: $base_dir/longmemeval-cleaned"
        echo "  Files: longmemeval_s_cleaned.json, longmemeval_oracle.json"
    fi
}

SELECTOR="$(normalize_selector "$DATASET_SELECTOR")" || {
    print_usage
    exit 1
}

mkdir -p "$DATA_DIR"
echo "Data directory: $DATA_DIR"

case "$SELECTOR" in
    longmemeval)
        download_longmemeval "$DATA_DIR"
        ;;
esac

print_dataset_info "$DATA_DIR"

echo
echo "=== Usage Example ==="
echo "LongMemEval:"
echo "  cd summary/trpc-agent-go-impl"
echo "  PGVECTOR_DSN=\"postgres://...\" go run . -dataset ../data/longmemeval-cleaned/longmemeval_s_cleaned.json -dataset-format longmemeval -lme-question-types single-session-user -num-cases 70 -events 40 -lme-visible-events 20 -detailed-prompt=true"
