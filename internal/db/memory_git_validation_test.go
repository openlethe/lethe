package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/openlethe/lethe/internal/models"
)

// Tests for the stateless payload contract: size caps, the per-op allowed
// payload key sets, and ambiguous-identifier rejection. Target-existence and
// projection semantics are covered in memory_git_remediation_test.go and
// store_memory_context_test.go.

// setupValidationProject returns a store with a project, legacy root, and an
// agent branch to commit validation probes against.
func setupValidationProject(t *testing.T, project string) (*Store, *models.MemoryChangeset, string, func()) {
	t.Helper()
	s, cleanup := newTestStore(t)
	setupAgentProject(t, s, "agent-val", project)
	root, _, err := s.EnsureLegacyRoot(context.Background(), project, "system")
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	ref := "refs/agents/val/main"
	if _, err := s.CreateMemoryBranch(context.Background(), project, ref, root.ChangesetID, "val", false); err != nil {
		cleanup()
		t.Fatal(err)
	}
	return s, root, ref, cleanup
}

// validationProbe submits a single-op changeset and returns the creation error.
// Accepted changesets become the parent of the next probe, so a probe can
// target memory introduced by an earlier accepted probe.
type validationProbe struct {
	t       *testing.T
	s       *Store
	project string
	ref     string
	parent  string
	seq     int
}

func (p *validationProbe) create(op models.MemorySemanticOp) error {
	p.seq++
	cs, err := p.s.CreateChangeset(context.Background(), CreateChangesetRequest{
		ProjectID: p.project, RefName: p.ref, ParentIDs: []string{p.parent},
		AuthorPrincipal: "val", IdempotencyKey: fmt.Sprintf("probe-%d", p.seq),
		Ops: []models.MemorySemanticOp{op},
	})
	if err == nil {
		p.parent = cs.ChangesetID
	}
	return err
}

func (p *validationProbe) mustReject(op models.MemorySemanticOp, want string) {
	p.t.Helper()
	err := p.create(op)
	if !errors.Is(err, ErrInvalidSemanticOp) {
		p.t.Fatalf("%s: error = %v, want ErrInvalidSemanticOp", want, err)
	}
	if !strings.Contains(err.Error(), want) {
		p.t.Fatalf("error %q does not name %q", err.Error(), want)
	}
}

func (p *validationProbe) mustAccept(op models.MemorySemanticOp, what string) {
	p.t.Helper()
	if err := p.create(op); err != nil {
		p.t.Fatalf("%s: valid op rejected: %v", what, err)
	}
}

