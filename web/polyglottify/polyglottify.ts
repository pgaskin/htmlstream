import { EditorState, Compartment, RangeSetBuilder, StateField } from "@codemirror/state";
import { EditorView, keymap, lineNumbers, highlightActiveLineGutter, highlightSpecialChars, drawSelection, dropCursor, rectangularSelection, crosshairCursor, highlightActiveLine, GutterMarker, gutterLineClass } from "@codemirror/view";
import { history, defaultKeymap, historyKeymap, indentWithTab } from "@codemirror/commands";
import { foldGutter, indentOnInput, syntaxHighlighting, defaultHighlightStyle, bracketMatching, foldKeymap } from "@codemirror/language";
import { highlightSelectionMatches, searchKeymap } from "@codemirror/search";
import { html } from "@codemirror/lang-html";
import { lintGutter, setDiagnostics, type Diagnostic } from "@codemirror/lint";
import { MergeView, getChunks } from "@codemirror/merge";
import { duotoneLight, duotoneDark } from "@uiw/codemirror-theme-duotone";
import { load, type Options, type Warning, type Result } from "./polyglottify_wasm";

declare function icon(name: string): string;
declare function isDark(): boolean;
declare function fail(msg: string): void;

const messages = document.getElementById("messages")!, status = document.getElementById("status")!;
const options = ["doctype", "customDoctype", "xmlDecl", "xmlSyntax", "scripting", "strict", "finalNewline", "indent", "maxTokenSize", "maxDepth", "maxStackBytes", "maxRawText"];
const indents: Record<string, string> = { "": "", tab: "\t", "1": " ", "2": "  ", "4": "    " };
const limits = ["maxTokenSize", "maxDepth", "maxStackBytes", "maxRawText"] as const;
let filename = "output.xhtml";

// Only errors of this script's own loading and conversion show the failure
// panel, not any error of the page (extensions cause those too).
function errorText(e: unknown) {
	return e instanceof Error ? e.stack ?? e.message : String(e);
}
let converter: Awaited<ReturnType<typeof load>>;
try {
	converter = await load();
} catch (e) {
	fail(errorText(e));
	throw e;
}

// Shades the gutter of the changed lines, instead of the merge view's narrow
// bar, by adding a class to every gutter element of those lines.
class ChangedMarker extends GutterMarker {
	constructor(elementClass: string) {
		super();
		this.elementClass = elementClass;
	}
}
const changedMarkers = { a: new ChangedMarker("cm-chg-a"), b: new ChangedMarker("cm-chg-b") };
const chunksOf = (state: EditorState) => {
	try {
		return getChunks(state);
	} catch {
		return null;
	}
};
const buildChangedMarkers = (state: EditorState) => {
	const builder = new RangeSetBuilder<ChangedMarker>(), c = chunksOf(state);
	if (!c) return builder.finish();
	for (const chunk of c.chunks) {
		const from = c.side == "a" ? chunk.fromA : chunk.fromB, to = c.side == "a" ? chunk.toA : chunk.toB;
		for (let pos = from; pos < to;) {
			const line = state.doc.lineAt(Math.min(pos, state.doc.length));
			builder.add(line.from, line.from, changedMarkers[c.side!]);
			pos = line.to + 1;
		}
	}
	return builder.finish();
};
const diffGutter = StateField.define({
	create: buildChangedMarkers,
	// The chunks are updated by a later transaction of the same update, so the
	// ones of an edited document are stale until then.
	update: (markers, tr) => chunksOf(tr.startState)?.chunks !== chunksOf(tr.state)?.chunks ? buildChangedMarkers(tr.state) : markers.map(tr.changes),
	provide: f => gutterLineClass.from(f),
});

const theme = new Compartment();
const themeExt = () => theme.of(isDark() ? duotoneDark : duotoneLight);
// The basic setup without autocompletion and bracket and tag auto-closing.
const common = [
	lineNumbers(), highlightActiveLineGutter(), highlightSpecialChars(), history(), foldGutter(), drawSelection(), dropCursor(),
	EditorState.allowMultipleSelections.of(true), indentOnInput(), syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
	bracketMatching(), rectangularSelection(), crosshairCursor(), highlightActiveLine(), highlightSelectionMatches(),
	keymap.of([...defaultKeymap, ...searchKeymap, ...historyKeymap, ...foldKeymap]),
	html({ autoCloseTags: false, matchClosingTags: false }),
	EditorView.lineWrapping,
];
const view = new MergeView({
	a: {
		doc: `<!DOCTYPE html>
<html lang="en">
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Document</title>
<p>
  Lorem ipsum dolor sit amet consectetur adipisicing elit. Odit
  consequuntur non eveniet enim rerum ex, saepe quaerat, natus fuga
  commodi praesentium. Quidem facere ducimus dolore obcaecati odit
  assumenda natus cum.
</p>
`,
		extensions: [common, themeExt(), diffGutter, keymap.of([indentWithTab]), lintGutter(), EditorView.updateListener.of(u => {
			if (!u.docChanged) return;
			if (!applying) undoDoc = null;
			updateApply();
			if (collapsed) queueMicrotask(() => setCollapsed(false));
			schedule();
		}), EditorView.domEventHandlers({ blur: () => { syncCollapsed(); }, focus: () => clearSelection(output) })],
	},
	b: {
		extensions: [common, themeExt(), diffGutter, EditorState.readOnly.of(true), EditorView.domEventHandlers({ focus: () => clearSelection(input) })],
	},
	diffConfig: { timeout: 500 },
});
const input = view.a, output = view.b;
// The toggle itself is in the early script, so it works before this loads.
window.addEventListener("themechange", () => {
	for (const v of [input, output]) v.dispatch({ effects: theme.reconfigure(isDark() ? duotoneDark : duotoneLight) });
});
input.dom.parentElement!.id = "input";
output.dom.parentElement!.id = "output";
document.getElementById("main")!.append(input.dom.parentElement!, output.dom.parentElement!);
document.getElementById("loading")!.hidden = true;
input.dom.parentElement!.append(document.getElementById("inputTools")!);
output.dom.parentElement!.append(document.getElementById("outputTools")!);

