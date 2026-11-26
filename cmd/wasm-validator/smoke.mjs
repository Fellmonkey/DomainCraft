// Runs every WASM export in Node using the Go runtime shim. This is the same
// binary the Studio loads; the only difference is the host. Run after building:
//   GOOS=js GOARCH=wasm go build -o bin/validate.wasm ./cmd/wasm-validator
//   node cmd/wasm-validator/smoke.mjs bin/validate.wasm
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';

const wasmPath = path.resolve(process.argv[2] ?? 'bin/validate.wasm');
const goroot = process.env.GOROOT ?? execFileSync('go', ['env', 'GOROOT'], { encoding: 'utf8' }).trim();
const execCandidates = [
  path.join(goroot, 'lib', 'wasm', 'wasm_exec.js'),
  path.join(goroot, 'misc', 'wasm', 'wasm_exec.js'),
];
const execPath = execCandidates.find((p) => fs.existsSync(p));
if (!execPath) throw new Error(`wasm_exec.js not found under ${goroot}`);

// wasm_exec.js is an IIFE that installs Go on globalThis.
(0, eval)(fs.readFileSync(execPath, 'utf8'));
if (typeof globalThis.Go !== 'function') throw new Error('wasm_exec.js did not define Go');

const go = new globalThis.Go();
const bytes = fs.readFileSync(wasmPath);
const { instance } = await WebAssembly.instantiate(bytes, go.importObject);
void go.run(instance); // main blocks on a channel; exports are installed first.
await new Promise((resolve) => setImmediate(resolve));

const required = ['goValidate', 'goParseField', 'goParseDomain', 'goSpecmeta', 'goVersion'];
for (const name of required) {
  if (typeof globalThis[name] !== 'function') throw new Error(`missing WASM export: ${name}`);
}

const yaml = `project:
  name: Bench
entities:
  User:
    fields:
      id: uuid [primary]
      email: string [required, unique, email]
      password: string [required, hidden]
auth:
  type: jwt
  roles: [Admin, User]
  entity: User
`;

function check(name, ok, detail) {
  console.log(`${ok ? 'ok' : 'FAIL'}  ${name}${detail ? ` — ${detail}` : ''}`);
  if (!ok) process.exitCode = 1;
}

const validation = JSON.parse(globalThis.goValidate(yaml));
check('goValidate', Array.isArray(validation.errors) && validation.errors.length === 0 && validation.parse === false);

const field = JSON.parse(globalThis.goParseField('string [required, max:255]', 'title'));
check('goParseField', field.Name === 'title' && field.Type === 'string' && field.Validations.max === '255');

const domain = JSON.parse(globalThis.goParseDomain(yaml));
check('goParseDomain', domain.project.name === 'Bench' && !!domain.entities.User);

const specmeta = JSON.parse(globalThis.goSpecmeta());
check('goSpecmeta', Array.isArray(specmeta.fieldTypes) && specmeta.fieldTypes.length > 0);

if (typeof globalThis.goGenerate === 'function') {
  const generated = JSON.parse(globalThis.goGenerate(yaml, 'admin-alpine', ''));
  const index = generated.files?.find((f) => f.path === 'admin/index.html');
  check('goGenerate', !generated.error && !!index && index.content.includes('Bench'),
    generated.error || `${generated.files?.length ?? 0} files`);
} else {
  console.log('skip  goGenerate — this build predates the export');
}

const version = globalThis.goVersion();
check('goVersion', typeof version === 'string' && version.length > 0, version);

// Time each export from the JS side: this includes the string boundary crossing
// that native Go benchmarks cannot see.
let timeCounter = 0;
function time(name, fn, iterations) {
  // Warm up long enough that cached and uncached variants reach the same
  // steady state; a short warmup penalizes the variant that allocates less.
  for (let i = 0; i < 200; i++) fn();
  const start = performance.now();
  for (let i = 0; i < iterations; i++) fn();
  const ms = (performance.now() - start) / iterations;
  console.log(`time ${name.padEnd(14)} ${ms.toFixed(4)} ms/call  (${iterations} calls)`);
}

time('goValidate', () => globalThis.goValidate(yaml), 200);
time('goValidate-changed', () => globalThis.goValidate(yaml.replace('Bench', 'Other' + (timeCounter++ % 2))), 200);
time('goParseField', () => globalThis.goParseField('string [required, max:255]', 'title'), 200);
time('goParseDomain', () => globalThis.goParseDomain(yaml), 200);
time('goParseDomain-changed', () => globalThis.goParseDomain(yaml.replace('Bench', 'Other' + (timeCounter++ % 2))), 200);
if (typeof globalThis.goGenerate === 'function') {
  time('goGenerate', () => globalThis.goGenerate(yaml, 'admin-alpine', ''), 30);
}
time('goSpecmeta', () => globalThis.goSpecmeta(), 200);
time('goVersion', () => globalThis.goVersion(), 200);

// Studio pattern: validate and parse raw with the same YAML back-to-back.
time('studio-cycle', () => {
  globalThis.goValidate(yaml);
  globalThis.goParseDomain(yaml);
}, 200);

