#!/usr/bin/env bash
# run_test.sh — Cross-language integration test for sketchlib.
#
# Builds and runs:
#   1. Go producer  (sketchlib-go)   — writes 9 .pb sketch files into a tmp dir
#   2. Rust consumer (sketchlib-rust) — reads those files, deserialises each sketch,
#      runs sanity queries, and asserts correctness
#
# Sketch types covered:
#   CountMin · KLL · DDSketch · HLL · CountSketch · CocoSketch ·
#   ElasticSketch · UnivMon · HydraSketch
#
# Usage (run from anywhere inside the repo):
#   sketchlib-go/tests/cross_language/run_test.sh
#   cd sketchlib-go/tests/cross_language && ./run_test.sh
#
# Options (environment variables):
#   KEEP_TMP=1      — do not delete the .pb output directory on exit
#   VERBOSE=1       — pass -v to go build / cargo build
#   TMP_DIR=<path>  — use an explicit output directory instead of mktemp
#
# Exit code: 0 = all checks passed, non-zero = at least one check failed.

set -euo pipefail

# ---------------------------------------------------------------------------
# Resolve paths
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# sketchlib-go root is two levels up from tests/cross_language/
GO_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
# sketchlib-rust sits next to sketchlib-go
REPO_ROOT="$(cd "$GO_DIR/.." && pwd)"
RUST_DIR="$REPO_ROOT/sketchlib-rust"

# ---------------------------------------------------------------------------
# Temporary directory for .pb files
# ---------------------------------------------------------------------------
if [[ -n "${TMP_DIR:-}" ]]; then
    mkdir -p "$TMP_DIR"
    _KEEP_TMP=1
else
    TMP_DIR="$(mktemp -d)"
    _KEEP_TMP="${KEEP_TMP:-0}"
fi

cleanup() {
    if [[ "$_KEEP_TMP" == "0" ]]; then
        rm -rf "$TMP_DIR"
    else
        echo ""
        echo "Protobuf files kept at: $TMP_DIR"
    fi
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Logging helpers
# ---------------------------------------------------------------------------
BOLD=$'\033[1m'
GREEN=$'\033[0;32m'
RED=$'\033[0;31m'
CYAN=$'\033[0;36m'
RESET=$'\033[0m'

header() { echo ""; echo "${BOLD}${CYAN}${1}${RESET}"; echo ""; }
ok()     { echo "${GREEN}[OK]${RESET}  $*"; }
err()    { echo "${RED}[ERR]${RESET} $*" >&2; }
step()   { echo "  ${BOLD}>${RESET} $*"; }

BUILD_FLAGS=""
if [[ "${VERBOSE:-0}" == "1" ]]; then
    BUILD_FLAGS="-v"
fi

# ---------------------------------------------------------------------------
# Sanity-check prerequisites
# ---------------------------------------------------------------------------
header "sketchlib Cross-Language Integration Test"
echo "  Go dir   : $GO_DIR"
echo "  Rust dir : $RUST_DIR"
echo "  PB dir   : $TMP_DIR"

for cmd in go cargo; do
    if ! command -v "$cmd" &>/dev/null; then
        err "Required tool not found: $cmd"
        exit 1
    fi
done

# ---------------------------------------------------------------------------
# Phase 1 — Go producer
# ---------------------------------------------------------------------------
header "Phase 1 — Go producer (sketchlib-go)"

PRODUCER_BIN="$TMP_DIR/xtest_producer"

step "go build ./cmd/xtest_producer"
(
    cd "$GO_DIR"
    go build $BUILD_FLAGS -o "$PRODUCER_BIN" ./cmd/xtest_producer
)
ok "Built $PRODUCER_BIN"

step "Run producer → $TMP_DIR"
"$PRODUCER_BIN" "$TMP_DIR"

echo ""
step "Produced .pb files:"
ls -lh "$TMP_DIR"/*.pb 2>/dev/null || { err "No .pb files produced"; exit 1; }

EXPECTED_FILES=(
    countmin.pb kll.pb ddsketch.pb hll.pb countsketch.pb
    coco.pb elastic.pb univmon.pb hydra.pb
)
MISSING=0
for f in "${EXPECTED_FILES[@]}"; do
    if [[ ! -f "$TMP_DIR/$f" ]]; then
        err "Missing expected file: $f"
        MISSING=1
    fi
done
if [[ "$MISSING" != "0" ]]; then exit 1; fi
ok "All 9 sketch files present"

# ---------------------------------------------------------------------------
# Phase 2 — Rust consumer
# ---------------------------------------------------------------------------
header "Phase 2 — Rust consumer (sketchlib-rust)"

step "cargo test --test xtest_consumer"
(
    cd "$RUST_DIR"
    if [[ "${VERBOSE:-0}" == "1" ]]; then
        XTEST_DIR="$TMP_DIR" cargo test --test xtest_consumer -- --nocapture
    else
        XTEST_DIR="$TMP_DIR" cargo test --test xtest_consumer -- --nocapture 2>&1 | grep -E '^(error|warning\[|   Compiling|    Finished|test |FAILED|ok$)' || true
    fi
)

# ---------------------------------------------------------------------------
# Done
# ---------------------------------------------------------------------------
header "Result"
echo "${BOLD}${GREEN}All cross-language checks PASSED.${RESET}"
echo ""
