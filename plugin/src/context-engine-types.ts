// ContextEngine result types — copied from openclaw/plugin-sdk since they're
// not re-exported from the public plugin-sdk surface.
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
