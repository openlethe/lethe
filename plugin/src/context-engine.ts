import {
  type ContextEngine,
  type ContextEngineInfo,
  delegateCompactionToRuntime,
} from "openclaw/plugin-sdk";
import type { AgentMessage } from "@mariozechner/pi-agent-core";
import type {
  BootstrapResult,
  AssembleResult,
  CompactResult,
  IngestResult,
  ContextEngineRuntimeContext,
} from "./context-engine-types.js";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface LetheContextEngineConfig {
  endpoint: string;
  apiKey: string;
  agentId: string;
  projectId: string;
}

interface AssembleParams {
  sessionId: string;
  sessionKey?: string;
  messages: AgentMessage[];
  tokenBudget?: number;
  prompt?: string;
}

interface AfterTurnParams {
  sessionId: string;
  sessionKey?: string;
  sessionFile: string;
  messages: AgentMessage[];
  prePromptMessageCount: number;
  isHeartbeat?: boolean;
  tokenBudget?: number;
  runtimeContext?: ContextEngineRuntimeContext;
}

interface CompactParams {
  sessionId: string;
  sessionKey?: string;
  sessionFile: string;
  tokenBudget?: number;
  force?: boolean;
  currentTokenCount?: number;
  runtimeContext?: ContextEngineRuntimeContext;
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function letheHeaders(apiKey: string): Record<string, string> {
  const h: Record<string, string> = {
    "Content-Type": "application/json",
  };
  if (apiKey) h["Authorization"] = `Bearer ${apiKey}`;
  return h;
}

async function letheFetch(
  endpoint: string,
  apiKey: string,
  path: string,
  body?: unknown
): Promise<Response> {
  return fetch(`${endpoint}${path}`, {
    method: body ? "POST" : "GET",
    headers: letheHeaders(apiKey),
    body: body ? JSON.stringify(body) : undefined,
  });
}

// ---------------------------------------------------------------------------
// LetheContextEngine
// ---------------------------------------------------------------------------

export class LetheContextEngine implements ContextEngine {
  readonly info: ContextEngineInfo = {
    id: "lethe",
    name: "Lethe",
    version: "0.1.0",
    ownsCompaction: true,
  };

  constructor(private cfg: LetheContextEngineConfig) {}

  // ------------------------------------------------------------------
  // Session management (OpenClaw ↔ Lethe)
  // ------------------------------------------------------------------

  async bootstrap({
    sessionId,
    sessionKey,
  }: {
    sessionId: string;
    sessionKey?: string;
  }): Promise<BootstrapResult> {
    const { endpoint, apiKey, agentId, projectId } = this.cfg;

    // Check if Lethe already has an interrupted session for this sessionKey.
    if (!sessionKey) {
      return { bootstrapped: false, reason: "no sessionKey" };
    }

    try {
      const res = await letheFetch(
        endpoint,
        apiKey,
        `/sessions/${sessionKey}`
      );

      if (res.ok) {
        const session = await res.json();
        if (session.state === "interrupted") {
          // Resume: pull the session summary + recent events from Lethe.
          const summaryRes = await letheFetch(
            endpoint,
            apiKey,
            `/sessions/${sessionKey}/summary`
          );
          if (summaryRes.ok) {
            const summary = await summaryRes.json();
            return {
              bootstrapped: true,
              systemPromptAddition: summaryPrompt(summary),
            };
          }
        }
      }

      // No interrupted session found — create a fresh one.
      const createRes = await letheFetch(endpoint, apiKey, "/sessions", {
        sessionId,
        agentId,
        projectId,
      });

      if (!createRes.ok && createRes.status !== 409) {
        return { bootstrapped: false, reason: "failed to create session" };
      }

      return { bootstrapped: true };
    } catch {
      return { bootstrapped: false, reason: "network error during bootstrap" };
    }
  }

  // ------------------------------------------------------------------
  // Ingest
  // ------------------------------------------------------------------

  async ingest({
    sessionId,
    sessionKey,
    message,
    isHeartbeat,
  }: {
    sessionId: string;
    sessionKey?: string;
    message: AgentMessage;
    isHeartbeat?: boolean;
  }): Promise<IngestResult> {
    // Heartbeats are handled by afterTurn; skip passive ingestion here.
    if (isHeartbeat) return { ingested: false };

    if (!sessionKey) return { ingested: false };

    const { endpoint, apiKey } = this.cfg;
    try {
      const res = await letheFetch(endpoint, apiKey, "/events", {
        sessionKey,
        eventType: "log",
        content: messageToLogContent(message),
      });
      return { ingested: res.ok };
    } catch {
      return { ingested: false };
    }
  }

