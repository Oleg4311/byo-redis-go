// Собирает демо для GitHub Pages в каталог site/: wasm, wasm_exec.js и страница.
import { execFileSync } from 'node:child_process';
import { copyFileSync, existsSync, mkdirSync, rmSync } from 'node:fs';
import { join } from 'node:path';

rmSync('site', { recursive: true, force: true });
mkdirSync('site');

execFileSync('go', ['build', '-o', join('site', 'byo-redis.wasm'), './cmd/wasm'], {
  stdio: 'inherit',
  env: { ...process.env, GOOS: 'js', GOARCH: 'wasm' },
});

const goroot = execFileSync('go', ['env', 'GOROOT'], { encoding: 'utf8' }).trim();
// В Go 1.24+ файл лежит в lib/wasm, в более старых версиях в misc/wasm
const candidates = [join(goroot, 'lib', 'wasm', 'wasm_exec.js'), join(goroot, 'misc', 'wasm', 'wasm_exec.js')];
const execJs = candidates.find(existsSync);
if (!execJs) throw new Error('wasm_exec.js не найден в GOROOT');
copyFileSync(execJs, join('site', 'wasm_exec.js'));
copyFileSync(join('web', 'index.html'), join('site', 'index.html'));
console.log('site/ готов');