func TestSemanticValidationSizeCaps(t *testing.T) {
	s, root, ref, cleanup := setupValidationProject(t, "proj-val-size")
	defer cleanup()
	p := &validationProbe{s: s, project: "proj-val-size", ref: ref, parent: root.ChangesetID}
	p.t = t

	// Free-text content is capped; the boundary value itself is accepted.
	p.mustAccept(models.MemorySemanticOp{OpType: models.OpAddMemory, Payload: map[string]any{
		"content": strings.Repeat("c", maxContentBytes),
	}}, "content at cap")
	p.mustReject(models.MemorySemanticOp{OpType: models.OpAddMemory, Payload: map[string]any{
		"content": strings.Repeat("c", maxContentBytes+1),
	}}, "content")

	// Summary and reason fields use the smaller narrative cap.
	p.mustReject(models.MemorySemanticOp{OpType: models.OpAttachVerification, Payload: map[string]any{
		"summary": strings.Repeat("s", maxSummaryBytes+1), "reviewer": "r",
	}}, "summary")
	p.mustReject(models.MemorySemanticOp{OpType: models.OpProposeDeprecation, TargetEventID: "mem-x", Payload: map[string]any{
		"reason": strings.Repeat("r", maxSummaryBytes+1),
	}}, "reason")

	// Identifier-class fields and op-level ID channels use the field cap.
	p.mustReject(models.MemorySemanticOp{OpType: models.OpAddMemory, Payload: map[string]any{
		"content": "x", "scope": strings.Repeat("s", maxFieldBytes+1),
	}}, "scope")
	p.mustReject(models.MemorySemanticOp{OpType: models.OpAddMemory,
		TargetEventID: strings.Repeat("t", maxIDBytes+1),
		Payload:       map[string]any{"content": "x"},
	}, "target_event_id")
	p.mustReject(models.MemorySemanticOp{OpType: models.OpAddMemory,
		ResultingEventID: strings.Repeat("r", maxIDBytes+1),
		Payload:          map[string]any{"content": "x"},
	}, "resulting_event_id")

	// Tags are capped in count and per entry; boundary values are accepted.
	manyTags := make([]any, 0, maxTags)
	for i := 0; i < maxTags; i++ {
		manyTags = append(manyTags, fmt.Sprintf("tag-%d", i))
	}
	p.mustAccept(models.MemorySemanticOp{OpType: models.OpAddMemory, Payload: map[string]any{
		"content": "x", "tags": manyTags,
	}}, "tags at cap")
	p.mustReject(models.MemorySemanticOp{OpType: models.OpAddMemory, Payload: map[string]any{
		"content": "x", "tags": append(manyTags, "one-too-many"),
	}}, "tags")
	p.mustAccept(models.MemorySemanticOp{OpType: models.OpAddMemory, Payload: map[string]any{
		"content": "x", "tags": []any{strings.Repeat("t", maxTagBytes)},
	}}, "tag at cap")
	p.mustReject(models.MemorySemanticOp{OpType: models.OpAddMemory, Payload: map[string]any{
		"content": "x", "tags": []any{strings.Repeat("t", maxTagBytes+1)},
	}}, "tags")

	// The per-op payload cap binds even when every individual field respects
	// its own cap, e.g. a nested non-string value under an allowed key.
	big := map[string]any{}
	for i := 0; i < 256; i++ {
		big[fmt.Sprintf("k%03d", i)] = strings.Repeat("v", 256)
	}
	p.mustReject(models.MemorySemanticOp{OpType: models.OpAttachEvidence, TargetEventID: "mem-x", Payload: map[string]any{
		"summary": "s", "source": big,
	}}, "payload")
}

func TestSemanticValidationUnsupportedPayloadKeys(t *testing.T) {
	s, root, ref, cleanup := setupValidationProject(t, "proj-val-keys")
	defer cleanup()
	p := &validationProbe{s: s, project: "proj-val-keys", ref: ref, parent: root.ChangesetID}
	p.t = t

	cases := map[string]models.MemorySemanticOp{
		"add_memory":          {OpType: models.OpAddMemory, Payload: map[string]any{"content": "x", "exec": "rm -rf"}},
		"correct_memory":      {OpType: models.OpCorrectMemory, TargetEventID: "mem-a", Payload: map[string]any{"content": "x", "duplicate_of": "mem-b"}},
		"supersede_memory":    {OpType: models.OpSupersedeMemory, TargetEventID: "mem-a", Payload: map[string]any{"content": "x", "reason": "why"}},
		"mark_duplicate":      {OpType: models.OpMarkDuplicate, TargetEventID: "mem-a", Payload: map[string]any{"duplicate_of": "mem-b", "content": "x"}},
		"add_relationship":    {OpType: models.OpAddRelationship, TargetEventID: "mem-a", ResultingEventID: "mem-b", Payload: map[string]any{"kind": "supports", "reason": "why"}},
		"propose_deprecation": {OpType: models.OpProposeDeprecation, TargetEventID: "mem-a", Payload: map[string]any{"reason": "old", "duplicate_id": "mem-b"}},
		"attach_evidence":     {OpType: models.OpAttachEvidence, TargetEventID: "mem-a", Payload: map[string]any{"summary": "s", "confidence": 0.9}},
		"attach_verification": {OpType: models.OpAttachVerification, Payload: map[string]any{"summary": "s", "reviewer": "r", "memory_id": "mem-a"}},
	}
	for name, op := range cases {
		err := p.create(op)
		if !errors.Is(err, ErrInvalidSemanticOp) {
			t.Fatalf("%s: error = %v, want ErrInvalidSemanticOp", name, err)
		}
		if !strings.Contains(err.Error(), "not supported") {
			t.Fatalf("%s: error %q does not name the offending key", name, err.Error())
		}
	}
	// The exact offending key must appear in the message.
	p.mustReject(models.MemorySemanticOp{OpType: models.OpAddMemory, Payload: map[string]any{
		"content": "x", "drop_table": true,
	}}, "drop_table")
}

