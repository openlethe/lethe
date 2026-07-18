# FAQ

**Is Lethe an OpenClaw component?**
No. Lethe is a general-purpose memory platform for any AI agent. The
OpenClaw plugin is one integration — recommended for OpenClaw users, never
required. ChatGPT, Claude, Claude Code, Cursor, IDE assistants, custom MCP
clients, and backend services all work directly.

**Is Memory Git actual Git?**
No. It borrows Git's *semantics* — refs, changesets, branching, merging,
immutable parent-linked history — and applies them to memory records stored
in SQLite. There are no `.git` directories and no filesystem repositories.

**Which mode should I run?**
OpenClaw → legacy. Durable or shared memory for anything else → git (add
Charon when memory is shared or leaves loopback). Both or migrating →
hybrid. See [runtime-modes.md](runtime-modes.md).

**Do I need Charon?**
Not for a single local agent — the bearer-key API is enough. You want
Charon when several agents share memory, when memory is reviewed before
acceptance, or when an agent connects over a tunnel (OAuth + scoped
principals + audit).

**Do I need a database server?**
No. Lethe embeds SQLite (WAL, `synchronous=FULL`) in one non-root container.
The only operational concern is a bind-mounted data directory owned by UID
1000.

**Where does my memory live?**
In `lethe.db` inside your bind-mounted data directory (`./lethe-data` or
`./lethe-git-data` by default). Backups are `sqlite3 .backup` copies of that
file. Nothing leaves your infrastructure unless you tunnel it yourself.

**How are credentials handled?**
You generate them (`openssl rand -hex 32`, `./lethe keygen`, or
`scripts/prepare-local-memory-git-env.sh`). They live in mode-0600 `.env`
files, are never committed, never stored in the database, and are distinct
per purpose. Details: [configuration.md](configuration.md).

**What happens on restart?**
Everything persists: sessions, events, changesets, refs, manifests, and
consumed merge nonces (so replay protection survives restarts). The one
credential that changes by design is Charon's browser **pairing key** —
regenerated at every Charon start and printed in the startup banner.

**A merge or changeset returned 409. Is that corruption?**
No — it is the compare-and-swap working as intended. Reread the current
head, reconcile against it, and resubmit with a **new** idempotency key.
A replayed merge authorization is also a hard rejection, even if the ref
cycled back to the same head.

**The gateway says "`CHARON_OAUTH_PAIRING_SECRET` is required". Do I need to set it?**
Usually no — that error means the OAuth block never reached the container,
not that a secret is missing. Pass
`CHARON_OAUTH_GENERATE_PAIRING_SECRET=true` (the released compose does) and
every Charon start prints a fresh browser authorization key in the logs
(`docker compose logs charon`). You only set a static
`CHARON_OAUTH_PAIRING_SECRET` (32+ characters) when you deliberately
disable generation for one stable key.

**Can multiple agents share one memory?**
Yes — that is the designed center of the system: one Lethe Git, one Charon,
a propose-profile principal per writing agent, an independent reviewer
principal, and accepted memory landing on `refs/shared/main` only through
review. See [integrations.md](integrations.md#multi-agent-one-memory-backend).

**Is there a hosted/SaaS version?**
Lethe is self-hosted by design. The container and the source are the
product.
