export type BootstrapResult = {
    bootstrapped: boolean;
    importedMessages?: number;
    reason?: string;
    systemPromptAddition?: string;
    /** Number of events in the session so assemble() can decide whether to surface them. */
    sessionEventCount?: number;
};
export type AssembleResult = {
    messages: any[];
    estimatedTokens: number;
    systemPromptAddition?: string;
    /** Lethe assembly ID if reported. */
    assemblyId?: string;
};
export type AssemblyReport = {
    assembly_id: string;
    source: string;
    plugin_version: string;
    assembler_version: string;
    message_count: number;
    provided_token_budget?: number;
    estimator_id: string;
    summary_estimated_tokens?: number;
    recent_estimated_tokens?: number;
    conversation_estimated_tokens?: number;
    total_estimated_tokens?: number;
    packed_bytes: number;
    recent_skipped: boolean;
    skip_reason?: string;
    notes?: string;
    items: AssemblyItem[];
};
export type AssemblyItem = {
    ordinal: number;
    item_kind: "summary" | "event";
    bucket: string;
    event_id?: string;
    content_snapshot?: string;
    content_sha256: string;
    packed_bytes: number;
    estimated_tokens?: number;
};
export type CompactResult = {
    ok: boolean;
    compacted: boolean;
    reason?: string;
    result?: {
        summary?: string;
        firstKeptEntryId?: string;
        tokensBefore: number;
        tokensAfter?: number;
        details?: unknown;
    };
};
export type IngestResult = {
    ingested: boolean;
};
export type ContextEngineRuntimeContext = Record<string, unknown> & {
    rewriteTranscriptEntries?: (request: any) => Promise<any>;
};