// The async clipboard API needs a secure context and permission, so fall back
// to the old one, and report failures here instead of as an unhandled error.
async function copy(text: string) {
	try {
		await navigator.clipboard.writeText(text);
		return;
	} catch {}
	const t = document.createElement("textarea");
	t.value = text;
	t.style.cssText = "position:fixed;opacity:0";
	document.body.append(t);
	t.select();
	const ok = document.execCommand("copy");
	t.remove();
	if (!ok) status.textContent = "copy failed";
}

// Like a document's single selection: focusing one editor clears the other's.
function clearSelection(v: EditorView) {
	const sel = v.state.selection.main;
	if (!sel.empty) v.dispatch({ selection: { anchor: sel.head } });
}

function setDoc(v: EditorView, text: string) {
	v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: text } });
}

function resetOptions() {
	(document.getElementById("doctype") as HTMLSelectElement).value = "default";
	(document.getElementById("customDoctype") as HTMLInputElement).value = "";
	(document.getElementById("xmlDecl") as HTMLSelectElement).value = "none";
	(document.getElementById("indent") as HTMLSelectElement).value = "";
	for (const id of ["xmlSyntax", "scripting", "strict"]) (document.getElementById(id) as HTMLInputElement).checked = false;
	(document.getElementById("finalNewline") as HTMLInputElement).checked = true;
	for (const id of limits) (document.getElementById(id) as HTMLInputElement).value = String(converter.defaults[id]);
}

// The merge view builds its collapsed ranges once when enabled, keeps only the
// ones away from changes on edits, and never rebuilds them, so toggle it off
// and on to rebuild.
let collapseOn = false, collapsed = false;
function setCollapsed(on: boolean) {
	if (collapsed || on) view.reconfigure({ collapseUnchanged: undefined });
	if (on) view.reconfigure({ collapseUnchanged: {} });
	collapsed = on;
}

// Same ranges as the merge view's default margin of 3 and minimum of 4 lines.
function canCollapse() {
	const doc = input.state.doc, { chunks } = getChunks(input.state)!;
	let prev = 1;
	for (let i = 0; ; i++) {
		const c = chunks[i];
		const from = i ? prev + 3 : 1;
		const to = c ? doc.lineAt(c.fromA).number - 4 : doc.lines;
		if (to - from + 1 >= 4) return true;
		if (!c) return false;
		prev = doc.lineAt(Math.min(doc.length, c.toA)).number;
	}
}

// Replaces the input with the output, and then with what it was before until
// the input is edited.
let applying = false, undoDoc: string | null = null, outputOk = false;
function updateApply() {
	const b = document.getElementById("apply") as HTMLButtonElement;
	b.dataset.icon = undoDoc == null ? icon("arrow_left_alt") : icon("undo");
	const tip = undoDoc == null ? "use output as input" : "undo";
	b.dataset.tip = tip;
	b.setAttribute("aria-label", tip);
	b.disabled = undoDoc == null && (!outputOk || input.state.doc.toString() == output.state.doc.toString());
}

function syncCollapsed() {
	document.getElementById("collapse")!.hidden = !canCollapse();
	setCollapsed(collapseOn && !input.hasFocus);
}

