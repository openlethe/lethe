import { delegateCompactionToRuntime, } from "openclaw/plugin-sdk";
// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------
function letheHeaders(apiKey) {
    const h = {
        "Content-Type": "application/json",
    };
    if (apiKey)
        h["Authorization"] = `Bearer ${apiKey}`;
    return h;
}
async function letheFetch(endpoint, apiKey, path, body) {
    return fetch(`${endpoint}${path}`, {
        method: body ? "POST" : "GET",
        headers: letheHeaders(apiKey),
        body: body ? JSON.stringify(body) : undefined,
    });
}
function estimateTokens(text) {
    return Math.ceil(text.length / 4);
}
// ---------------------------------------------------------------------------
// LetheContextEngine
// ---------------------------------------------------------------------------
export class LetheContextEngine {
    cfg;
    info = {
        id: "mentholmike-lethe",
        name: "Lethe",
        version: "0.1.9",
        ownsCompaction: true,
    };
    constructor(cfg) {
        this.cfg = cfg;
    }
    // ------------------------------------------------------------------
    // bootstrap
    // ------------------------------------------------------------------
    // Uses sessionKey as the stable Lethe session_id. On first boot creates
    // the session via POST /sessions. On subsequent boots checks for an
    // interrupted session and resumes it with a summary injection.
    // ------------------------------------------------------------------
    async bootstrap({ sessionId, sessionKey, }) {
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
                    const summaryRes = await letheFetch(endpoint, apiKey, `/sessions/${encodeURIComponent(sessionKey)}/summary`);
                    if (summaryRes.ok) {
                        const summary = await summaryRes.json();
                        return {
                            bootstrapped: true,
                            systemPromptAddition: summaryPrompt(summary),
                            sessionEventCount: summary.event_count ?? 0,
                        };
                    }
                }
                // Session already exists and is active.
                // Fetch event count so assemble() can decide whether to use recent events.
                let sessionEventCount = 0;
                try {
                    const summaryRes = await letheFetch(endpoint, apiKey, `/sessions/${encodeURIComponent(sessionKey)}/summary`);
                    if (summaryRes.ok) {
                        const summary = await summaryRes.json();
                        sessionEventCount = summary.event_count ?? 0;
                    }
                }
                catch {
                    // Non-fatal — continue without event count.
                }
                return { bootstrapped: true, sessionEventCount };
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
        }
        catch {
            return { bootstrapped: false, reason: "network error during bootstrap" };
        }
    }
    // ------------------------------------------------------------------
    // ingest — passive log ingestion on non-heartbeat turns
    // ------------------------------------------------------------------
    async ingest({ sessionId, sessionKey, message, isHeartbeat, }) {
        if (isHeartbeat || !sessionKey)
            return { ingested: false };
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
        }
        catch {
            return { ingested: false };
        }
    }
    // ------------------------------------------------------------------
    // afterTurn — checkpoint on real turns, lightweight heartbeat ping on beats
    // ------------------------------------------------------------------
    async afterTurn(params) {
        const { sessionKey, messages, isHeartbeat, tokenBudget } = params;
        if (!sessionKey)
            return;
        const { endpoint, apiKey } = this.cfg;
        if (isHeartbeat) {
            await letheFetch(endpoint, apiKey, `/sessions/${encodeURIComponent(sessionKey)}/heartbeat`, {
                token_budget: tokenBudget,
            }).catch(() => { });
            return;
        }
        // Real turn: write a checkpoint and auto-log tool calls and thread state.
        const lastMsg = messages[messages.length - 1];
        const openThreads = extractOpenThreads(lastMsg);
        const lastTool = lastMsg ? extractLastTool(lastMsg) : null;
        const allTools = lastMsg ? extractAllToolCallNames(lastMsg) : [];
        // Write checkpoint.
        await letheFetch(endpoint, apiKey, `/sessions/${encodeURIComponent(sessionKey)}/checkpoints`, {
            snapshot: {
                open_threads: openThreads,
                recent_event_ids: [],
                current_task: "",
                last_tool: lastTool,
            },
        }).catch(() => { });
        // Auto-log: tools used (only if there were actual tool calls, not just text).
        if (allTools.length > 0) {
            await letheFetch(endpoint, apiKey, `/sessions/${encodeURIComponent(sessionKey)}/events`, {
                event_type: "log",
                content: `tools: ${allTools.join(" → ")}`,
                tags: ["auto", "tool-call"],
            }).catch(() => { });
        }
        // Auto-log: open threads detected in the conversation.
        if (openThreads.length > 0) {
            await letheFetch(endpoint, apiKey, `/sessions/${encodeURIComponent(sessionKey)}/events`, {
                event_type: "log",
                content: `threads: ${openThreads.join(" | ")}`,
                tags: ["auto", "thread"],
            }).catch(() => { });
        }
    }
    // ------------------------------------------------------------------
    // assemble — two-stage retrieval under token budget
    //
    // Safety guarantees:
    // 1. HARD CAP: never more than hardLimit recent events (default 5).
    //    After /new the transcript has ≤3 messages; surfacing 20 stale Lethe
    //    events on top creates context overflow. A hard cap of 5 is safe.
    // 2. SESSION-AGE HEURISTIC: if messages.length <= 3 and the session has
    //    accumulated >10 events, skip recent events entirely — this is the
    //    signature of a /new or /reset that kept the same Lethe session.
    // 3. TOKEN BUDGET: budget-aware fetching when a real budget is provided.
    // ------------------------------------------------------------------
    async assemble(params) {
        const { sessionKey, messages, tokenBudget } = params;
        if (!sessionKey)
            return { messages: messages, estimatedTokens: 0 };
        const { endpoint, apiKey } = this.cfg;
        // Safety: hard cap of 5 recent events prevents unbounded accumulation.
        // This is the primary guard against /new overflow.
        const HARD_LIMIT = params.hardLimit ?? 5;
        // Safety: session-age heuristic — detect /new or /reset.
        // Condition: fresh transcript (<=3 messages) AND last event was >30 min ago.
        // This avoids false positives on the very first message of a real conversation
        // (messages.length=1 but events are from 10 seconds ago = don't skip).
        // A /new after heavy work will have: messages.length<=3 AND events from minutes/hours ago.
        // minutesSinceLastEvent is computed after the summary fetch (which includes recent_events).
        let minutesSinceLastEvent = Infinity;
        // Stage 1: session summary (the "story so far") — always fetched.
        let summaryText = "";
        let summaryTokens = 0;
        let sessionEventCount = 0;
        let summaryData = null;
        try {
            const res = await letheFetch(endpoint, apiKey, `/sessions/${encodeURIComponent(sessionKey)}/summary`);
            if (res.ok) {
                summaryData = await res.json();
                const rawSummary = summaryData.summary ?? summaryData.session?.summary ?? null;
                if (rawSummary) {
                    summaryText = rawSummary;
                    summaryTokens = estimateTokens(summaryText);
                }
                sessionEventCount = summaryData.event_count ?? 0;
            }
        }
        catch {
            // Proceed without summary.
        }
        // Compute session-age heuristic now that we have event timestamps.
        // A /new reset signature: fresh transcript (<=3 messages) AND last event was >30 min ago.
        // If events are recent (< 30 min), this is the first message of a live conversation — don't skip.
        const recentEventsForHeuristic = summaryData?.recent_events ?? [];
        const lastEventTimestamp = recentEventsForHeuristic[0]?.created_at;
        if (lastEventTimestamp) {
            minutesSinceLastEvent = (Date.now() - new Date(lastEventTimestamp).getTime()) / 60000;
        }
        const isNewEpoch = messages.length <= 3 && minutesSinceLastEvent > 30;
        const shouldSkipRecentEvents = isNewEpoch;
        // Stage 2: recent events — guarded.
        let recentEvents = [];
        let recentTokens = 0;
        if (!shouldSkipRecentEvents && sessionEventCount > 0) {
            // Token budget path: reserve headroom for summary + messages.
            const budgetForRecent = tokenBudget
                ? Math.max(0, tokenBudget - summaryTokens - 200 - estimateTokens(JSON.stringify(messages)))
                : undefined;
            const effectiveLimit = budgetForRecent && budgetForRecent < 200 ? 3 : HARD_LIMIT;
            if (!budgetForRecent || budgetForRecent > 50) {
                try {
                    const eventsUrl = `${endpoint}/sessions/${encodeURIComponent(sessionKey)}/events?limit=${effectiveLimit}`;
                    const res = await fetch(eventsUrl, { method: "GET", headers: letheHeaders(apiKey) });
                    if (res.ok) {
                        const data = await res.json();
                        // GetSessionEvents returns ASC (oldest-first) for pagination.
                        // Reverse so events are prepended in chronological order before current messages.
                        const events = (data.events ?? []).slice().reverse();
                        recentEvents = events.map(eventToMessage);
                        recentTokens = estimateTokens(JSON.stringify(recentEvents));
                    }
                }
                catch {
                    // Proceed without recent events.
                }
            }
        }
        const systemPromptAddition = buildSystemPromptAddition(summaryText, recentTokens);
        const assembledMessages = [
            ...(summaryText ? [makeSummaryMessage(summaryText)] : []),
            ...recentEvents,
            ...messages,
        ];
        return {
            messages: assembledMessages,
            estimatedTokens: summaryTokens + recentTokens + estimateTokens(JSON.stringify(messages)),
            systemPromptAddition,
        };
    }
    // ------------------------------------------------------------------
    // compact — Lethe writes reasoning chain, surfaces summary
    // ------------------------------------------------------------------
    async compact(params) {
        const { sessionKey, tokenBudget, force, currentTokenCount } = params;
        if (!sessionKey)
            return delegateCompactionToRuntime(params);
        const { endpoint, apiKey } = this.cfg;
        try {
            const res = await letheFetch(endpoint, apiKey, `/sessions/${encodeURIComponent(sessionKey)}/compact`, { token_budget: tokenBudget, force });
            if (!res.ok)
                return delegateCompactionToRuntime(params);
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
        }
        catch {
            return delegateCompactionToRuntime(params);
        }
    }
    async dispose() { }
}
// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------
function summaryPrompt(summary) {
    const text = summary.summary ?? "";
    const updated = summary.updated_at ?? "";
    return (`\n[Lethe Memory — Previous Session]\n` +
        (updated ? `Last updated: ${updated}\n` : "") +
        `${text}\n` +
        `[/Lethe Memory]\n`);
}
function buildSystemPromptAddition(summaryText, recentTokens) {
    if (!summaryText && recentTokens === 0)
        return "";
    const parts = [];
    if (summaryText) {
        parts.push(`Previous session summary (${estimateTokens(summaryText)} tokens):\n${summaryText}`);
    }
    if (recentTokens > 0) {
        parts.push(`Recent memory events (~${recentTokens} tokens). Full history available in context.`);
    }
    return parts.join("\n\n");
}
function messageToLogContent(msg) {
    const content = msg.content;
    if (msg.role === "user") {
        const text = extractText(msg);
        if (!text.trim())
            return ""; // Empty user message
        return `[user] ${text}`;
    }
    if (msg.role === "assistant") {
        const text = extractText(msg);
        const toolCalls = (content || []).filter((c) => c.type === "toolCall");
        if (toolCalls.length) {
            return `[assistant] ${text}\n[tools called: ${toolCalls.map((t) => t.name).join(", ")}]`;
        }
        if (!text.trim())
            return ""; // Empty assistant message
        return `[assistant] ${text}`;
    }
    return `[${msg.role}] ${JSON.stringify(content)}`;
}
function extractText(msg) {
    const content = msg.content;
    if (typeof content === "string")
        return content;
    if (!Array.isArray(content))
        return "";
    return content
        .filter((c) => c.type === "text")
        .map((c) => c.text)
        .join("\n");
}
// extractAllToolCallNames returns all tool call names from the last assistant message.
function extractAllToolCallNames(msg) {
    const content = msg?.content;
    if (!Array.isArray(content))
        return [];
    return content.filter((c) => c.type === "toolCall").map((c) => c.name);
}
function extractOpenThreads(msg) {
    const text = extractText(msg);
    const threads = [];
    for (const line of text.split("\n").slice(-10)) {
        const trimmed = line.trim();
        if (trimmed.startsWith("##") ||
            trimmed.startsWith("TODO") ||
            trimmed.startsWith("[ ]") ||
            trimmed.startsWith("- [ ]")) {
            threads.push(trimmed);
        }
    }
    return threads;
}
function extractLastTool(msg) {
    const content = msg.content;
    if (!Array.isArray(content))
        return null;
    const toolCalls = content.filter((c) => c.type === "toolCall");
    return toolCalls.length ? toolCalls[toolCalls.length - 1].name : null;
}
function eventToMessage(event) {
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
    };
}
function makeSummaryMessage(text) {
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
    };
}
