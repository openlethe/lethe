-- Migration 012: Single-use protected-merge authorization.
--
-- A protected ref can move only against a Charon-signed, expiring, single-use
-- authorization envelope. The nonce is consumed atomically with the ref CAS in
-- one transaction, so a captured authorization can never be replayed — even if
-- the ref later returns to the same expected head. Every protected-ref
-- movement also writes a durable advancement record for reconciliation.

PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS memory_merge_authorizations (
    nonce                TEXT PRIMARY KEY,
    key_id               TEXT NOT NULL DEFAULT '',
    project_id           TEXT NOT NULL REFERENCES projects(project_id),
    ref_name             TEXT NOT NULL,
    expected_head        TEXT NOT NULL,
    new_head             TEXT NOT NULL,
    merge_proposal_id    TEXT NOT NULL,
    proposal_digest      TEXT NOT NULL,
    reviewer_principal   TEXT NOT NULL,
    merger_principal     TEXT NOT NULL,
    strategy             TEXT NOT NULL,
    expires_at           DATETIME NOT NULL,
    consumed_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_memory_merge_authorizations_proposal
    ON memory_merge_authorizations(merge_proposal_id);

CREATE TABLE IF NOT EXISTS memory_protected_ref_advances (
    advance_id           TEXT PRIMARY KEY,
    project_id           TEXT NOT NULL REFERENCES projects(project_id),
    ref_name             TEXT NOT NULL,
    expected_head        TEXT NOT NULL,
    new_head             TEXT NOT NULL,
    merge_proposal_id    TEXT NOT NULL,
    proposal_digest      TEXT NOT NULL,
    reviewer_principal   TEXT NOT NULL,
    merger_principal     TEXT NOT NULL,
    strategy             TEXT NOT NULL,
    authorization_nonce  TEXT NOT NULL REFERENCES memory_merge_authorizations(nonce),
    created_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_memory_protected_ref_advances_ref
    ON memory_protected_ref_advances(project_id, ref_name, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_memory_protected_ref_advances_proposal
    ON memory_protected_ref_advances(merge_proposal_id);
