import { Type } from "@sinclair/typebox";
function letheHeaders(apiKey) {
    const h = {
        "Content-Type": "application/json",
    };
    if (apiKey)
        h["Authorization"] = `Bearer ${apiKey}`;
    return h;
}
async function lethePost(endpoint, apiKey, path, body) {
    return fetch(`${endpoint}${path}`, {
        method: "POST",
        headers: letheHeaders(apiKey),
        body: JSON.stringify(body),
    });
}
// ---------------------------------------------------------------------------
// Tool schemas (Typebox)
// ---------------------------------------------------------------------------
// memory.record — deliberate decision with reasoning
const RecordParams = Type.Object({
    content: Type.String({
        description: "The decision or reasoning to record. Be specific: include the what, why, and any constraints considered.",
    }),
    sessionKey: Type.Optional(Type.String({ description: "Override the current session key." })),
    tags: Type.Optional(Type.Array(Type.String(), {
        description: "Optional tags to organize this record (e.g. ['architecture', 'decision'])",
    })),
});
// memory.log — ambient observation
const LogParams = Type.Object({
    content: Type.String({
        description: "An observation, event, or note worth remembering.",
    }),
    sessionKey: Type.Optional(Type.String({ description: "Override the current session key." })),
    tags: Type.Optional(Type.Array(Type.String())),
});
// memory.flag — agent self-reported uncertainty
const FlagParams = Type.Object({
    content: Type.String({
        description: "What the agent is uncertain about — a hypothesis, a guess, or a known gap.",
    }),
    confidence: Type.Number({
        description: "Confidence score from 0.0 (complete guess) to 1.0 (certain).",
        minimum: 0,
        maximum: 1,
    }),
    sessionKey: Type.Optional(Type.String({ description: "Override the current session key." })),
});
// memory.task — track a task through status transitions
const TaskParams = Type.Object({
    title: Type.String({
        description: "Short, descriptive title for the task.",
    }),
    status: Type.Union([
        Type.Literal("todo"),
        Type.Literal("in_progress"),
        Type.Literal("done"),
        Type.Literal("blocked"),
    ], { description: "Current status of the task." }),
    sessionKey: Type.Optional(Type.String({ description: "Override the current session key." })),
    parentEventId: Type.Optional(Type.String({
        description: "Link this status change to a prior task event.",
    })),
});
// memory_search — search Lethe events across sessions
const SearchParams = Type.Object({
    query: Type.String({
        description: "Search terms — keywords, phrases, or topic to find in stored memory events.",
    }),
    limit: Type.Optional(Type.Number({
        description: "Maximum number of results to return (default 10).",
        default: 10,
        minimum: 1,
        maximum: 50,
    })),
    sessionKey: Type.Optional(Type.String({ description: "Override the current session key for session-scoped search." })),
    eventType: Type.Optional(Type.Union([
        Type.Literal("record"),
        Type.Literal("log"),
        Type.Literal("flag"),
        Type.Literal("task"),
    ], { description: "Filter by event type (record, log, flag, task)." })),
});
// ---------------------------------------------------------------------------
// LetheTools
// ---------------------------------------------------------------------------
export class LetheTools {
    cfg;
    constructor(cfg) {
        this.cfg = cfg;
    }
    // ---------------------------------------------------------------------------
    // Tool factories
    // ---------------------------------------------------------------------------
    getRecordTool() {
        return this.makeTool({
            name: "memory.record",
            description: "Record a deliberate decision the agent has made, including the reasoning behind it. Use this for architecture choices, trade-off resolutions, and any conclusions reached during the session.",
            params: RecordParams,
            label: "Record Decision",
            execute: async (toolCallId, params) => {
                const { content, sessionKey, tags } = params;
                const { endpoint, apiKey, agentId, projectId } = this.cfg;
                const sk = sessionKey ?? agentId;
                const res = await lethePost(endpoint, apiKey, `/sessions/${sk}/events`, {
                    event_type: "record",
                    content,
                    tags: tags ?? [],
                });
                if (!res.ok) {
                    const err = await res.text();
                    return {
                        content: [{ type: "text", text: `Lethe error: ${err}` }],
                        details: { ok: false, error: err },
                    };
                }
                const data = await res.json();
                return {
                    content: [
                        {
                            type: "text",
                            text: `Recorded: ${content}${tags?.length ? ` [${tags.join(", ")}]` : ""}`,
                        },
                    ],
                    details: { ok: true, event_id: data.event_id },
                };
            },
        });
    }
    getLogTool() {
        return this.makeTool({
            name: "memory.log",
            description: "Log an ambient observation, event, or note. Lower stakes than record — use this to track what's happening without requiring structured reasoning.",
            params: LogParams,
            label: "Log Observation",
            execute: async (toolCallId, params) => {
                const { content, sessionKey, tags } = params;
                const { endpoint, apiKey, agentId, projectId } = this.cfg;
                const sk = sessionKey ?? agentId;
                const res = await lethePost(endpoint, apiKey, `/sessions/${sk}/events`, {
                    event_type: "log",
                    content,
                    tags: tags ?? [],
                });
                if (!res.ok) {
                    const err = await res.text();
                    return {
                        content: [{ type: "text", text: `Lethe error: ${err}` }],
                        details: { ok: false, error: err },
                    };
                }
                const data = await res.json();
                return {
                    content: [{ type: "text", text: `Logged: ${content}` }],
                    details: { ok: true, event_id: data.event_id },
                };
            },
        });
    }
    getFlagTool() {
        return this.makeTool({
            name: "memory.flag",
            description: "Flag a knowledge gap, uncertainty, or educated guess. The confidence score surfaces this for human review. Use when you know you're working with incomplete information.",
            params: FlagParams,
            label: "Flag Uncertainty",
            execute: async (toolCallId, params) => {
                const { content, confidence, sessionKey } = params;
                const { endpoint, apiKey, agentId, projectId } = this.cfg;
                const sk = sessionKey ?? agentId;
                const res = await lethePost(endpoint, apiKey, `/sessions/${sk}/events`, {
                    event_type: "flag",
                    content,
                    confidence,
                });
                if (!res.ok) {
                    const err = await res.text();
                    return {
                        content: [{ type: "text", text: `Lethe error: ${err}` }],
                        details: { ok: false, error: err },
                    };
                }
                const data = await res.json();
                return {
                    content: [
                        {
                            type: "text",
                            text: `Flagged (confidence ${confidence}): ${content}`,
                        },
                    ],
                    details: { ok: true, event_id: data.event_id },
                };
            },
        });
    }
    getTaskTool() {
        return this.makeTool({
            name: "memory.task",
            description: "Track a task through status transitions (todo → in_progress → done | blocked). Each transition is recorded as a separate event with a parent link, building a full audit trail.",
            params: TaskParams,
            label: "Update Task",
            execute: async (toolCallId, params) => {
                const { title, status, sessionKey, parentEventId } = params;
                const { endpoint, apiKey, agentId, projectId } = this.cfg;
                const sk = sessionKey ?? agentId;
                const res = await lethePost(endpoint, apiKey, `/sessions/${sk}/events`, {
                    event_type: "task",
                    content: title,
                    task_status: status,
                    parent_event_id: parentEventId,
                });
                if (!res.ok) {
                    const err = await res.text();
                    return {
                        content: [{ type: "text", text: `Lethe error: ${err}` }],
                        details: { ok: false, error: err },
                    };
                }
                const data = await res.json();
                return {
                    content: [
                        {
                            type: "text",
                            text: `Task [${status}]: ${title}`,
                        },
                    ],
                    details: { ok: true, event_id: data.event_id, status },
                };
            },
        });
    }
    // ---------------------------------------------------------------------------
    // memory_search — search Lethe events across sessions
    // ---------------------------------------------------------------------------
    getSearchTool() {
        return this.makeTool({
            name: "memory_search",
            description: "Search Lethe memory for past decisions, observations, flags, and tasks. " +
                "Use this before re-reasoning about prior work, past decisions, or context " +
                "from previous sessions. Returns matching events sorted by recency.",
            params: SearchParams,
            label: "Search Memory",
            execute: async (toolCallId, params) => {
                const { query, limit, sessionKey, eventType } = params;
                const { endpoint, apiKey, agentId } = this.cfg;
                // Build search URL with query params
                const searchParams = new URLSearchParams({ q: query });
                if (limit)
                    searchParams.set("limit", String(limit));
                if (eventType)
                    searchParams.set("event_type", eventType);
                try {
                    // Search across all sessions first (broader recall)
                    const res = await fetch(`${endpoint}/events/search?${searchParams.toString()}`, { method: "GET", headers: letheHeaders(apiKey) });
                    if (!res.ok) {
                        const err = await res.text();
                        return {
                            content: [{ type: "text", text: `Lethe search error: ${err}` }],
                            details: { ok: false, error: err },
                        };
                    }
                    const data = await res.json();
                    const events = data.events ?? [];
                    const count = data.count ?? events.length;
                    if (events.length === 0) {
                        // If no cross-session results, try session-scoped search
                        const sk = sessionKey ?? agentId;
                        const sessionRes = await fetch(`${endpoint}/sessions/${encodeURIComponent(sk)}/events/search?${searchParams.toString()}`, { method: "GET", headers: letheHeaders(apiKey) });
                        if (sessionRes.ok) {
                            const sessionData = await sessionRes.json();
                            const sessionEvents = sessionData.events ?? [];
                            if (sessionEvents.length > 0) {
                                const formatted = sessionEvents
                                    .map(formatEvent)
                                    .join("\n---\n");
                                return {
                                    content: [{
                                            type: "text",
                                            text: `Found ${sessionData.count ?? sessionEvents.length} result(s) in session:\n${formatted}`,
                                        }],
                                    details: { ok: true, count: sessionData.count ?? sessionEvents.length, scope: "session" },
                                };
                            }
                        }
                        return {
                            content: [{ type: "text", text: "No matching events found in Lethe memory." }],
                            details: { ok: true, count: 0 },
                        };
                    }
                    const formatted = events.map(formatEvent).join("\n---\n");
                    return {
                        content: [{
                                type: "text",
                                text: `Found ${count} result(s) across sessions:\n${formatted}`,
                            }],
                        details: { ok: true, count, scope: "global" },
                    };
                }
                catch (err) {
                    return {
                        content: [{ type: "text", text: `Lethe search failed: ${err.message ?? err}` }],
                        details: { ok: false, error: err.message },
                    };
                }
            },
        });
    }
    // ---------------------------------------------------------------------------
    // Tool builder
    // ---------------------------------------------------------------------------
    makeTool({ name, description, params, label, execute, }) {
        return {
            name,
            description,
            parameters: params,
            label,
            execute,
        };
    }
}
// Format a Lethe event for display in tool results
function formatEvent(event) {
    const type = event.event_type ?? "log";
    const date = event.created_at
        ? new Date(event.created_at).toISOString().split("T")[0]
        : "unknown";
    const tags = event.tags
        ? (Array.isArray(event.tags) ? event.tags : event.tags.split(",")).filter((t) => t)
        : [];
    const tagStr = tags.length ? ` [${tags.join(", ")}]` : "";
    const confidence = event.confidence != null ? ` (confidence: ${event.confidence})` : "";
    const content = (event.content ?? "").substring(0, 500);
    const truncated = event.content && event.content.length > 500 ? "..." : "";
    let prefix = `[${type}]`;
    if (type === "task" && event.task_status) {
        prefix = `[task:${event.task_status}]`;
    }
    if (type === "flag") {
        prefix = `[flag]`;
    }
    return `${date} ${prefix}${tagStr}${confidence}: ${content}${truncated}`;
}