  // ------------------------------------------------------------------
  // afterTurn — checkpoint + lightweight events
  // ------------------------------------------------------------------

  async afterTurn(params: AfterTurnParams): Promise<void> {
    const { sessionId, sessionKey, messages, isHeartbeat, tokenBudget } =
      params;

    if (!sessionKey) return;

    const { endpoint, apiKey } = this.cfg;

    // Heartbeat: lightweight heartbeat ping only.
    if (isHeartbeat) {
      await letheFetch(endpoint, apiKey, `/sessions/${sessionKey}/heartbeat`, {
        tokenBudget,
      }).catch(() => {});
      return;
    }

    // Real turn: write full checkpoint.
    const lastMsg = messages[messages.length - 1];
    const openThreads = extractOpenThreads(lastMsg);

    await letheFetch(endpoint, apiKey, `/sessions/${sessionKey}/checkpoints`, {
      sessionId,
      openThreads,
      lastTool: lastMsg ? extractLastTool(lastMsg) : null,
      tokenBudget,
    }).catch(() => {});
  }

  // ------------------------------------------------------------------
  // assemble — two-stage retrieval under token budget
  // ------------------------------------------------------------------

  async assemble(params: AssembleParams): Promise<AssembleResult> {
    const { sessionKey, messages, tokenBudget } = params;
    if (!sessionKey) return { messages: messages as any[], estimatedTokens: 0 };

    const { endpoint, apiKey } = this.cfg;

    // Stage 1: session summary (the "story so far" — written at graceful end
    // or last significant checkpoint).
    let summaryText = "";
    let summaryTokens = 0;
    try {
      const res = await letheFetch(
        endpoint,
        apiKey,
        `/sessions/${sessionKey}/summary`
      );
      if (res.ok) {
        const summary = await res.json();
        if (summary.text) {
          summaryText = summary.text;
          summaryTokens = estimateTokens(summary.text);
        }
      }
    } catch {
      // Network or parse error — proceed without summary.
    }

    // Stage 2: recent events, budget-aware.
    let recentEvents: AgentMessage[] = [];
    let recentTokens = 0;
    const budgetForRecent = tokenBudget
      ? Math.max(0, tokenBudget - summaryTokens - 200) // 200 = buffer
      : undefined;

    if (!budgetForRecent || budgetForRecent > 100) {
      try {
        const res = await letheFetch(endpoint, apiKey, `/events`, {
          sessionKey,
          limit: 20,
          tokenBudget: budgetForRecent,
        });
        if (res.ok) {
          const data = await res.json();
          recentEvents = (data.events ?? []).map(eventToMessage);
          recentTokens = estimateTokens(JSON.stringify(recentEvents));
        }
      } catch {
        // Proceed without recent events.
      }
    }

    // Build the assembled context.
    const systemPromptAddition = buildSystemPromptAddition(summaryText, recentTokens);
    const assembledMessages: any[] = [
      ...(summaryText ? [makeSummaryMessage(summaryText)] : []),
      ...recentEvents,
      ...(messages as any[]),
    ];

    return {
      messages: assembledMessages,
      estimatedTokens:
        summaryTokens + recentTokens + estimateTokens(JSON.stringify(messages)),
      systemPromptAddition,
    };
  }

  // ------------------------------------------------------------------
  // compact — Lethe writes reasoning chain to its store, surfaces summary
  // ------------------------------------------------------------------

  async compact(params: CompactParams): Promise<CompactResult> {
    const { sessionKey, tokenBudget, force, currentTokenCount } = params;

    if (!sessionKey) {
      return delegateCompactionToRuntime(params);
    }

    const { endpoint, apiKey } = this.cfg;

    try {
      // Ask Lethe server to compact: write the reasoning chain to its store
      // and return a summary.
      const res = await letheFetch(
        endpoint,
        apiKey,
        `/sessions/${sessionKey}/compact`,
        {
          tokenBudget,
          force,
        }
      );

      if (!res.ok) {
        return delegateCompactionToRuntime(params);
      }

      const data = await res.json();

      return {
        ok: true,
        compacted: true,
        result: {
          summary: data.summary,
          tokensBefore: currentTokenCount ?? 0,
          tokensAfter: data.tokensAfter,
        },
      };
    } catch {
      // Lethe unavailable — delegate to built-in compaction.
      return delegateCompactionToRuntime(params);
    }
  }

