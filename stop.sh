#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCREEN_NAME="${BKCAM_SCREEN_NAME:-bkcam_gate}"
PORT="${BKCAM_PORT:-8088}"
PIDFILE="${BKCAM_PIDFILE:-$ROOT/.bkcam.pid}"
PIDS=()

have() {
  command -v "$1" >/dev/null 2>&1
}

collect_pids() {
  while IFS= read -r pid; do
    [[ "$pid" =~ ^[0-9]+$ ]] || continue
    [[ "$pid" == "$$" ]] && continue
    PIDS+=("$pid")
  done < <("$@" 2>/dev/null || true)
}

collect_pid_words() {
  local pid
  for pid in $("$@" 2>/dev/null || true); do
    [[ "$pid" =~ ^[0-9]+$ ]] || continue
    [[ "$pid" == "$$" ]] && continue
    PIDS+=("$pid")
  done
}

if have screen && screen -ls 2>/dev/null | grep -q "[.]${SCREEN_NAME}[[:space:]]"; then
  screen -S "$SCREEN_NAME" -X quit 2>/dev/null || true
  sleep 0.3
fi

if [[ -f "$PIDFILE" ]]; then
  pid="$(tr -cd '0-9' < "$PIDFILE")"
  if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
    PIDS+=("$pid")
  fi
fi

if have pgrep; then
  collect_pids pgrep -f "$ROOT/bkcam-go/bkcam"
  collect_pids pgrep -x bkcam
fi

if have lsof; then
  collect_pids lsof -tiTCP:"$PORT" -sTCP:LISTEN
fi

if have ss; then
  collect_pids bash -c "ss -ltnp 'sport = :$PORT' 2>/dev/null | sed -n 's/.*pid=\\([0-9][0-9]*\\).*/\\1/p'"
fi

if have fuser; then
  collect_pid_words fuser -n tcp "$PORT"
  collect_pid_words fuser "${PORT}/tcp"
fi

UNIQUE=()
for pid in "${PIDS[@]}"; do
  kill -0 "$pid" 2>/dev/null || continue
  seen=0
  for existing in "${UNIQUE[@]}"; do
    if [[ "$existing" == "$pid" ]]; then
      seen=1
      break
    fi
  done
  [[ "$seen" == 0 ]] && UNIQUE+=("$pid")
done

if [[ "${#UNIQUE[@]}" -eq 0 ]]; then
  rm -f "$PIDFILE"
  echo "bkcam is not running"
  exit 0
fi

echo "stopping bkcam pids: ${UNIQUE[*]}"
kill "${UNIQUE[@]}" 2>/dev/null || true

for _ in {1..20}; do
  ALIVE=()
  for pid in "${UNIQUE[@]}"; do
    kill -0 "$pid" 2>/dev/null && ALIVE+=("$pid")
  done
  [[ "${#ALIVE[@]}" -eq 0 ]] && {
    rm -f "$PIDFILE"
    echo "bkcam stopped"
    exit 0
  }
  sleep 0.25
done

echo "force killing bkcam pids: ${ALIVE[*]}"
kill -KILL "${ALIVE[@]}" 2>/dev/null || true
rm -f "$PIDFILE"
echo "bkcam stopped"
