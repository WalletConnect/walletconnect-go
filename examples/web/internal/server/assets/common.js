// Shared helpers for the demo pages: the JSON API, the server event stream,
// the event log, toasts and small DOM builders.

export const CHAINS = [
  ["eip155:1", "Ethereum"], ["eip155:10", "Optimism"], ["eip155:137", "Polygon"],
  ["eip155:8453", "Base"], ["eip155:42161", "Arbitrum"], ["eip155:11155111", "Sepolia"],
];
const chainNames = Object.fromEntries(CHAINS);
export const chainName = (caip2) => chainNames[caip2] || caip2;

// ---- icons (16px, stroke currentColor) ----
const ICONS = {
  copy: '<rect x="5" y="5" width="9" height="9" rx="2"/><path d="M11 5V3.5A1.5 1.5 0 0 0 9.5 2h-6A1.5 1.5 0 0 0 2 3.5v6A1.5 1.5 0 0 0 3.5 11H5"/>',
  check: '<path d="M3 8.5l3 3 7-7"/>',
  x: '<path d="M4 4l8 8M12 4l-8 8"/>',
  clock: '<circle cx="8" cy="8" r="6"/><path d="M8 4.5V8l2.5 1.5"/>',
  link: '<path d="M7 9a3 3 0 0 0 4.2 0l2-2a3 3 0 0 0-4.2-4.2l-.6.6"/><path d="M9 7a3 3 0 0 0-4.2 0l-2 2a3 3 0 0 0 4.2 4.2l.6-.6"/>',
  ping: '<path d="M2 8h3l2-4 2 8 2-4h3"/>',
  refresh: '<path d="M13 3v4H9"/><path d="M13 7A5 5 0 1 0 12 11"/>',
  power: '<path d="M8 2v6"/><path d="M4.5 4.5a5 5 0 1 0 7 0"/>',
  qr: '<rect x="2" y="2" width="5" height="5" rx="1"/><rect x="9" y="2" width="5" height="5" rx="1"/><rect x="2" y="9" width="5" height="5" rx="1"/><path d="M9 9h2v2H9zM12 12h2v2h-2zM12 9h2M9 12v2"/>',
  pen: '<path d="M10.5 2.5l3 3L6 13H3v-3z"/>',
  shield: '<path d="M8 2l5 2v4c0 3-2.2 5.2-5 6-2.8-.8-5-3-5-6V4z"/>',
  plus: '<path d="M8 3v10M3 8h10"/>',
  bolt: '<path d="M9 2L3 9h4l-1 5 6-7H8z"/>',
  trash: '<path d="M3 4.5h10M6.5 4.5V3h3v1.5M4.5 4.5l.7 8.5h5.6l.7-8.5"/>',
};

export function icon(name) {
  const span = document.createElement("span");
  span.style.display = "inline-flex";
  span.innerHTML = `<svg class="i" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">${ICONS[name] || ""}</svg>`;
  return span.firstChild;
}

// ---- DOM ----

// el builds an element: el("div", {class: "x", onclick: fn}, child, "text").
export function el(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k.startsWith("on")) node.addEventListener(k.slice(2), v);
    else if (v !== undefined && v !== null && v !== false) node.setAttribute(k, v === true ? "" : v);
  }
  for (const c of children.flat()) {
    if (c === null || c === undefined || c === false) continue;
    node.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return node;
}

export const short = (s, n = 6) => (s && s.length > 2 * n + 3 ? `${s.slice(0, n)}…${s.slice(-n)}` : s);

export function fill(id, items, render, emptyTitle, emptyHint) {
  const node = document.getElementById(id);
  node.replaceChildren(...(items.length ? items.map(render)
    : [el("div", { class: "empty" }, el("strong", {}, emptyTitle), emptyHint)]));
  const count = document.querySelector(`[data-count-for="${id}"]`);
  if (count) count.textContent = items.length;
}

// ---- time ----

export function relative(unixSeconds) {
  const diff = unixSeconds - Date.now() / 1000;
  const abs = Math.abs(diff);
  const [n, unit] = abs < 90 ? [Math.round(abs), "second"] : abs < 5400 ? [Math.round(abs / 60), "minute"]
    : abs < 129600 ? [Math.round(abs / 3600), "hour"] : [Math.round(abs / 86400), "day"];
  const text = `${n} ${unit}${n === 1 ? "" : "s"}`;
  return diff >= 0 ? `in ${text}` : `${text} ago`;
}

export const clock = () => new Date().toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });

// ---- components ----

export function copyable(text, display = short(text)) {
  const btn = el("button", { title: "Copy", "aria-label": "Copy" }, icon("copy"));
  btn.addEventListener("click", async () => {
    await navigator.clipboard.writeText(text);
    btn.replaceChildren(icon("check"));
    setTimeout(() => btn.replaceChildren(icon("copy")), 1200);
  });
  return el("span", { class: "copyable" }, el("code", { title: text }, display), btn);
}

export function avatar(metadata) {
  const icon0 = metadata.icons && metadata.icons[0];
  const initial = (metadata.name || "?").trim()[0].toUpperCase();
  if (!icon0) return el("div", { class: "avatar" }, initial);
  const img = el("img", { class: "avatar", src: icon0, alt: "" });
  img.addEventListener("error", () => img.replaceWith(el("div", { class: "avatar" }, initial)));
  return img;
}

