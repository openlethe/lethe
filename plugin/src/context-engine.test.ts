// plugin/src/context-engine.test.ts
// Node built-in test runner — validates context assembly selection heuristics.

import { describe, it, beforeEach, afterEach } from "node:test";
import assert from "node:assert";
import { LetheContextEngine, type LetheContextEngineConfig } from "./context-engine.js";

// Minimal AgentMessage shape for tests.
function makeMessage(role = "assistant", text = "x"): any {
  return { role, content: [{ type: "text", text }] };
}

// Build a summary response with N recent events (newest-first).
function summaryResponse(opts: {
  summary?: string;
  eventCount?: number;
  recentEvents?: any[];
}): Response {
  const body = JSON.stringify({
    summary: opts.summary ?? "",
    event_count: opts.eventCount ?? 0,
    recent_events: opts.recentEvents ?? [],
  });
  return new Response(body, { status: 200, headers: { "Content-Type": "application/json" } });
}

// Build a generic 404/empty response.
function emptyResponse(status = 404): Response {
  return new Response("", { status });
}

const CFG: LetheContextEngineConfig = {
  endpoint: "http://localhost:18483",
  apiKey: "test-key",
  agentId: "test-agent",
  projectId: "test-project",
  autoLog: false,
};

describe("LetheContextEngine assemble", () => {
  let originalFetch: typeof fetch;
  let fetches: { url: string; init?: RequestInit }[];

  beforeEach(() => {
    originalFetch = globalThis.fetch;
    fetches = [];
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  function mockFetch(...responses: Response[]) {
    let i = 0;
    globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === "string" ? input : input.toString();
      fetches.push({ url, init });
      const res = responses[i++];
      return res ?? emptyResponse(404);
    };
  }

  // ------------------------------------------------------------------
  // 1. Ten events, hard limit five: newest five selected, chronological order.
  // ------------------------------------------------------------------
  it("selects newest five events from ten and injects them in chronological order", async () => {
    const events = Array.from({ length: 10 }, (_, i) => ({
      event_id: `evt-${i}`,
      content: `event ${i}`,
      created_at: new Date(Date.now() - i * 60000).toISOString(), // newest first
    }));

    mockFetch(
      summaryResponse({ summary: "Session summary", eventCount: 10, recentEvents: events }),
      emptyResponse(201) // assembly report accepted
    );

    const engine = new LetheContextEngine(CFG);
    const result = await engine.assemble({
      sessionId: "s1",
      sessionKey: "sess-key",
      messages: [makeMessage("user", "hello")],
    });

    // The returned messages are: [summary, 5 recent events, original message]
    assert.strictEqual(result.messages.length, 1 + 5 + 1, "should have summary + 5 events + user msg");
    const injected = result.messages.slice(1, 6); // the 5 recent events
    const ids = injected.map((m: any) => (m.content[0].text as string).replace(/event /, "")).map(Number);
    // events array is newest-first: evt-0, evt-1, ... evt-9
    // selected newest 5: evt-0, evt-1, evt-2, evt-3, evt-4
    // reversed for chronological: evt-4, evt-3, evt-2, evt-1, evt-0
    assert.deepStrictEqual(ids, [4, 3, 2, 1, 0], "chronological order of newest five");
  });

  // ------------------------------------------------------------------
  // 2. Low budget: effective limit falls to 3.
  // ------------------------------------------------------------------
  it("limits to 3 recent events when budget is low", async () => {
    const events = Array.from({ length: 10 }, (_, i) => ({
      event_id: `evt-${i}`,
      content: `event ${i}`,
      created_at: new Date(Date.now() - i * 60000).toISOString(),
    }));

    mockFetch(
      summaryResponse({ summary: "Session summary", eventCount: 10, recentEvents: events }),
      emptyResponse(201)
    );

    const engine = new LetheContextEngine(CFG);
    const result = await engine.assemble({
      sessionId: "s1",
      sessionKey: "sess-key",
      messages: [makeMessage("user", "hello")],
      tokenBudget: 300, // low budget triggers limit=3
    });

    // summary + 3 events + user msg = 5 total
    assert.strictEqual(result.messages.length, 1 + 3 + 1, "low budget should limit to 3 events");
  });

  // ------------------------------------------------------------------
  // 3. New epoch: summary retained, recent events skipped.
  // ------------------------------------------------------------------
  it("skips recent events when new-epoch heuristic fires", async () => {
    // events from 60 minutes ago
    const events = Array.from({ length: 5 }, (_, i) => ({
      event_id: `evt-${i}`,
      content: `event ${i}`,
      created_at: new Date(Date.now() - 60 * 60000 - i * 1000).toISOString(),
    }));

    mockFetch(
      summaryResponse({ summary: "Session summary", eventCount: 15, recentEvents: events }),
      emptyResponse(201)
    );

    const engine = new LetheContextEngine(CFG);
    // messages.length <= 3 and last event > 30 min ago => new epoch
    const result = await engine.assemble({
      sessionId: "s1",
      sessionKey: "sess-key",
      messages: [makeMessage("user", "hello")],
    });

    // summary + 0 events + user msg = 2 total
    assert.strictEqual(result.messages.length, 1 + 0 + 1, "new epoch should skip recent events");
    // Assembly report should indicate skipped
    assert.ok(result.assemblyId, "assembly should be reported");
  });

  // ------------------------------------------------------------------
  // 4. Active conversation: recent events are NOT omitted.
  // ------------------------------------------------------------------
  it("includes recent events in active conversation (events < 30 min old)", async () => {
    const events = Array.from({ length: 5 }, (_, i) => ({
      event_id: `evt-${i}`,
      content: `event ${i}`,
      created_at: new Date(Date.now() - i * 1000).toISOString(), // very recent
    }));

    mockFetch(
      summaryResponse({ summary: "Session summary", eventCount: 5, recentEvents: events }),
      emptyResponse(201)
    );

    const engine = new LetheContextEngine(CFG);
    // messages.length <= 3 but events are recent (< 30 min) => NOT a new epoch
    const result = await engine.assemble({
      sessionId: "s1",
      sessionKey: "sess-key",
      messages: [makeMessage("user", "hello")],
    });

    // summary + 5 events + user msg = 7 total
    assert.strictEqual(result.messages.length, 1 + 5 + 1, "active conversation keeps recent events");
  });

  // ------------------------------------------------------------------
  // 5. No summary: recent events still injected from summaryData.recent_events.
  // ------------------------------------------------------------------
  it("injects recent events even when summary is empty", async () => {
    const events = Array.from({ length: 3 }, (_, i) => ({
      event_id: `evt-${i}`,
      content: `event ${i}`,
      created_at: new Date(Date.now() - i * 1000).toISOString(),
    }));

    mockFetch(
      summaryResponse({ summary: "", eventCount: 3, recentEvents: events }),
      emptyResponse(201)
    );

    const engine = new LetheContextEngine(CFG);
    const result = await engine.assemble({
      sessionId: "s1",
      sessionKey: "sess-key",
      messages: [makeMessage("user", "hello")],
    });

    // no summary + 3 events + user msg = 4 total
    assert.strictEqual(result.messages.length, 0 + 3 + 1, "events injected even without summary");
  });

  // ------------------------------------------------------------------
  // 6. Failed summary request: returns original messages without throwing.
  // ------------------------------------------------------------------
  it("returns original messages unchanged when Lethe summary request fails", async () => {
    mockFetch(emptyResponse(500));

    const engine = new LetheContextEngine(CFG);
    const original = [makeMessage("user", "hello"), makeMessage("assistant", "hi")];
    const result = await engine.assemble({
      sessionId: "s1",
      sessionKey: "sess-key",
      messages: original,
    });

    // Should return original messages without throwing
    assert.strictEqual(result.messages.length, 2, "returns original messages when summary fails");
    assert.strictEqual(result.messages[0].content[0].text, "hello");
    assert.strictEqual(result.messages[1].content[0].text, "hi");
  });

  // ------------------------------------------------------------------
  // 7. Event ordering with equal timestamps: server output is stable.
  // ------------------------------------------------------------------
  it("preserves server order for events with equal timestamps", async () => {
    const sameTime = new Date().toISOString();
    const events = [
      { event_id: "evt-1", content: "first", created_at: sameTime },
      { event_id: "evt-2", content: "second", created_at: sameTime },
      { event_id: "evt-3", content: "third", created_at: sameTime },
    ];

    mockFetch(
      summaryResponse({ summary: "Session summary", eventCount: 3, recentEvents: events }),
      emptyResponse(201)
    );

    const engine = new LetheContextEngine(CFG);
    const result = await engine.assemble({
      sessionId: "s1",
      sessionKey: "sess-key",
      messages: [makeMessage("user", "hello")],
    });

    // events are newest-first from server; we take all 3 and reverse
    // reversed order should be: third, second, first (stable)
    const injected = result.messages.slice(1, 4);
    const texts = injected.map((m: any) => m.content[0].text);
    assert.deepStrictEqual(texts, ["third", "second", "first"], "stable reverse of equal-timestamp events");
  });
});
