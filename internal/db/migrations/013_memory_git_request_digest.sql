-- Migration 013: bind changeset idempotency to the complete normalized request.
-- The replay digest covers the ref-mutation control fields (expected_head,
-- advance_ref, create_ref_if_missing, protected) in addition to immutable
-- changeset content, so replaying an idempotency key with flipped control
-- fields fails closed instead of returning a false-success replay.
--
-- Rows that predate this migration retain an empty request_digest. Their
-- historic ref-mutation intent cannot be reconstructed safely, so replaying
-- those keys fails closed and callers must use a new idempotency key.

ALTER TABLE memory_changesets
    ADD COLUMN request_digest TEXT NOT NULL DEFAULT '';