export function peer(metadata) {
  let host = metadata.url || "";
  try { host = new URL(metadata.url).host; } catch { /* keep raw */ }
  return el("div", { class: "peer" }, avatar(metadata),
    el("div", { class: "who" }, el("div", { class: "title" }, metadata.name || "Unnamed app"),
      el("div", { class: "url" }, host)));
}

export function badge(text, cls = "") {
  return el("span", { class: `badge ${cls}` }, el("span", { class: "dot" }), text);
}

// accountsOf lists the CAIP-10 accounts of a session.
export const accountsOf = (session) => Object.values(session.namespaces || {}).flatMap((ns) => ns.accounts || []);

// chainsOf lists the CAIP-2 chains of a session, in account order.
export const chainsOf = (session) => [...new Set(accountsOf(session).map((a) => a.split(":").slice(0, 2).join(":")))];

export function accountChip(caip10) {
  const parts = caip10.split(":");
  const address = parts[2];
  return el("span", { class: "chip" }, el("span", { class: "net" }, chainName(parts[0] + ":" + parts[1])),
    el("span", { class: "mono" }, short(address, 5)));
}

export function chainSelect(chains) {
  return el("select", {}, chains.map((c) => el("option", { value: c }, chainName(c))));
}

// buttonAction runs fn on click, showing a spinner and disabling the button.
export function buttonAction(content, fn, cls = "") {
  const b = el("button", { class: cls }, content);
  b.addEventListener("click", async () => {
    b.disabled = true;
    b.classList.add("busy");
    try { await fn(); } catch { /* reported by api() */ } finally {
      b.disabled = false;
      b.classList.remove("busy");
    }
  });
  return b;
}

// ---- toasts ----

export function toast(message, kind = "info") {
  let box = document.getElementById("toasts");
  if (!box) document.body.append(box = el("div", { id: "toasts", role: "status" }));
  const t = el("div", { class: `toast ${kind}` }, icon(kind === "bad" ? "x" : kind === "ok" ? "check" : "bolt"), el("div", {}, message));
  box.append(t);
  setTimeout(() => t.remove(), 4500);
}

// ---- API ----

export async function api(path, body) {
  const res = await fetch(path, body === undefined ? {} : {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    const message = data.error || res.statusText;
    toast(message, "bad");
    log("api error", { path, message });
    throw new Error(message);
  }
  return data;
}

// onEvents calls handler(type, data) for each server event.
export function onEvents(handler) {
  const source = new EventSource("/api/events");
  source.onmessage = (msg) => {
    const ev = JSON.parse(msg.data);
    if (ev.type === "relay_state") setRelay(ev.data.connected, ev.data.error);
    else log(ev.type, ev.data);
    handler(ev.type, ev.data);
  };
  source.onerror = () => setRelay(false, "server unreachable");
}

export function setRelay(connected, error) {
  const node = document.getElementById("relay");
  if (!node) return;
  node.replaceWith(Object.assign(
    badge(connected ? "Relay connected" : error ? "Relay offline" : "Relay idle", connected ? "ok" : error ? "bad" : ""),
    { id: "relay", title: error || "" }));
}

// ---- event log ----

const LOG_LIMIT = 200;

function summarize(kind, data) {
  if (!data || typeof data !== "object") return "";
  if (data.error) return data.error;
  const d = data.result && typeof data.result === "object" ? data.result : data;
  const topic = data.topic || data.Topic || (data.Session && data.Session.topic) || (data.Proposal && data.Proposal.pairingTopic);
  const parts = [];
  if (data.method || data.Method) parts.push(data.method || data.Method);
  if (data.Name) parts.push(data.Name);
  if (d.verified === true) parts.push("signature verified");
  if (d.roundTrip) parts.push(d.roundTrip);
  if (topic) parts.push(short(topic));
  return parts.join(" · ");
}

export function log(kind, data) {
  const box = document.getElementById("log");
  if (!box) return;
  box.querySelector(".empty")?.remove();
  const cls = data && data.error ? "bad" : kind.endsWith("_ack") || kind.endsWith("_result") ? "ok" : kind.startsWith("session_") ? "blue" : "";
  const entry = el("details", { class: "log-entry" },
    el("summary", {}, el("span", { class: "time" }, clock()), el("span", { class: `kind ${cls}` }, kind.replaceAll("_", " ")),
      el("span", { class: "summary" }, summarize(kind, data))),
    el("pre", {}, JSON.stringify(data, null, 2)));
  box.prepend(entry);
  while (box.children.length > LOG_LIMIT) box.lastChild.remove();
}

export function initLog() {
  const box = document.getElementById("log");
  box.replaceChildren(el("div", { class: "empty" }, el("strong", {}, "No events yet"), "Protocol events appear here as they happen."));
  document.getElementById("clear-log")?.addEventListener("click", initLog);
}

// decodeMessage shows a hex personal_sign message as text when it is printable.
export function decodeMessage(hex) {
  if (typeof hex !== "string" || !/^0x([0-9a-f]{2})*$/i.test(hex)) return hex;
  const bytes = new Uint8Array(hex.slice(2).match(/../g)?.map((b) => parseInt(b, 16)) || []);
  try {
    const text = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
    return /^[\P{C}\n\r\t]*$/u.test(text) ? text : hex;
  } catch { return hex; }
}
