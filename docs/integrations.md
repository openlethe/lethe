# Integrations

Lethe works with any agent that can call HTTP or MCP. These are the
supported patterns; none of them require OpenClaw.

## ChatGPT (remote, via Charon OAuth)

ChatGPT connects to Charon's primary gateway over HTTPS and completes an
S256 PKCE flow gated by an operator-held pairing key.

1. Deploy the governed stack ([docker-compose.md](docker-compose.md#variation-d--governed-stack-lethe-git--charon)).
2. Tunnel `127.0.0.1:18484` to a public HTTPS URL; set `CHARON_PUBLIC_URL`
   to it and allow ChatGPT in `CHARON_OAUTH_REDIRECT_URIS`.
3. Restart Charon and copy the current pairing key from the startup banner
   (`docker compose logs charon`) — it regenerates on every restart.
4. Add the connector in ChatGPT and paste the pairing key when prompted.

Tokens are capped to `memory.read · search · thread.read · propose` by
default; review/merge never come over OAuth.

## Claude Code / Claude (MCP)

Any streamable-HTTP MCP client can connect to Charon's Obol gateway:

```json
{
  "mcpServers": {
    "charon": {
      "url": "http://127.0.0.1:18486/mcp",
      "transport": "streamable-http",
      "headers": { "Authorization": "Bearer obol_…" }
    }
  }
}
```

Mint the Obol through the Obol listener (tokens are audience-bound):

```bash
docker compose exec charon-reviewer charon obol mint --expires 7d <principal-id>
```

## Cursor / VS Code agents

Same pattern as Claude: register the MCP server with the streamable-HTTP
URL and the Obol bearer in the editor's MCP settings. The agent gets the 13
Memory Git tools with its principal's scopes.

## Generic MCP client (incl. OpenClaw as a client)

```bash
TOKEN=$(grep -oE 'Token: obol_[A-Za-z0-9_-]+' secrets/author-obol.txt | sed 's/Token: //')
openclaw mcp add charon-author \
  --url http://127.0.0.1:18486/mcp \
  --transport streamable-http \
  --header "Authorization=Bearer ${TOKEN}"
```

Run the author and the reviewer as **separate** agents with separate
credentials — review independence is the point of the system.

## Direct HTTP (backend services)

No MCP client? The typed API is enough:

```bash
curl -H "Authorization: Bearer $LETHE_API_KEY" \
  http://127.0.0.1:18485/api/memory/<project>/refs
```

See [api.md](api.md) for the full route table.

## Multi-agent, one memory backend

The pattern that makes Lethe more than a notepad:

1. One Lethe Git + one Charon deployment.
2. A **propose** principal per writing agent (exact project grant;
   `memory.branch · commit · propose`).
3. One **review** principal (separate agent or human;
   `memory.review · merge`).
4. Every writer works on its own ref (`refs/agents/<name>/main` or topic
   refs); accepted memory lands on `refs/shared/main` only through
   independent review and the signed merge envelope.
5. Readers (including OAuth-capped ChatGPT) consume accepted memory with
   `memory_context_at`.

The full end-to-end run — bootstrap, credentials, propose, review, merge,
rotation, backup — is in
[Charon's full-run guide](https://github.com/openlethe/charon/blob/main/docs/full-run.md).