func TestSemanticValidationAmbiguousIdentifiers(t *testing.T) {
	s, root, ref, cleanup := setupValidationProject(t, "proj-val-amb")
	defer cleanup()
	p := &validationProbe{s: s, project: "proj-val-amb", ref: ref, parent: root.ChangesetID}
	p.t = t

	// mark_duplicate: each role's two channels must agree when both are set.
	p.mustReject(models.MemorySemanticOp{OpType: models.OpMarkDuplicate,
		TargetEventID: "mem-a", Payload: map[string]any{"duplicate_id": "mem-b", "duplicate_of": "mem-c"}}, "duplicate_id")
	p.mustReject(models.MemorySemanticOp{OpType: models.OpMarkDuplicate,
		TargetEventID: "mem-a", ResultingEventID: "mem-b", Payload: map[string]any{"duplicate_of": "mem-c"}}, "duplicate_of")

	// add_relationship: from/to channels must agree.
	p.mustReject(models.MemorySemanticOp{OpType: models.OpAddRelationship,
		TargetEventID: "mem-a", Payload: map[string]any{"from_memory_id": "mem-b", "to_memory_id": "mem-c", "kind": "supports"}}, "from_memory_id")
	p.mustReject(models.MemorySemanticOp{OpType: models.OpAddRelationship,
		TargetEventID: "mem-a", ResultingEventID: "mem-b", Payload: map[string]any{"to_memory_id": "mem-c", "kind": "supports"}}, "to_memory_id")

	// add_memory: the three resulting-identity channels must agree pairwise.
	p.mustReject(models.MemorySemanticOp{OpType: models.OpAddMemory,
		ResultingEventID: "mem-a", Payload: map[string]any{"content": "x", "memory_id": "mem-b"}}, "ambiguous")
	p.mustReject(models.MemorySemanticOp{OpType: models.OpAddMemory,
		ResultingEventID: "mem-a", Payload: map[string]any{"content": "x", "event_id": "mem-b"}}, "ambiguous")
	p.mustReject(models.MemorySemanticOp{OpType: models.OpAddMemory,
		Payload: map[string]any{"content": "x", "memory_id": "mem-a", "event_id": "mem-b"}}, "ambiguous")

	// supersede_memory: replacement identity channels must agree.
	p.mustReject(models.MemorySemanticOp{OpType: models.OpSupersedeMemory,
		TargetEventID: "mem-t", ResultingEventID: "mem-a", Payload: map[string]any{"content": "x", "memory_id": "mem-b"}}, "ambiguous")

	// Agreement across channels is normalization, not ambiguity.
	for _, id := range []string{"mem-x1", "mem-x2"} {
		p.mustAccept(models.MemorySemanticOp{OpType: models.OpAddMemory, ResultingEventID: id,
			Payload: map[string]any{"content": "seed " + id}}, "seed "+id)
	}
	p.mustAccept(models.MemorySemanticOp{OpType: models.OpAddMemory,
		ResultingEventID: "mem-same", Payload: map[string]any{"content": "x", "memory_id": "mem-same", "event_id": "mem-same"}}, "add_memory agreeing ids")
	p.mustAccept(models.MemorySemanticOp{OpType: models.OpAddRelationship,
		TargetEventID: "mem-x1", ResultingEventID: "mem-x2", Payload: map[string]any{"from_memory_id": "mem-x1", "to_memory_id": "mem-x2", "kind": "supports"}}, "add_relationship agreeing ids")
	p.mustAccept(models.MemorySemanticOp{OpType: models.OpMarkDuplicate,
		TargetEventID: "mem-x2", ResultingEventID: "mem-x1", Payload: map[string]any{"duplicate_id": "mem-x2", "duplicate_of": "mem-x1"}}, "mark_duplicate agreeing ids")
	p.mustAccept(models.MemorySemanticOp{OpType: models.OpSupersedeMemory,
		TargetEventID: "mem-same", ResultingEventID: "mem-next", Payload: map[string]any{"content": "y", "memory_id": "mem-next"}}, "supersede agreeing ids")
}

