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

function estimateTokens(text: string): number {
  return Math.ceil(text.length / 4);
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
  // bootstrap
  // ------------------------------------------------------------------
  // Uses sessionKey as the stable Lethe session_id. On first boot creates
  // the session via POST /sessions. On subsequent boots checks for an
  // interrupted session and resumes it with a summary injection.
  // ------------------------------------------------------------------

  async bootstrap({
    sessionId,
    sessionKey,
  }: {
    sessionId: string;
    sessionKey?: string;
  }): Promise<BootstrapResult> {
    const { endpoint, apiKey, agentId, projectId } = this.cfg;

    if (!sessionKey) {
      return { bootstrapped: false, reason: "no sessionKey" };
    }

    try {
      // Try to get existing session by sessionKey.
      const res = await letheFetch(endpoint, apiKey, `/sessions/${encodeURIComponent(sessionKey)}`);

      if (res.ok) {
        const session = await res.json();
        if (session.state === "interrupted") {
          const summaryRes = await letheFetch(
            endpoint,
            apiKey,
            `/sessions/${encodeURIComponent(sessionKey)}/summary`
          );
          if (summaryRes.ok) {
            const summary = await summaryRes.json();
            return {
              bootstrapped: true,
              systemPromptAddition: summaryPrompt(summary),
            };
          }
        }
        // Session already exists and is active — nothing to do.
        return { bootstrapped: true };
      }

      // Session doesn't exist yet — create it.
      // Use sessionKey as the Lethe session_id so we can look it up later.
      const createRes = await letheFetch(endpoint, apiKey, "/sessions", {
        session_key: sessionKey,
        agent_id: agentId,
        project_id: projectId,
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
  // ingest — passive log ingestion on non-heartbeat turns
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
    if (isHeartbeat || !sessionKey) return { ingested: false };

    const { endpoint, apiKey } = this.cfg;
    try {
      const logContent = messageToLogContent(message);
      // Skip if content would be empty/whitespace
      if (!logContent || !logContent.trim()) {
        return { ingested: true };
      }
      const res = await letheFetch(endpoint, apiKey, `/sessions/${encodeURIComponent(sessionKey)}/events`, {
        event_type: "log",
        content: logContent,
        tags: [],
      });
      return { ingested: res.ok };
    } catch {
      return { ingested: false };
    }
  }

  // ------------------------------------------------------------------
  // afterTurn — checkpoint on real turns, lightweight heartbeat ping on beats
  // ------------------------------------------------------------------

  async afterTurn(params: AfterTurnParams): Promise<void> {
    const { sessionKey, messages, isHeartbeat, tokenBudget } = params;

    if (!sessionKey) return;

    const { endpoint, apiKey } = this.cfg;

    if (isHeartbeat) {
      await letheFetch(endpoint, apiKey, `/sessions/${encodeURIComponent(sessionKey)}/heartbeat`, {
        token_budget: tokenBudget,
      }).catch(() => {});
      return;
    }

    // Real turn: write a checkpoint capturing open threads and last tool.
    const lastMsg = messages[messages.length - 1];
    const openThreads = extractOpenThreads(lastMsg);
    const lastTool = lastMsg ? extractLastTool(lastMsg) : null;

    await letheFetch(endpoint, apiKey, `/sessions/${encodeURIComponent(sessionKey)}/checkpoints`, {
      snapshot: {
        open_threads: openThreads,
        recent_event_ids: [],
        current_task: "",
        last_tool: lastTool,
      },
    }).catch(() => {});
  }

  // ------------------------------------------------------------------
  // assemble — two-stage retrieval under token budget
  // ------------------------------------------------------------------

  async assemble(params: AssembleParams): Promise<AssembleResult> {
    const { sessionKey, messages, tokenBudget } = params;
    if (!sessionKey) return { messages: messages as any[], estimatedTokens: 0 };

    const { endpoint, apiKey } = this.cfg;

    // Stage 1: session summary (the "story so far").
    let summaryText = "";
    let summaryTokens = 0;
    try {
      const res = await letheFetch(endpoint, apiKey, `/sessions/${encodeURIComponent(sessionKey)}/summary`);
      if (res.ok) {
        const summary = await res.json();
        if (summary.summary) {
          summaryText = summary.summary;
          summaryTokens = estimateTokens(summaryText);
        }
      }
    } catch {
      // Proceed without summary.
    }

    // Stage 2: recent events, budget-aware.
    let recentEvents: AgentMessage[] = [];
    let recentTokens = 0;
    const budgetForRecent = tokenBudget
      ? Math.max(0, tokenBudget - summaryTokens - 200)
      : undefined;

    if (!budgetForRecent || budgetForRecent > 100) {
      try {
        const res = await letheFetch(endpoint, apiKey, `/sessions/${encodeURIComponent(sessionKey)}/events`, {
          limit: 20,
          token_budget: budgetForRecent,
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
  // compact — Lethe writes reasoning chain, surfaces summary
  // ------------------------------------------------------------------

  async compact(params: CompactParams): Promise<CompactResult> {
    const { sessionKey, tokenBudget, force, currentTokenCount } = params;

    if (!sessionKey) return delegateCompactionToRuntime(params);

    const { endpoint, apiKey } = this.cfg;

    try {
      const res = await letheFetch(
        endpoint,
        apiKey,
        `/sessions/${encodeURIComponent(sessionKey)}/compact`,
        { token_budget: tokenBudget, force }
      );

      if (!res.ok) return delegateCompactionToRuntime(params);

      const data = await res.json();
      return {
        ok: true,
        compacted: true,
        result: {
          summary: data.summary,
          tokensBefore: currentTokenCount ?? 0,
          tokensAfter: data.tokens_after,
        },
      };
    } catch {
      return delegateCompactionToRuntime(params);
    }
  }

  async dispose(): Promise<void> {}
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

function summaryPrompt(summary: { summary?: string; updated_at?: string }): string {
  const text = summary.summary ?? "";
  const updated = summary.updated_at ?? "";
  return (
    `\n[Lethe Memory — Previous Session]\n` +
    (updated ? `Last updated: ${updated}\n` : "") +
    `${text}\n` +
    `[/Lethe Memory]\n`
  );
}

function buildSystemPromptAddition(summaryText: string, recentTokens: number): string {
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
    if (!text.trim()) return ""; // Empty user message
    return `[user] ${text}`;
  }
  if (msg.role === "assistant") {
    const text = extractText(msg);
    const toolCalls = (content || []).filter((c: any) => c.type === "toolCall");
    if (toolCalls.length) {
      return `[assistant] ${text}\n[tools called: ${toolCalls.map((t: any) => t.name).join(", ")}]`;
    }
    if (!text.trim()) return ""; // Empty assistant message
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
  const text = extractText(msg);
  const threads: string[] = [];
  for (const line of text.split("\n").slice(-10)) {
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
  return toolCalls.length ? toolCalls[toolCalls.length - 1].name : null;
}

function eventToMessage(event: any): AgentMessage {
  return {
    id: event.event_id,
    role: "assistant",
    content: [{ type: "text", text: event.content }],
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
    timestamp: event.created_at
      ? new Date(event.created_at).getTime()
      : Date.now(),
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
