#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCREEN_NAME="${BKCAM_SCREEN_NAME:-bkcam_gate}"
PORT="${BKCAM_PORT:-8088}"
CONFIG="${BKCAM_CONFIG:-$ROOT/bkcam-server/config.json}"
LOG="${BKCAM_LOG:-/tmp/bkcam-go.log}"
PIDFILE="${BKCAM_PIDFILE:-$ROOT/.bkcam.pid}"
BIN="$ROOT/bkcam-go/bkcam"

have() {
  command -v "$1" >/dev/null 2>&1
}

screen_running() {
  have screen && screen -ls 2>/dev/null | grep -q "[.]${SCREEN_NAME}[[:space:]]"
}

pidfile_running() {
  [[ -f "$PIDFILE" ]] || return 1
  local pid
  pid="$(tr -cd '0-9' < "$PIDFILE")"
  [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null
}

port_in_use() {
  if have lsof; then
    lsof -nP -iTCP:"$PORT" -sTCP:LISTEN >/dev/null 2>&1
  elif have ss; then
    ss -ltnp "sport = :$PORT" 2>/dev/null | awk 'NR > 1 { found = 1 } END { exit !found }'
  elif have fuser; then
    fuser -n tcp "$PORT" >/dev/null 2>&1 || fuser "${PORT}/tcp" >/dev/null 2>&1
  else
    return 1
  fi
}

show_port_owner() {
  if have lsof; then
    lsof -nP -iTCP:"$PORT" -sTCP:LISTEN >&2 || true
  elif have ss; then
    ss -ltnp "sport = :$PORT" >&2 || true
  elif have fuser; then
    fuser -v -n tcp "$PORT" >&2 || fuser -v "${PORT}/tcp" >&2 || true
  fi
}

if screen_running; then
  echo "bkcam already has a screen session: $SCREEN_NAME"
  exit 0
fi

if pidfile_running; then
  echo "bkcam already has a pidfile process: $(cat "$PIDFILE")"
  exit 0
fi
[[ -f "$PIDFILE" ]] && rm -f "$PIDFILE"

if port_in_use; then
  echo "port $PORT is already in use:" >&2
  show_port_owner
  exit 1
fi

if [[ ! -x "$BIN" || "$ROOT/bkcam-go/main.go" -nt "$BIN" ]]; then
  (cd "$ROOT/bkcam-go" && go build -o bkcam .)
fi

mkdir -p "$(dirname "$LOG")"
if have screen; then
  BKCAM_CONFIG="$CONFIG" screen -dmS "$SCREEN_NAME" bash -lc 'cd "$1" && exec "$2" >> "$3" 2>&1' _ "$ROOT/bkcam-go" "$BIN" "$LOG"
  MODE="screen:$SCREEN_NAME"
else
  (
    cd "$ROOT/bkcam-go"
    BKCAM_CONFIG="$CONFIG" nohup "$BIN" >> "$LOG" 2>&1 &
    echo $! > "$PIDFILE"
  )
  sleep 0.2
  if ! pidfile_running; then
    echo "bkcam failed to start; check $LOG" >&2
    exit 1
  fi
  MODE="nohup pid:$(cat "$PIDFILE")"
fi

echo "bkcam started"
echo "mode: $MODE"
echo "url: http://127.0.0.1:$PORT"
echo "log: $LOG"
