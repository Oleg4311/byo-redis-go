// Проверка WASM-сборки без браузера: грузит byo-redis.wasm через wasm_exec.js и выполняет команды.
// Использование: node scripts/wasm-smoke.mjs <каталог-с-wasm_exec.js-и-byo-redis.wasm>
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { join, resolve } from 'node:path';

const dir = resolve(process.argv[2] ?? 'site');
createRequire(import.meta.url)(join(dir, 'wasm_exec.js'));
const go = new Go();
const { instance } = await WebAssembly.instantiate(readFileSync(join(dir, 'byo-redis.wasm')), go.importObject);
go.run(instance);

const run = (line) => globalThis.byoRedisExec(line);
const steps = [
  ['PING', 'PONG'],
  ['SET k "hello world" EX 100', 'OK'],
  ['GET k', '"hello world"'],
  ['TTL k', '(integer) 100'],
  ['RPUSH l a b', '(integer) 2'],
  ['LRANGE l 0 -1', '1) "a"\n2) "b"'],
  ['LPUSH k x', '(error) WRONGTYPE Operation against a key holding the wrong kind of value'],
  ['MULTI', 'OK'],
  ['INCR n', 'QUEUED'],
  ['EXEC', '1) (integer) 1'],
  ['SET q "unclosed', '(error) ERR Protocol error: unbalanced quotes in request'],
  ['', ''],
];
for (const [cmd, want] of steps) assert.equal(run(cmd), want, cmd);
globalThis.byoRedisReset();
assert.equal(run('GET k'), '(nil)', 'после reset база пуста');
console.log(`wasm-smoke: ${steps.length} команд, OK`);
