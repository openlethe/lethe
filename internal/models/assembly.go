package models

import "time"

// ContextAssembly records the exact Lethe summary and event IDs selected by
// a client after the client completes its own assembly process.
type ContextAssembly struct {
	AssemblyID string `json:"assembly_id"`
	SessionID  string `json:"session_id"`
	ProjectID  string `json:"project_id"`

	Source           string `json:"source"`
	PluginVersion    string `json:"plugin_version,omitempty"`
	AssemblerVersion string `json:"assembler_version"`

	MessageCount        int  `json:"message_count"`
	ProvidedTokenBudget *int `json:"provided_token_budget,omitempty"`

	EstimatorID string `json:"estimator_id,omitempty"`

	SummaryEstimatedTokens      *int `json:"summary_estimated_tokens,omitempty"`
	RecentEstimatedTokens       *int `json:"recent_estimated_tokens,omitempty"`
	ConversationEstimatedTokens *int `json:"conversation_estimated_tokens,omitempty"`
	TotalEstimatedTokens        *int `json:"total_estimated_tokens,omitempty"`

	PackedBytes   int    `json:"packed_bytes"`
	RecentSkipped bool   `json:"recent_skipped"`
	SkipReason    string `json:"skip_reason,omitempty"`
	Notes         string `json:"notes,omitempty"`

	CreatedAt time.Time             `json:"created_at"`
	Items     []ContextAssemblyItem `json:"items,omitempty"`
}

// ContextAssemblyItem is a single item within an assembly (summary or event).
type ContextAssemblyItem struct {
	Ordinal int `json:"ordinal"`

	ItemKind string `json:"item_kind"`
	Bucket   string `json:"bucket"`

	EventID         string `json:"event_id,omitempty"`
	ContentSnapshot string `json:"content_snapshot,omitempty"`
	ContentSHA256   string `json:"content_sha256"`

	PackedBytes     int  `json:"packed_bytes"`
	EstimatedTokens *int `json:"estimated_tokens,omitempty"`
}

// ContextAssemblyFeedback is user feedback on a specific assembly.
type ContextAssemblyFeedback struct {
	FeedbackID     string    `json:"feedback_id"`
	AssemblyID     string    `json:"assembly_id"`
	Verdict        string    `json:"verdict"`
	RelatedEventID string    `json:"related_event_id,omitempty"`
	Note           string    `json:"note,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}
