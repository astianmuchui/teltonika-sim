#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

EXT=""
case "${OSTYPE:-}" in
msys* | cygwin* | win32) EXT=".exe" ;;
esac

BIN="./teltonika-sim${EXT}"
PORT="${PORT:-5027}"
DEVICES="${DEVICES:-10}"
DURATION="${DURATION:-60s}"
SERVE_LOG="${SERVE_LOG:-serve.log}"
RUN_LOG="${RUN_LOG:-run.log}"
SERVE_ARGS="${SERVE_ARGS:-}"
RUN_ARGS="${RUN_ARGS:-}"

go build -o "${BIN}" ./cmd/teltonika-sim

# shellcheck disable=SC2086
"${BIN}" serve --listen ":${PORT}" --duration "${DURATION}" ${SERVE_ARGS} >"${SERVE_LOG}" 2>&1 &
SERVE_PID=$!

cleanup() {
	kill "${SERVE_PID}" 2>/dev/null || true
	wait "${SERVE_PID}" 2>/dev/null || true
}
trap cleanup EXIT

sleep 1
if ! kill -0 "${SERVE_PID}" 2>/dev/null; then
	echo "serve exited before it could accept anything, see ${SERVE_LOG}:" >&2
	tail -5 "${SERVE_LOG}" >&2
	echo "is something else already bound to :${PORT}? set PORT to a free one." >&2
	exit 1
fi
echo "serve is listening on :${PORT}, logging to ${SERVE_LOG}"
echo "running ${DEVICES} devices for ${DURATION}, logging to ${RUN_LOG}"

# shellcheck disable=SC2086
"${BIN}" run --server "127.0.0.1:${PORT}" --devices "${DEVICES}" --duration "${DURATION}" ${RUN_ARGS} >"${RUN_LOG}" 2>&1 || true

wait "${SERVE_PID}" 2>/dev/null || true
trap - EXIT

echo
echo "================ run summary ================"
awk '/^---- teltonika-sim run/{found=NR} {lines[NR]=$0} END{for(i=found;i<=NR;i++) print lines[i]}' "${RUN_LOG}"
echo
echo "================ serve summary ================"
awk '/^---- teltonika-sim serve/{found=NR} {lines[NR]=$0} END{for(i=found;i<=NR;i++) print lines[i]}' "${SERVE_LOG}"