  // ------------------------------------------------------------------
  // dispose
  // ------------------------------------------------------------------

  async dispose(): Promise<void> {
    // Nothing to dispose; the HTTP client has no persistent connections.
  }
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

function summaryPrompt(
  summary: { text: string; updatedAt: string }
): string {
  return (
    `\n[Lethe Memory — Previous Session]\n` +
    `Last updated: ${summary.updatedAt}\n` +
    `${summary.text}\n` +
    `[/Lethe Memory]\n`
  );
}

function buildSystemPromptAddition(
  summaryText: string,
  recentTokens: number
): string {
  if (!summaryText && recentTokens === 0) return "";
  const parts: string[] = [];
  if (summaryText) {
    parts.push(
      `Previous session summary (${estimateTokens(summaryText)} tokens):\n${summaryText}`
    );
  }
  if (recentTokens > 0) {
    parts.push(
      `Recent memory events (~${recentTokens} tokens). Full history available in context.`
    );
  }
  return parts.join("\n\n");
}

function messageToLogContent(msg: AgentMessage): string {
  const content = (msg as any).content;
  if (msg.role === "user") {
    const text = extractText(msg);
    return `[user] ${text}`;
  }
  if (msg.role === "assistant") {
    const text = extractText(msg);
    const toolCalls = (content || []).filter(
      (c: any) => c.type === "toolCall"
    );
    if (toolCalls.length) {
      return `[assistant] ${text}\n[tools called: ${toolCalls.map((t: any) => t.name).join(", ")}]`;
    }
    return `[assistant] ${text}`;
  }
  return `[${msg.role}] ${JSON.stringify(content)}`;
}

function extractText(msg: AgentMessage): string {
  const content = (msg as any).content;
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return "";
  return content
    .filter((c: any) => c.type === "text")
    .map((c: any) => c.text)
    .join("\n");
}

function extractOpenThreads(msg: AgentMessage): string[] {
  // Look for common "open thread" indicators in the last assistant message.
  const text = extractText(msg);
  const threads: string[] = [];
  // Match patterns like "## Open thread" or "### TODO" or "[open]" in the
  // trailing assistant output that hasn't been addressed yet.
  const lines = text.split("\n");
  for (const line of lines.slice(-10)) {
    const trimmed = line.trim();
    if (
      trimmed.startsWith("##") ||
      trimmed.startsWith("TODO") ||
      trimmed.startsWith("[ ]") ||
      trimmed.startsWith("- [ ]")
    ) {
      threads.push(trimmed);
    }
  }
  return threads;
}

function extractLastTool(msg: AgentMessage): string | null {
  const content = (msg as any).content;
  if (!Array.isArray(content)) return null;
  const toolCalls = content.filter((c: any) => c.type === "toolCall");
  if (!toolCalls.length) return null;
  return toolCalls[toolCalls.length - 1].name;
}

function eventToMessage(event: any): AgentMessage {
  return {
    id: event.eventId,
    role: "assistant",
    content: event.content,
    api: "unknown",
    provider: "unknown",
    model: "unknown",
    usage: {
      input: 0,
      output: 0,
      cacheRead: 0,
      cacheWrite: 0,
      totalTokens: 0,
      cost: {
        input: 0,
        output: 0,
        cacheRead: 0,
        cacheWrite: 0,
        total: 0,
      },
    },
    stopReason: "stop",
    timestamp: event.createdAt ? new Date(event.createdAt).getTime() : Date.now(),
  } as AgentMessage;
}

function makeSummaryMessage(text: string): AgentMessage {
  return {
    id: "lethe-summary",
    role: "assistant",
    content: [
      {
        type: "text",
        text: `## Prior Session Summary\n\n${text}`,
      },
    ],
    api: "unknown",
    provider: "unknown",
    model: "unknown",
    usage: {
      input: 0,
      output: 0,
      cacheRead: 0,
      cacheWrite: 0,
      totalTokens: 0,
      cost: {
        input: 0,
        output: 0,
        cacheRead: 0,
        cacheWrite: 0,
        total: 0,
      },
    },
    stopReason: "stop",
    timestamp: Date.now(),
  } as AgentMessage;
}

function estimateTokens(text: string): number {
  // Rough approximation: ~4 chars per token for English text.
  return Math.ceil(text.length / 4);
}
