import { delegateCompactionToRuntime, } from "openclaw/plugin-sdk/core";
// Throttled warning for assembly report failures.
let assemblyWarnCount = 0;
let requestWarnCount = 0;
const ASSEMBLY_WARN_MAX = 3;
const REQUEST_WARN_MAX = 5;
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
// Request deadline and response body cap. A Lethe server that accepts a
// connection but stalls (headers or body) must not block agent hooks
// indefinitely, and an unbounded response body must not exhaust memory.
// Both knobs are parsed defensively so a bad value can never wedge hooks.
const DEFAULT_FETCH_TIMEOUT_MS = 10_000;
const MIN_FETCH_TIMEOUT_MS = 1_000;
const MAX_FETCH_TIMEOUT_MS = 120_000;
const DEFAULT_MAX_BODY_BYTES = 5 * 1024 * 1024;
const MIN_MAX_BODY_BYTES = 1_024;
const MAX_MAX_BODY_BYTES = 64 * 1024 * 1024;
function envInt(name, fallback, min, max) {
    const raw = process.env[name];
    if (!raw)
        return fallback;
    const parsed = Number.parseInt(raw, 10);
    if (!Number.isFinite(parsed))
        return fallback;
    return Math.min(max, Math.max(min, parsed));
}
function fetchTimeoutMs() {
    return envInt("LETHE_FETCH_TIMEOUT_MS", DEFAULT_FETCH_TIMEOUT_MS, MIN_FETCH_TIMEOUT_MS, MAX_FETCH_TIMEOUT_MS);
}
function maxBodyBytes() {
    return envInt("LETHE_FETCH_MAX_BODY_BYTES", DEFAULT_MAX_BODY_BYTES, MIN_MAX_BODY_BYTES, MAX_MAX_BODY_BYTES);
}
// Reads a response body with a hard byte cap. Rejects early when a declared
// content-length already exceeds the cap, and aborts mid-stream when the
// actual bytes do. Returns the raw bytes for a bounded re-materialization.
async function readCappedBody(response, cap) {
    if (!response.body)
        return new Uint8Array(0);
    const declared = Number(response.headers.get("content-length"));
    if (Number.isFinite(declared) && declared > cap) {
        throw new Error(`Lethe response body too large: content-length ${declared} exceeds cap of ${cap} bytes`);
    }
    const reader = response.body.getReader();
    const chunks = [];
    let total = 0;
    for (;;) {
        const { done, value } = await reader.read();
        if (done)
            break;
        total += value.byteLength;
        if (total > cap) {
            await reader.cancel().catch(() => { });
            throw new Error(`Lethe response body exceeded cap of ${cap} bytes`);
        }
        chunks.push(value);
    }
    const body = new Uint8Array(total);
    let offset = 0;
    for (const chunk of chunks) {
        body.set(chunk, offset);
        offset += chunk.byteLength;
    }
    return body;
}
export async function letheFetch(endpoint, apiKey, path, body, signal) {
    const timeoutMs = fetchTimeoutMs();
    const controller = new AbortController();
    let timedOut = false;
    const timer = setTimeout(() => {
        timedOut = true;
        controller.abort();
    }, timeoutMs);
    // Respect a caller-provided cancellation signal alongside the deadline.
    const onCallerAbort = () => controller.abort();
    if (signal) {
        if (signal.aborted)
            controller.abort();
        else
            signal.addEventListener("abort", onCallerAbort, { once: true });
    }
    try {
        const response = await fetch(`${endpoint}${path}`, {
            method: body ? "POST" : "GET",
            headers: letheHeaders(apiKey),
            body: body ? JSON.stringify(body) : undefined,
            signal: controller.signal,
        });
        // The deadline stays armed across the body read: a server that sends
        // headers but stalls the body is capped by the same timeout.
        const bytes = await readCappedBody(response, maxBodyBytes());
        return new Response(bytes.length > 0 ? bytes : null, {
            status: response.status,
            statusText: response.statusText,
        });
    }
    catch (err) {
        if (timedOut) {
            throw new Error(`Lethe request to ${path} timed out after ${timeoutMs}ms`);
        }
        throw err;
    }
    finally {
        clearTimeout(timer);
        signal?.removeEventListener("abort", onCallerAbort);
    }
}
async function bestEffortPost(endpoint, apiKey, path, body, operation) {
    try {
        const response = await letheFetch(endpoint, apiKey, path, body);
        if (!response.ok) {
            warnLetheResponse(operation, response);
            return false;
        }
        return true;
    }
    catch (err) {
        warnLetheFailure(operation, err?.message || "network error");
        return false;
    }
}
function estimateTokens(text) {
    return Math.ceil(text.length / 4);
}
function warnLetheFailure(operation, detail) {
    if (requestWarnCount >= REQUEST_WARN_MAX)
        return;
    requestWarnCount++;
    console.warn(`[Lethe] ${operation} failed (${detail}, count=${requestWarnCount}/${REQUEST_WARN_MAX}). ` +
        "Check the configured endpoint and apiKey; memory/checkpoint data was not recorded.");
}
function warnLetheResponse(operation, response) {
    warnLetheFailure(operation, `HTTP ${response.status}`);
}
// ---------------------------------------------------------------------------
// LetheContextEngine
// ---------------------------------------------------------------------------
export class LetheContextEngine {
    cfg;
    info = {
        id: "mentholmike-lethe",
        name: "Lethe",
        version: "0.4.0",
        ownsCompaction: true,
    };
    constructor(cfg) {
        this.cfg = cfg;
    }
    // ------------------------------------------------------------------
    // bootstrap
    // ------------------------------------------------------------------
    // Uses sessionKey as the stable Lethe session_id. On first boot creates
    // the session via POST /api/sessions. On subsequent boots checks for an
    // interrupted session and resumes it with a summary injection.
    // ------------------------------------------------------------------
    async bootstrap({ sessionId, sessionKey, }) {
        const { endpoint, apiKey, agentId, projectId } = this.cfg;
        if (!sessionKey) {
            return { bootstrapped: false, reason: "no sessionKey" };
        }
        try {
            // Try to get existing session by sessionKey.
            const res = await letheFetch(endpoint, apiKey, `/api/sessions/${encodeURIComponent(sessionKey)}`);
            if (res.ok) {
                const session = await res.json();
                if (session.state === "interrupted") {
                    const summaryRes = await letheFetch(endpoint, apiKey, `/api/sessions/${encodeURIComponent(sessionKey)}/summary`);
                    if (summaryRes.ok) {
                        const summary = await summaryRes.json();
                        return {
                            bootstrapped: true,
                            systemPromptAddition: summaryPrompt(summary),
                            sessionEventCount: summary.event_count ?? 0,
                        };
                    }
                    warnLetheResponse("bootstrap summary", summaryRes);
                }
                // Session already exists and is active.
                // Fetch event count so assemble() can decide whether to use recent events.
                let sessionEventCount = 0;
                try {
                    const summaryRes = await letheFetch(endpoint, apiKey, `/api/sessions/${encodeURIComponent(sessionKey)}/summary`);
                    if (summaryRes.ok) {
                        const summary = await summaryRes.json();
                        sessionEventCount = summary.event_count ?? 0;
                    }
                    else {
                        warnLetheResponse("bootstrap event count", summaryRes);
                    }
                }
                catch (err) {
                    warnLetheFailure("bootstrap event count", err?.message || "network error");
                }
                return { bootstrapped: true, sessionEventCount };
            }
            if (res.status === 401 || res.status === 403) {
                warnLetheResponse("bootstrap authentication", res);
                return { bootstrapped: false, reason: `Lethe authentication failed (HTTP ${res.status})` };
            }
            if (res.status !== 404) {
                warnLetheResponse("bootstrap session lookup", res);
                return { bootstrapped: false, reason: `Lethe session lookup failed (HTTP ${res.status})` };
            }
            // Session doesn't exist yet — create it.
            // Use sessionKey as the Lethe session_id so we can look it up later.
            const createRes = await letheFetch(endpoint, apiKey, "/api/sessions", {
                session_key: sessionKey,
                agent_id: agentId,
                project_id: projectId,
            });
            if (!createRes.ok && createRes.status !== 409) {
                warnLetheResponse("bootstrap session create", createRes);
                return { bootstrapped: false, reason: `failed to create session (HTTP ${createRes.status})` };
            }
            return { bootstrapped: true };
        }
        catch (err) {
            warnLetheFailure("bootstrap", err?.message || "network error");
            return { bootstrapped: false, reason: "network error during bootstrap" };
        }
    }
    // ------------------------------------------------------------------
    // ingest — passive log ingestion on non-heartbeat turns
    // ------------------------------------------------------------------
    async ingest({ sessionId, sessionKey, message, isHeartbeat, }) {
        if (isHeartbeat || !sessionKey)
            return { ingested: false };
        if (!this.cfg.autoLog)
            return { ingested: true };
        const { endpoint, apiKey } = this.cfg;
        try {
            const logContent = messageToLogContent(message);
            // Skip if content would be empty/whitespace
            if (!logContent || !logContent.trim()) {
                return { ingested: true };
            }
            const res = await letheFetch(endpoint, apiKey, `/api/sessions/${encodeURIComponent(sessionKey)}/events`, {
                event_type: "log",
                content: logContent,
                tags: [],
            });
            if (!res.ok)
                warnLetheResponse("event ingest", res);
            return { ingested: res.ok };
        }
        catch (err) {
            warnLetheFailure("event ingest", err?.message || "network error");
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
            await bestEffortPost(endpoint, apiKey, `/api/sessions/${encodeURIComponent(sessionKey)}/heartbeat`, { token_budget: tokenBudget }, "heartbeat");
            return;
        }
        // Real turn: write a checkpoint. Optional diagnostic auto event logging is
        // off by default so normal tool use does not become permanent memory.
        const lastMsg = messages[messages.length - 1];
        const openThreads = extractOpenThreads(lastMsg);
        const lastTool = lastMsg ? extractLastTool(lastMsg) : null;
        const allTools = this.cfg.autoLog && lastMsg ? extractAllToolCallNames(lastMsg) : [];
        // Write checkpoint.
        await bestEffortPost(endpoint, apiKey, `/api/sessions/${encodeURIComponent(sessionKey)}/checkpoints`, {
            snapshot: {
                open_threads: openThreads,
                recent_event_ids: [],
                current_task: "",
                last_tool: lastTool,
            },
        }, "checkpoint");
        // Optional auto-log: tools used (only if there were actual tool calls, not just text).
        if (this.cfg.autoLog && allTools.length > 0) {
            await bestEffortPost(endpoint, apiKey, `/api/sessions/${encodeURIComponent(sessionKey)}/events`, {
                event_type: "log",
                content: `tools: ${allTools.join(" → ")}`,
                tags: ["auto", "tool-call"],
            }, "tool auto-log");
        }
        // Optional auto-log: open threads detected in the conversation.
        if (this.cfg.autoLog && openThreads.length > 0) {
            await bestEffortPost(endpoint, apiKey, `/api/sessions/${encodeURIComponent(sessionKey)}/events`, {
                event_type: "log",
                content: `threads: ${openThreads.join(" | ")}`,
                tags: ["auto", "thread"],
            }, "thread auto-log");
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
        const { endpoint, apiKey, agentId, projectId } = this.cfg;
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
            const res = await letheFetch(endpoint, apiKey, `/api/sessions/${encodeURIComponent(sessionKey)}/summary`);
            if (res.ok) {
                summaryData = await res.json();
                const rawSummary = summaryData.summary ?? summaryData.session?.summary ?? null;
                if (rawSummary) {
                    summaryText = rawSummary;
                    summaryTokens = estimateTokens(summaryText);
                }
                sessionEventCount = summaryData.event_count ?? 0;
            }
            else {
                warnLetheResponse("session summary", res);
            }
        }
        catch (err) {
            warnLetheFailure("session summary", err?.message || "network error");
        }
        // OpenLethe's default path is session/event memory only. Memory Git context
        // belongs to Charon; this adapter remains opt-in for migration experiments.
        let memoryContext = null;
        let acceptedText = "";
        let acceptedTokens = 0;
        const conversationTokens = estimateTokens(JSON.stringify(messages));
        if (this.cfg.memoryGitContext) {
            const acceptedBudget = tokenBudget
                ? Math.max(0, tokenBudget - summaryTokens - conversationTokens - 300)
                : 2000;
            const acceptedLimit = tokenBudget && tokenBudget < 1000 ? 5 : 12;
            try {
                const contextRes = await letheFetch(endpoint, apiKey, `/api/memory/${encodeURIComponent(projectId)}/context`, {
                    ref_name: "refs/shared/main",
                    query: params.prompt || latestUserText(messages),
                    limit: acceptedLimit,
                    create_manifest: false,
                });
                if (contextRes.ok) {
                    const projected = (await contextRes.json());
                    const fitted = fitMemoryContextToBudget(projected, acceptedBudget);
                    const manifest = await pinMemoryContext(endpoint, apiKey, fitted.context, sessionKey, agentId, fitted.droppedMemoryIDs);
                    if (manifest) {
                        memoryContext = { ...fitted.context, manifest_id: manifest.manifest_id };
                        acceptedText = fitted.promptFits ? acceptedMemoryPrompt(memoryContext) : "";
                        acceptedTokens = estimateTokens(acceptedText);
                    }
                }
                else {
                    warnLetheResponse("accepted memory context", contextRes);
                }
            }
            catch (err) {
                warnLetheFailure("accepted memory context", err?.message || "network error");
            }
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
                ? Math.max(0, tokenBudget - acceptedTokens - summaryTokens - 200 - conversationTokens)
                : undefined;
            const effectiveLimit = budgetForRecent && budgetForRecent < 200 ? 3 : HARD_LIMIT;
            if (!budgetForRecent || budgetForRecent > 50) {
                // Use the already-fetched recent_events from the summary response.
                // The summary endpoint returns the newest 20 events, newest-first.
                // We take the newest effectiveLimit, then reverse for chronological order.
                const availableRecentEvents = Array.isArray(summaryData?.recent_events)
                    ? summaryData.recent_events
                    : [];
                const selectedNewestFirst = availableRecentEvents.slice(0, effectiveLimit);
                const selectedChronological = selectedNewestFirst.slice().reverse();
                recentEvents = selectedChronological.map(eventToMessage);
                recentTokens = estimateTokens(JSON.stringify(recentEvents));
            }
        }
        const systemPromptAddition = buildSystemPromptAddition(acceptedText, memoryContext, summaryText, recentTokens);
        const assembledMessages = [
            ...(acceptedText ? [makeAcceptedMemoryMessage(acceptedText)] : []),
            ...(summaryText ? [makeSummaryMessage(summaryText)] : []),
            ...recentEvents,
            ...messages,
        ];
        // Best-effort: report the assembly to Lethe ledger.
        const assemblyReport = await this.reportAssembly({
            sessionKey,
            summaryText,
            recentEvents,
            acceptedText,
            memoryContext,
            messages: messages,
            summaryTokens,
            recentTokens,
            acceptedTokens,
            tokenBudget,
            shouldSkipRecentEvents,
        });
        return {
            messages: assembledMessages,
            estimatedTokens: acceptedTokens + summaryTokens + recentTokens + conversationTokens,
            systemPromptAddition,
            assemblyId: assemblyReport?.assembly_id,
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
            const res = await letheFetch(endpoint, apiKey, `/api/sessions/${encodeURIComponent(sessionKey)}/compact`, { token_budget: tokenBudget, force });
            if (!res.ok) {
                warnLetheResponse("compaction", res);
                return delegateCompactionToRuntime(params);
            }
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
        catch (err) {
            warnLetheFailure("compaction", err?.message || "network error");
            return delegateCompactionToRuntime(params);
        }
    }
    async dispose() { }
    // ------------------------------------------------------------------
    // reportAssembly — best-effort ledger of what was sent to the LLM
    // ------------------------------------------------------------------
    async reportAssembly(params) {
        const { endpoint, apiKey } = this.cfg;
        const assemblyId = generateAssemblyId();
        const items = [];
        let ordinal = 0;
        // Summary item (ordinal 0).
        if (params.summaryText) {
            const summaryBytes = new TextEncoder().encode(params.summaryText);
            items.push({
                ordinal,
                item_kind: "summary",
                bucket: "summary",
                content_snapshot: params.summaryText.substring(0, 500),
                content_sha256: await sha256Hex(params.summaryText),
                packed_bytes: summaryBytes.length,
                estimated_tokens: params.summaryTokens,
            });
            ordinal++;
        }
        // Recent event items.
        for (const event of params.recentEvents) {
            const text = extractText(event);
            const eventBytes = new TextEncoder().encode(text);
            items.push({
                ordinal,
                item_kind: "event",
                bucket: "recent",
                event_id: event.id,
                content_snapshot: text.substring(0, 500),
                content_sha256: await sha256Hex(text),
                packed_bytes: eventBytes.length,
                estimated_tokens: estimateTokens(text),
            });
            ordinal++;
        }
        // Only summary and event items are stored in the ledger.
        // Conversation metadata is reported at the top level but never stored
        // as items (no prompt / message content in telemetry per section 11.5).
        const convoText = JSON.stringify(params.messages);
        const acceptedBytes = new TextEncoder().encode(params.acceptedText).length;
        const totalBytes = items.reduce((sum, i) => sum + i.packed_bytes, 0) + acceptedBytes;
        const totalTokens = params.acceptedTokens + params.summaryTokens + params.recentTokens + estimateTokens(convoText);
        const report = {
            assembly_id: assemblyId,
            source: "openclaw-plugin",
            plugin_version: this.info.version ?? "0.4.0",
            assembler_version: "openclaw-memory-git-v1",
            message_count: params.messages.length,
            provided_token_budget: params.tokenBudget,
            estimator_id: "js-utf16-length-div-4-v1",
            summary_estimated_tokens: params.summaryTokens || undefined,
            recent_estimated_tokens: params.recentTokens || undefined,
            accepted_estimated_tokens: params.acceptedTokens || undefined,
            conversation_estimated_tokens: estimateTokens(convoText),
            total_estimated_tokens: totalTokens,
            packed_bytes: totalBytes,
            recent_skipped: params.shouldSkipRecentEvents,
            skip_reason: params.shouldSkipRecentEvents ? "session-age-heuristic" : undefined,
            memory_manifest_id: params.memoryContext?.manifest_id,
            memory_head_changeset_id: params.memoryContext?.head_changeset_id,
            notes: `items: ${items.length}; accepted_memories: ${params.memoryContext?.memories.length ?? 0}`,
            items,
        };
        try {
            const res = await letheFetch(endpoint, apiKey, `/api/sessions/${encodeURIComponent(params.sessionKey)}/assemblies`, report);
            if (res.ok) {
                return { assembly_id: assemblyId };
            }
            warnLetheResponse("assembly report", res);
        }
        catch (err) {
            if (assemblyWarnCount < ASSEMBLY_WARN_MAX) {
                assemblyWarnCount++;
                console.warn(`[Lethe] assembly report failed (assembly_id=${assemblyId}, count=${assemblyWarnCount}/${ASSEMBLY_WARN_MAX}): ${err?.message || "unknown"}`);
            }
        }
        return null;
    }
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
function fitMemoryContextToBudget(context, maxTokens) {
    const memories = Array.isArray(context.memories) ? [...context.memories] : [];
    const droppedMemoryIDs = [];
    while (memories.length > 0) {
        const probe = {
            ...context,
            manifest_id: "m".repeat(128),
            memories,
        };
        if (estimateTokens(acceptedMemoryPrompt(probe)) <= maxTokens)
            break;
        const dropped = memories.pop();
        if (dropped)
            droppedMemoryIDs.push(dropped.memory_id);
    }
    const exclusionReasons = { ...(context.exclusion_reasons ?? {}) };
    for (const id of droppedMemoryIDs) {
        exclusionReasons[id] = "excluded by OpenClaw accepted-memory token budget";
    }
    const fittedContext = {
        ...context,
        manifest_id: undefined,
        memories,
        exclusion_reasons: exclusionReasons,
    };
    const budgetProbe = { ...fittedContext, manifest_id: "m".repeat(128) };
    return {
        context: fittedContext,
        droppedMemoryIDs,
        promptFits: estimateTokens(acceptedMemoryPrompt(budgetProbe)) <= maxTokens,
    };
}
async function pinMemoryContext(endpoint, apiKey, context, sessionID, actorID, droppedMemoryIDs) {
    const selectedMemoryIDs = context.memories.map((memory) => memory.memory_id);
    const selected = new Set(selectedMemoryIDs);
    const inclusionReasons = Object.fromEntries(Object.entries(context.inclusion_reasons ?? {}).filter(([id]) => selected.has(id)));
    for (const memory of context.memories) {
        if (!inclusionReasons[memory.memory_id]) {
            inclusionReasons[memory.memory_id] = "selected within OpenClaw token budget";
        }
    }
    const exclusionReasons = { ...(context.exclusion_reasons ?? {}) };
    for (const id of droppedMemoryIDs) {
        exclusionReasons[id] = "excluded by OpenClaw accepted-memory token budget";
    }
    try {
        const response = await letheFetch(endpoint, apiKey, "/api/memory/manifests", {
            direction: "input",
            project_id: context.project_id,
            ref_name: context.ref_name,
            head_changeset_id: context.head_changeset_id,
            selected_memory_ids: selectedMemoryIDs,
            inclusion_reasons: inclusionReasons,
            exclusion_reasons: exclusionReasons,
            unresolved_conflicts: context.unresolved_conflicts ?? [],
            session_id: sessionID,
            actor_id: actorID,
        });
        if (!response.ok) {
            warnLetheResponse("accepted memory manifest", response);
            return null;
        }
        const manifest = await response.json();
        if (!manifest?.manifest_id) {
            warnLetheFailure("accepted memory manifest", "response missing manifest_id");
            return null;
        }
        return { manifest_id: manifest.manifest_id };
    }
    catch (err) {
        warnLetheFailure("accepted memory manifest", err?.message || "network error");
        return null;
    }
}
function acceptedMemoryPrompt(context) {
    const memories = Array.isArray(context.memories) ? context.memories : [];
    const conflictIDs = context.unresolved_conflicts ?? [];
    if (memories.length === 0 && conflictIDs.length === 0)
        return "";
    const manifest = context.manifest_id ? `Manifest: ${context.manifest_id}\n` : "";
    if (memories.length === 0) {
        const shown = conflictIDs.slice(0, 5);
        const remainder = conflictIDs.length - shown.length;
        return ("## Accepted Memory Conflict Warning\n\n" +
            manifest +
            `Head: ${context.head_changeset_id}\n` +
            `Unresolved conflicts (${conflictIDs.length}): ${shown.join(", ")}` +
            (remainder > 0 ? ` (+${remainder} more in manifest)` : "") +
            "\nReview these conflicts before treating project memory as canonical.");
    }
    const lines = memories.map((memory) => {
        const label = memory.kind || memory.event_type || "memory";
        const scope = memory.scope ? `/${memory.scope}` : "";
        return `- [${label}${scope}] ${memory.content}`;
    });
    const conflicts = context.unresolved_conflicts && context.unresolved_conflicts.length > 0
        ? `Unresolved conflicts requiring review: ${context.unresolved_conflicts.join(", ")}\n`
        : "";
    return ("## Accepted Project Memory\n\n" +
        `Ref: ${context.ref_name} @ ${context.head_changeset_id}\n` +
        manifest +
        `Projection: ${context.projection_version}\n` +
        conflicts +
        "\n" +
        lines.join("\n"));
}
function buildSystemPromptAddition(acceptedText, context, summaryText, recentTokens) {
    if (!acceptedText && !summaryText && recentTokens === 0)
        return "";
    const parts = [];
    if (acceptedText) {
        parts.push(`Accepted project memory is pinned to manifest ${context?.manifest_id ?? "unavailable"} ` +
            `at ${context?.ref_name ?? "refs/shared/main"} @ ${context?.head_changeset_id ?? "unknown"}. ` +
            "Treat it as canonical; unresolved conflicts are review items, not facts.");
    }
    if (summaryText) {
        parts.push(`Previous session summary (${estimateTokens(summaryText)} tokens):\n${summaryText}`);
    }
    if (recentTokens > 0) {
        parts.push(`Recent session events (~${recentTokens} tokens). Full history is available through Lethe.`);
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
function latestUserText(messages) {
    for (let i = messages.length - 1; i >= 0; i--) {
        if (messages[i]?.role === "user")
            return extractText(messages[i]);
    }
    return "";
}
function makeAcceptedMemoryMessage(text) {
    return {
        id: "lethe-accepted-memory",
        role: "assistant",
        content: [{ type: "text", text }],
        api: "unknown",
        provider: "unknown",
        model: "unknown",
        usage: {
            input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0,
            cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
        },
        stopReason: "stop",
        timestamp: Date.now(),
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
function generateAssemblyId() {
    return `asm-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}
async function sha256Hex(text) {
    const encoder = new TextEncoder();
    const data = encoder.encode(text);
    const hashBuffer = await crypto.subtle.digest("SHA-256", data);
    const hashArray = Array.from(new Uint8Array(hashBuffer));
    return hashArray.map((b) => b.toString(16).padStart(2, "0")).join("");
}
