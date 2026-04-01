# Lethe — Persistent Memory Layer

## Purpose

Lethe is a persistent memory layer for AI agents. It records session events, checkpoints, and summaries to a Lethe server, enabling continuity across sessions.

## Usage

The Lethe plugin activates automatically as a context engine. It intercepts session events and sends them to the configured Lethe endpoint.

### Configuration

```json
{
  "endpoint": "http://localhost:18483",
  "apiKey": "your-api-key",
  "agentId": "archimedes",
  "projectId": "default"
}
```

### What Gets Recorded

- **Messages** — conversation turns sent to the agent
- **Tool calls** — tools used and their outputs
- **Checkpoints** — periodic session summaries
- **Heartbeats** — periodic alive signals
- **Threads** — detected conversation threads

### Key Endpoints

| Method | Path | Description |
|--------|------|-------------|
| POST | /sessions | Create a new session |
| POST | /events | Log session events |
| GET | /sessions/:key/summary | Retrieve session summary |
| POST | /checkpoints | Save a checkpoint |
| POST | /heartbeat | Send a heartbeat |

## Notes

- Events are batched and sent at appropriate checkpoints during the session.
- The plugin uses in-memory buffering to avoid sending every individual event.
- If the Lethe endpoint is unreachable, the plugin logs a warning but continues without crashing.
- Session summaries include conversation direction, key topics, and open threads.
