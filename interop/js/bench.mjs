// Benchmarks connect / sign / disconnect between two JS Sign clients on the
// live relay. Prints one JSON object with per-phase timings in ms.
//   TEST_PROJECT_ID=... node bench.mjs [iterations]
import { performance } from "node:perf_hooks";
import { SignClient } from "@walletconnect/sign-client";

class MemoryStorage {
  constructor() { this.m = new Map(); }
  async getKeys() { return [...this.m.keys()]; }
  async getEntries() { return [...this.m.entries()]; }
  async getItem(k) { return this.m.get(k); }
  async setItem(k, v) { this.m.set(k, v); }
  async removeItem(k) { this.m.delete(k); }
}

const iterations = Number(process.argv[2] || 30);
const relayUrl = process.env.TEST_RELAY_URL || "wss://relay.walletconnect.com";
const opts = (name) => ({
  projectId: process.env.TEST_PROJECT_ID, relayUrl, logger: "error", storage: new MemoryStorage(),
  // Two clients in one process need separate Cores; JS shares one per prefix.
  customStoragePrefix: `bench-${name}`,
  metadata: { name: `js-bench-${name}`, description: "bench", url: "https://example.com", icons: [] },
});
const namespaces = {
  eip155: {
    chains: ["eip155:1"], methods: ["personal_sign"], events: ["chainChanged", "accountsChanged"],
    accounts: ["eip155:1:0xab16a96D359eC26a11e2C2b3d8f8B8942d5Bfcdb"],
  },
};

const t0 = performance.now();
const dapp = await SignClient.init(opts("dapp"));
const wallet = await SignClient.init(opts("wallet"));
const initMs = performance.now() - t0;

wallet.on("session_proposal", ({ id }) => wallet.approve({ id, namespaces }).catch(console.error));
wallet.on("session_request", ({ id, topic }) =>
  wallet.respond({ topic, response: { id, jsonrpc: "2.0", result: "0xsig" } }).catch(console.error));
let onDelete;
wallet.on("session_delete", () => onDelete && onDelete());

async function cycle() {
  const t = {};
  let start = performance.now();
  const { uri, approval } = await dapp.connect({
    optionalNamespaces: { eip155: { chains: ["eip155:1"], methods: ["personal_sign"], events: ["chainChanged"] } },
  });
  const settled = approval();
  await wallet.pair({ uri });
  const session = await settled;
  t.connect = performance.now() - start;

  start = performance.now();
  await dapp.request({ topic: session.topic, chainId: "eip155:1", request: { method: "personal_sign", params: ["0x68656c6c6f", "0xab16a96D359eC26a11e2C2b3d8f8B8942d5Bfcdb"] } });
  t.sign = performance.now() - start;

  start = performance.now();
  const deleted = new Promise((resolve) => (onDelete = resolve));
  await dapp.disconnect({ topic: session.topic, reason: { code: 6000, message: "User disconnected." } });
  await deleted;
  t.disconnect = performance.now() - start;
  return t;
}

await cycle(); // warm-up
const runs = [];
for (let i = 0; i < iterations; i++) runs.push(await cycle());

const usage = process.resourceUsage();
console.log(JSON.stringify({
  sdk: "js 2.26.0",
  iterations,
  initMs,
  phases: Object.fromEntries(["connect", "sign", "disconnect"].map((p) => [p, runs.map((r) => r[p])])),
  maxRssMB: usage.maxRSS / 1024,
  cpuMs: (usage.userCPUTime + usage.systemCPUTime) / 1000,
}));
await dapp.core.relayer.transportClose().catch(() => {});
await wallet.core.relayer.transportClose().catch(() => {});
process.exit(0);