function convert() {
	const doctype = document.getElementById("doctype") as HTMLSelectElement, customDoctype = document.getElementById("customDoctype") as HTMLInputElement;
	customDoctype.hidden = doctype.value != "custom";
	const opt: Options = {
		doctype: doctype.value == "custom" ? customDoctype.value : doctype.value,
		xmlDecl: (document.getElementById("xmlDecl") as HTMLSelectElement).value,
		xmlSyntax: (document.getElementById("xmlSyntax") as HTMLInputElement).checked,
		scripting: (document.getElementById("scripting") as HTMLInputElement).checked,
		strict: (document.getElementById("strict") as HTMLInputElement).checked,
		finalNewline: (document.getElementById("finalNewline") as HTMLInputElement).checked,
		indent: indents[(document.getElementById("indent") as HTMLSelectElement).value],
	};
	for (const id of limits) opt[id] = (document.getElementById(id) as HTMLInputElement).valueAsNumber || 0;

	const start = performance.now();
	let res: Result;
	try {
		res = converter.convert(input.state.doc.toString(), opt);
	} catch (e) {
		fail(errorText(e));
		return;
	}
	const ms = performance.now() - start;

	setDoc(output, res.output);
	outputOk = !res.error;
	updateApply();
	syncCollapsed();
	const len = input.state.doc.length;
	const diagnostic = (m: Warning, severity: Diagnostic["severity"]): Diagnostic => ({
		from: Math.min(m.index, len),
		to: Math.min(m.index + 1, len),
		severity,
		message: m.msg,
	});
	const diagnostics = res.warnings.map(w => diagnostic(w, "warning"));
	if (res.error?.line) diagnostics.unshift(diagnostic(res.error, "error"));
	input.dispatch(setDiagnostics(input.state, diagnostics));
	const items = [];
	if (res.error) items.push(message(res.error, "error"));
	for (const w of res.warnings) items.push(message(w, "warning"));
	messages.replaceChildren(...items);
	status.textContent = `${res.error ? "failed" : "ok"}, ${res.warnings.length} warning${res.warnings.length == 1 ? "" : "s"}, ${ms.toFixed(1)} ms`;
}

function message(m: NonNullable<Result["error"]>, severity: "error" | "warning") {
	const li = document.createElement("li");
	li.className = severity;
	const marker = document.createElement("span");
	marker.dataset.icon = severity == "error" ? icon("error") : icon("warning");
	const text = document.createElement("span");
	text.textContent = m.line ? `${m.line}:${m.col}: ${m.msg}` : m.msg;
	li.append(marker, text);
	if (m.line) li.dataset.index = String(m.index);
	return li;
}

let timer: ReturnType<typeof setTimeout>;
function schedule() {
	clearTimeout(timer);
	timer = setTimeout(convert, 150);
}

messages.addEventListener("click", e => {
	const li = (e.target as Element).closest<HTMLElement>("li[data-index]");
	if (!li) return;
	const i = Math.min(+li.dataset.index!, input.state.doc.length);
	input.dispatch({
		selection: { anchor: i, head: Math.min(i + 1, input.state.doc.length) },
		scrollIntoView: true,
	});
	input.focus();
});

for (const id of options) {
	(document.getElementById(id) as HTMLInputElement | HTMLSelectElement).autocomplete = "off";
	document.getElementById(id)!.addEventListener("input", schedule);
}

document.getElementById("collapse")!.addEventListener("click", () => {
	collapseOn = !collapseOn;
	const b = document.getElementById("collapse")!;
	const tip = collapseOn ? "expand unchanged lines" : "collapse unchanged lines";
	b.setAttribute("aria-pressed", String(collapseOn));
	b.dataset.icon = collapseOn ? icon("expand") : icon("compress");
	b.dataset.tip = tip;
	b.setAttribute("aria-label", tip);
	syncCollapsed();
});

document.getElementById("file")!.addEventListener("change", async e => {
	const f = (e.target as HTMLInputElement).files![0];
	if (!f) return;
	filename = f.name.replace(/\.[^.]*$/, "") + ".xhtml";
	setDoc(input, await f.text());
});

document.getElementById("apply")!.addEventListener("click", () => {
	const doc = undoDoc ?? input.state.doc.toString();
	const text = undoDoc == null ? output.state.doc.toString() : undoDoc;
	applying = true;
	setDoc(input, text);
	applying = false;
	undoDoc = undoDoc == null ? doc : null;
	updateApply();
});
// The summary toggles it natively, so this handles the rest of the panel (and
// the arrow, which is a pseudo-element), except links and text selections.
const about = document.getElementById("about") as HTMLDetailsElement;
about.addEventListener("click", e => {
	if (matchMedia("(min-width: 1400px)").matches || (e.target as Element).closest("a, summary") || getSelection()!.toString()) return;
	about.open = !about.open;
});
document.getElementById("copyInput")!.addEventListener("click", () => copy(input.state.doc.toString()));
document.getElementById("upload")!.addEventListener("click", () => document.getElementById("file")!.click());
document.getElementById("copy")!.addEventListener("click", () => copy(output.state.doc.toString()));

document.getElementById("download")!.addEventListener("click", () => {
	const a = document.createElement("a");
	a.href = URL.createObjectURL(new Blob([output.state.doc.toString()], { type: "application/xhtml+xml" }));
	a.download = filename;
	a.click();
	setTimeout(() => URL.revokeObjectURL(a.href));
});

resetOptions();
convert();
