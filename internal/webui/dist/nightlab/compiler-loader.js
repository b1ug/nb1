// The support runtime is bundled locally with the exact Go compiler version.
// Importing this module does not instantiate WASM or discover/open devices.
import './compiler/wasm_exec.js';

const methods = new Set(['info', 'planSequence']);
let loading;
let runtimeFailure;

export class CompilerError extends Error {
  constructor(code, message, cause) {
    super(message, cause === undefined ? undefined : {cause});
    this.name = 'CompilerError';
    this.code = code;
  }
}

async function startCompiler() {
  if (typeof globalThis.Go !== 'function') {
    throw new CompilerError('compiler_unavailable', 'The bundled Go runtime could not start.');
  }
  const response = await fetch(new URL('./compiler/nb1.wasm', import.meta.url));
  if (!response.ok) {
    throw new CompilerError('compiler_unavailable', 'The local Go compiler could not be loaded.');
  }
  const go = new globalThis.Go();
  // ArrayBuffer also works when an extension's local file MIME type differs.
  const {instance} = await WebAssembly.instantiate(await response.arrayBuffer(), go.importObject);
  let timeout;
  const ready = new Promise((resolve, reject) => {
    timeout = setTimeout(() => reject(new CompilerError('compiler_unavailable', 'The local Go compiler did not become ready.')), 10000);
    globalThis.nb1GoOnReady = () => resolve(globalThis.nb1Go);
  });
  try {
    // Go stays alive to serve callbacks. Waiting for run() to finish would block
    // startup forever. No device connection or background heartbeat is created.
    void go.run(instance).then(() => {
      runtimeFailure = new CompilerError('compiler_stopped', 'The Go compiler stopped. Reload this page before trying again.');
    }, cause => {
      runtimeFailure = new CompilerError('compiler_stopped', 'The Go compiler stopped. Reload this page before trying again.', cause);
    });
    const api = await ready;
    if (!api || [...methods].some(method => typeof api[method] !== 'function')) {
      throw new CompilerError('invalid_response', 'The bundled compiler interface is incomplete.');
    }
    return api;
  } finally {
    clearTimeout(timeout);
    delete globalThis.nb1GoOnReady;
  }
}

/** Load the bundled compiler once in this Window or MV3 service worker. */
export function loadCompiler() {
  if (runtimeFailure) return Promise.reject(runtimeFailure);
  if (!loading) {
    loading = startCompiler().catch(cause => {
      if (cause instanceof CompilerError) throw cause;
      throw new CompilerError('compiler_unavailable', 'The local Go compiler could not start.', cause);
    });
  }
  return loading;
}

/** Return the unwrapped result, or throw CompilerError with the original error. */
export async function callCompiler(method, raw = '', aliases = '') {
  if (!methods.has(method)) throw new CompilerError('invalid_argument', 'Unknown compiler method.');
  if (typeof raw !== 'string') throw new CompilerError('invalid_argument', 'Compiler input must be a JSON string.');
  if (aliases !== null && typeof aliases === 'object' && !Array.isArray(aliases)) aliases = JSON.stringify(aliases);
  if (typeof aliases !== 'string') throw new CompilerError('invalid_argument', 'Color aliases must be a JSON object or JSON string.');
  const api = await loadCompiler();
  if (runtimeFailure) throw runtimeFailure;
  let response;
  try {
    response = JSON.parse(api[method](raw, aliases));
  } catch (cause) {
    throw new CompilerError('invalid_response', 'The Go compiler returned an invalid response.', cause);
  }
  if (response?.ok === false && typeof response.error?.message === 'string') {
    throw new CompilerError(response.error.code || 'invalid_input', response.error.message);
  }
  if (response?.ok !== true || !Object.hasOwn(response, 'result')) {
    throw new CompilerError('invalid_response', 'The Go compiler returned an invalid response.');
  }
  return response.result;
}
