// The host side of polyglottify.go: loads the WebAssembly module and wraps its
// exports. The module needs no JavaScript runtime, only the few WASI imports
// that TinyGo and the standard toolchain's runtimes call, stubbed here.

export interface Options {
	doctype: string;
	xmlDecl: string;
	xmlSyntax: boolean;
	scripting: boolean;
	strict: boolean;
	finalNewline: boolean;
	indent: string;
	indentPrefix?: string;
	maxTokenSize?: number;
	maxDepth?: number;
	maxStackBytes?: number;
	maxRawText?: number;
}

export interface Warning {
	msg: string;
	line: number;
	col: number;
	index: number;
}

export interface Result {
	output: string;
	warnings: Warning[];
	error?: Warning | { msg: string; line?: undefined };
}

export interface Converter {
	/** The default limits (maxTokenSize, maxDepth, maxStackBytes, maxRawText). */
	defaults: Record<string, number>;
	convert(input: string, options: Options): Result;
}

interface Exports {
	memory: WebAssembly.Memory;
	_initialize(): void;
	alloc(n: number): number;
	release(p: number): void;
	defaults(): number;
	polyglottify(input: number, inputLen: number, options: number, optionsLen: number): number;
}

const enc = new TextEncoder(), dec = new TextDecoder();
const ENOSYS = 52, EBADF = 8;

// The options as polyglottify.go reads them: flags, the limits as uint32 (0 for
// the default; JS numbers are truncated and capped to what the module's int can
// hold), and the length-prefixed strings.
const flags = ["strict", "finalNewline", "xmlSyntax", "scripting"] as const;
const limits = ["maxDepth", "maxRawText", "maxTokenSize", "maxStackBytes"] as const;
const strings = ["indent", "indentPrefix", "doctype", "xmlDecl"] as const;
function encodeOptions(o: Options) {
	const s = strings.map(k => enc.encode(typeof o[k] == "string" ? o[k] : ""));
	const buf = new Uint8Array(1 + 4 * limits.length + s.reduce((n, b) => n + 4 + b.length, 0)), dv = new DataView(buf.buffer);
	buf[0] = flags.reduce((v, k, i) => v | (o[k] ? 1 << i : 0), 0);
	limits.forEach((k, i) => {
		const v = typeof o[k] == "number" ? Math.trunc(o[k]) : 0;
		dv.setUint32(1 + 4 * i, v > 0 ? Math.min(v, 0x7fffffff) : 0, true);
	});
	let p = 1 + 4 * limits.length;
	for (const b of s) {
		dv.setUint32(p, b.length, true);
		buf.set(b, p + 4);
		p += 4 + b.length;
	}
	return buf;
}

// Instantiates the module, which may have been built by TinyGo (which needs
// args and random_get) or the standard toolchain (which needs a few more).
// Everything else fails with ENOSYS.
async function instantiate(module: WebAssembly.Module) {
	let x!: Exports, dv: DataView;
	// The memory can grow (and be replaced) during any call into the module.
	const view = () => dv?.buffer == x.memory.buffer ? dv : (dv = new DataView(x.memory.buffer));
	const wasi: Record<string, (...args: number[]) => number> = {};
	for (const { module: m, name } of WebAssembly.Module.imports(module)) {
		if (m != "wasi_snapshot_preview1") throw new Error(`unexpected import ${m}.${name}`);
		wasi[name] = () => ENOSYS;
	}
	// The sizes of the arguments and environment, which are empty.
	wasi.args_sizes_get = wasi.environ_sizes_get = (n, size) => (view().setUint32(n, 0, true), view().setUint32(size, 0, true), 0);
	// There are no preopened directories.
	wasi.fd_prestat_get = () => EBADF;
	wasi.clock_time_get = (_id, _precision, out) => (view().setBigUint64(out, BigInt(Math.round(performance.now() * 1e6)), true), 0);
	wasi.random_get = (p, n) => (crypto.getRandomValues(new Uint8Array(x.memory.buffer, p, n)), 0);
	wasi.proc_exit = code => { throw new Error(`wasm exited with code ${code}`); };

	x = (await WebAssembly.instantiate(module, { wasi_snapshot_preview1: wasi })).exports as unknown as Exports;
	x._initialize();
	return { x, view };
}

// The compiled module, from the script which the build adds after the page's.
declare const wasmReady: Promise<WebAssembly.Module>;

/** Instantiates the module, once it has been received. */
export async function load(): Promise<Converter> {
	const { x, view } = await instantiate(await wasmReady);

	// Runs fn with the pointer to a copy of each of the buffers in the module's
	// memory. Reading after the calls re-views the memory, since it can grow.
	const run = (fn: (...ptrs: number[]) => number, ...bufs: Uint8Array[]) => {
		const ptrs: number[] = [];
		try {
			for (const b of bufs) {
				const p = x.alloc(b.length);
				ptrs.push(p);
				new Uint8Array(x.memory.buffer, p, b.length).set(b);
			}
			const r = fn(...ptrs);
			const n = view().getUint32(r, true);
			return JSON.parse(dec.decode(new Uint8Array(x.memory.buffer, r + 4, n)));
		} finally {
			for (const p of ptrs) x.release(p);
		}
	};

	const defaults = run(() => x.defaults());
	return {
		defaults,
		convert(input, options) {
			const i = enc.encode(input), o = encodeOptions(options);
			return run((ip, op) => x.polyglottify(ip, i.length, op, o.length), i, o);
		},
	};
}
