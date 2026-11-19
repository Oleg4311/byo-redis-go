#!/usr/bin/env bash
# Проверка совместимости с настоящими redis-cli и redis-benchmark (пакет redis-tools).
# Запуск: scripts/compat.sh  (нужны Go и redis-tools)
set -euo pipefail

PORT=${PORT:-6399}
BIN=$(mktemp -d)/byo-redis
go build -o "$BIN" ./cmd/byo-redis
"$BIN" --addr "127.0.0.1:$PORT" 2>"$BIN.log" &
PID=$!
trap 'kill $PID 2>/dev/null || true' EXIT
for _ in $(seq 50); do (echo > "/dev/tcp/127.0.0.1/$PORT") 2>/dev/null && break; sleep 0.1; done

cli() { redis-cli -h 127.0.0.1 -p "$PORT" "$@"; }
fail=0
check() { # check "ожидаемый вывод" команда...
  local want=$1; shift
  local got; got=$(cli "$@" 2>&1) || true
  if [ "$got" == "$want" ]; then printf 'ok    %s\n' "$*"; else printf 'FAIL  %s\n  ожидалось: %q\n  получено:  %q\n' "$*" "$want" "$got"; fail=1; fi
}

echo "== redis-cli: $(redis-cli --version)"
check PONG PING
check OK SET greeting hello
check hello GET greeting
check 1 INCR counter
check 11 INCRBY counter 10
check 3 RPUSH queue a b c
check $'a\nb\nc' LRANGE queue 0 -1
check 2 HSET user name Ada role admin
check Ada HGET user name
check 2 SADD tags go redis
check 1 SISMEMBER tags go
check "WRONGTYPE Operation against a key holding the wrong kind of value" LPUSH greeting x # redis-cli вне терминала печатает ошибку без префикса "(error)"
check -2 TTL missing
check OK SET temp 1 EX 100
check 100 TTL temp
check string TYPE greeting
check "$(printf 'greeting\ncounter\nqueue\nuser\ntags\ntemp\n' | sort)" KEYS '*'

# Транзакция и режим --pipe (массовая вставка протоколом RESP) — то, чем пользуются реальные инструменты
printf 'MULTI\r\nSET tx1 a\r\nSET tx2 b\r\nEXEC\r\n' | redis-cli -h 127.0.0.1 -p "$PORT" >/dev/null
check a GET tx1
for i in $(seq 1 1000); do printf '*3\r\n$3\r\nSET\r\n$%d\r\nbulk:%d\r\n$1\r\nx\r\n' $((5 + ${#i})) "$i"; done \
  | redis-cli -h 127.0.0.1 -p "$PORT" --pipe | tee /tmp/pipe.out
grep -q "errors: 0, replies: 1000" /tmp/pipe.out || { echo "FAIL  --pipe"; fail=1; }
check x GET bulk:777

echo "== redis-benchmark: $(redis-benchmark --version)"
redis-benchmark -h 127.0.0.1 -p "$PORT" -q -n 20000 -c 20 -t set,get,incr,lpush,rpush,lpop,rpop,sadd,hset
redis-benchmark -h 127.0.0.1 -p "$PORT" -q -n 20000 -c 20 -P 16 -t set,get

[ "$(cli DBSIZE)" -gt 1000 ] || { echo "FAIL  DBSIZE после бенчмарка"; fail=1; }
cat "$BIN.log"
exit $fail
