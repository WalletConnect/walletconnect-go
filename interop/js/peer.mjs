// A JS Sign client driven over stdin/stdout with newline-delimited JSON, for
// the Go interop tests. Commands are {"cmd": ..., ...}; every reply and event
// is written as one JSON line on stdout.
import { createInterface } from "node:readline";
import { SignClient } from "@walletconnect/sign-client";

const out = (obj) => process.stdout.write(JSON.stringify(obj) + "\n");
const fail = (cmd, err) => out({ event: "error", cmd, message: String(err?.message ?? err), code: err?.code });

class MemoryStorage {
  constructor() { this.m = new Map(); }
  async getKeys() { return [...this.m.keys()]; }
  async getEntries() { return [...this.m.entries()]; }
  async getItem(k) { return this.m.get(k); }
  async setItem(k, v) { this.m.set(k, v); }
  async removeItem(k) { this.m.delete(k); }
}

const role = process.argv[2];
const client = await SignClient.init({
  projectId: process.env.TEST_PROJECT_ID,
  relayUrl: process.env.TEST_RELAY_URL || "wss://relay.walletconnect.com",
  storage: new MemoryStorage(),
  logger: "error",
  metadata: { name: `js-${role}`, description: "walletconnect-go interop", url: "https://example.com", icons: [] },
});

// update() and extend() hand back acknowledged() only after their own publish
// returns, and createDelayedPromise drops a resolution that lands before
// acknowledged() is called. A fast peer hits that window, so watch the
// engine's internal ack events instead of awaiting acknowledged().
{
  const ev = client.engine.events;
  const emit = ev.emit.bind(ev);
  ev.emit = (name, ...args) => {
    for (const kind of ["session_update", "session_extend"]) {
      if (typeof name === "string" && name.startsWith(`${kind}:`)) {
        const error = args[0]?.error;
        out(error ? { event: "error", cmd: kind, message: error.message } : { event: `${kind}_acked` });
      }
    }
    return emit(name, ...args);
  };
}

for (const name of ["session_proposal", "session_request", "session_event", "session_update",
  "session_extend", "session_ping", "session_delete", "session_authenticate"]) {
  client.on(name, (payload) => out({ event: name, payload }));
}

const handlers = {
  async connect({ optionalNamespaces }) {
    const { uri, approval } = await client.connect({ optionalNamespaces });
    out({ event: "uri", uri });
    approval().then((session) => out({ event: "session", session }), (e) => fail("approval", e));
  },
  async authenticate({ chains, methods }) {
    const { uri, response } = await client.authenticate({
      chains, methods, domain: "example.com", uri: "https://example.com", nonce: "123456789",
    });
    out({ event: "uri", uri });
    response().then((r) => out({ event: "session", session: r.session }), (e) => fail("authenticate", e));
  },
  async pair({ uri }) {
    await client.pair({ uri });
    out({ event: "paired" });
  },
  async approve({ id, namespaces }) {
    const { topic, acknowledged } = await client.approve({ id, namespaces });
    out({ event: "approved", topic });
    const session = await acknowledged();
    out({ event: "acknowledged", topic: session.topic });
  },
  async reject({ id }) {
    await client.reject({ id, reason: { code: 5000, message: "User rejected." } });
    out({ event: "rejected" });
  },
  async request({ topic, chainId, method, params }) {
    try {
      const result = await client.request({ topic, chainId, request: { method, params } });
      out({ event: "request_result", result });
    } catch (e) {
      out({ event: "request_result", error: { code: e?.code, message: e?.message } });
    }
  },
  async respond({ topic, id, result, error }) {
    const response = error ? { id, jsonrpc: "2.0", error } : { id, jsonrpc: "2.0", result };
    await client.respond({ topic, response });
    out({ event: "responded", id });
  },
  async update({ topic, namespaces }) {
    await client.update({ topic, namespaces });
  },
  async emit({ topic, chainId, name, data }) {
    await client.emit({ topic, chainId, event: { name, data } });
    out({ event: "emitted" });
  },
  async extend({ topic }) {
    await client.extend({ topic });
  },
  async ping({ topic }) {
    await client.ping({ topic });
    out({ event: "pinged" });
  },
  async disconnect({ topic }) {
    await client.disconnect({ topic, reason: { code: 6000, message: "User disconnected." } });
    out({ event: "disconnected" });
  },
  async sessions() {
    out({ event: "sessions", sessions: client.session.getAll() });
  },
  async exit() {
    await client.core.relayer.transportClose().catch(() => {});
    process.exit(0);
  },
};

out({ event: "ready", clientId: await client.core.crypto.getClientId() });

createInterface({ input: process.stdin }).on("line", (line) => {
  if (!line.trim()) return;
  let msg;
  try {
    msg = JSON.parse(line);
  } catch (e) {
    return fail("parse", e);
  }
  const handler = handlers[msg.cmd];
  if (!handler) return fail(msg.cmd, new Error("unknown command"));
  handler(msg).catch((e) => fail(msg.cmd, e));
});