// TestSemanticValidationLegitimateKeys sweeps every allowed payload key per
// op type so the contract cannot accidentally reject a documented field.
func TestSemanticValidationLegitimateKeys(t *testing.T) {
	s, root, ref, cleanup := setupValidationProject(t, "proj-val-legit")
	defer cleanup()
	p := &validationProbe{s: s, project: "proj-val-legit", ref: ref, parent: root.ChangesetID}
	p.t = t

	// Seed two memories to target.
	for _, id := range []string{"mem-a", "mem-b"} {
		p.mustAccept(models.MemorySemanticOp{OpType: models.OpAddMemory, ResultingEventID: id,
			Payload: map[string]any{"content": "seed " + id}}, "seed "+id)
	}

	provenance := map[string]any{
		"reviewer": "r", "proposal_id": "p-1", "source_changeset_id": "cs-1",
		"rejected_from": "cs-2", "cherrypicked_from": "cs-3",
		"left_branch": "refs/agents/a/main", "right_branch": "refs/agents/b/main",
	}
	common := map[string]any{
		"summary": "s", "project_id": "proj-val-legit", "topic_id": "t-1", "actor_id": "a-1",
		"from_visibility": "private", "to_visibility": "shared",
	}
	content := map[string]any{
		"kind": "fact", "event_type": "record", "scope": "s-1", "visibility": "shared",
		"tags": []any{"x"}, "confidence": 0.5, "fact_key": "k", "subject": "sub",
		"valid_from": "2026-07-01T00:00:00Z", "valid_to": "2026-07-31T00:00:00Z",
		"protected": true, "approval": "user_approved",
	}
	merge := func(groups ...map[string]any) map[string]any {
		out := map[string]any{}
		for _, group := range groups {
			for k, v := range group {
				out[k] = v
			}
		}
		return out
	}

	p.mustAccept(models.MemorySemanticOp{OpType: models.OpAddMemory, ResultingEventID: "mem-full",
		Payload: merge(common, provenance, content, map[string]any{
			"content": "full add", "memory_id": "mem-full", "event_id": "mem-full",
		})}, "add_memory full key set")
	p.mustAccept(models.MemorySemanticOp{OpType: models.OpCorrectMemory, TargetEventID: "mem-a",
		Payload: merge(common, provenance, content, map[string]any{
			"content": "full correct", "target_trust": "user_approved", "source_trust": "user_approved",
		})}, "correct_memory full key set")
	p.mustAccept(models.MemorySemanticOp{OpType: models.OpSupersedeMemory, TargetEventID: "mem-b", ResultingEventID: "mem-sup",
		Payload: merge(common, provenance, content, map[string]any{
			"content": "full supersede", "memory_id": "mem-sup", "event_id": "mem-sup",
			"target_trust": "model_inference", "source_trust": "user_approved",
		})}, "supersede_memory full key set")
	p.mustAccept(models.MemorySemanticOp{OpType: models.OpMarkDuplicate, TargetEventID: "mem-sup", ResultingEventID: "mem-a",
		Payload: merge(common, provenance, map[string]any{"duplicate_id": "mem-sup", "duplicate_of": "mem-a"})}, "mark_duplicate full key set")
	p.mustAccept(models.MemorySemanticOp{OpType: models.OpAddRelationship, TargetEventID: "mem-a", ResultingEventID: "mem-full",
		Payload: merge(common, provenance, map[string]any{"kind": "supports", "from_memory_id": "mem-a", "to_memory_id": "mem-full"})}, "add_relationship full key set")
	p.mustAccept(models.MemorySemanticOp{OpType: models.OpProposeDeprecation, TargetEventID: "mem-full",
		Payload: merge(common, provenance, map[string]any{"reason": "stale"})}, "propose_deprecation full key set")
	p.mustAccept(models.MemorySemanticOp{OpType: models.OpAttachEvidence, TargetEventID: "mem-a",
		Payload: merge(common, provenance, map[string]any{"kind": "ci", "source": "pipeline", "note": "n", "status": "ok"})}, "attach_evidence full key set")
	// Target-less attestation carrying every provenance marker.
	p.mustAccept(models.MemorySemanticOp{OpType: models.OpAttachVerification,
		Payload: merge(common, provenance, map[string]any{"kind": "reviewed_merge", "note": "n", "status": "accepted"})}, "attach_verification attestation full key set")
}
