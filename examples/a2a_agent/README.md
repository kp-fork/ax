# A2A Agent

`coding_agent.py` is a sample [A2A-protocol](https://github.com/a2aproject/A2A)
agent built on Google ADK's `LlmAgent` (Gemini). It demonstrates how to
register and use any A2A-compliant agent from AX.

The agent writes Python code on request, saves the source directly under
`examples/a2a_agent/output/`, and returns the saved file as a
`text/x-python` attachment alongside its text reply.

## What it demonstrates

- **The full A2A integration surface AX supports**: AgentCard discovery,
  multi-transport (gRPC + JSON-RPC + HTTP+JSON REST), streaming and
  polling-fallback, FilePart artifacts.
- **Inline tool calls inside a single A2A turn**: the agent calls
  `save_python_file(filename, code)`, the file is written immediately,
  and the same task transitions `SUBMITTED → WORKING → COMPLETED` without
  pausing.
- **Optional dual-scheme auth** (`--auth`): the AgentCard advertises
  Bearer and API-key as alternatives; the server enforces either via
  HTTP middleware and a gRPC interceptor.

## Prerequisites

- Python 3.10+ and [`uv`](https://docs.astral.sh/uv/) (the script uses
  PEP-723 inline metadata to declare its dependencies)
- Gemini credentials set in the environment:
  ```bash
  # Either a Gemini API key:
  export GOOGLE_API_KEY="<your-key>"

  # OR Vertex AI (requires gcloud application-default credentials):
  export GOOGLE_GENAI_USE_VERTEXAI=TRUE
  export GOOGLE_CLOUD_PROJECT="<your-project>"
  export GOOGLE_CLOUD_LOCATION="<your-region>"
  ```

## Run the agent server

```bash
# From ax root directory
uv run examples/a2a_agent/coding_agent.py
```

This serves:

- HTTP / JSON-RPC / REST on `127.0.0.1:41241`
- gRPC on `127.0.0.1:50051`
- AgentCard at `http://127.0.0.1:41241/.well-known/agent-card.json`

### Useful flags

| Flag | Purpose |
|---|---|
| `--host`, `--port`, `--grpc-port` | Override default listen addresses. |
| `--no-streaming` | Disable streaming in the AgentCard so clients exercise the polling fallback. |
| `--auth` | Enable auth (advertises both Bearer and API key on the card; accepts either). Requires the env var named by `--auth-token-env`. |
| `--auth-token-env` | Env var that holds the expected credential value. Default: `CODING_AGENT_AUTH_TOKEN`. |
| `--api-key-header` | Header name advertised on the AgentCard's `APIKeySecurityScheme.name`. Default: `X-API-Key`. |
| `--log-level` | `DEBUG`, `INFO`, `WARNING`, `ERROR`. Default: `INFO`. |

### Where saved files land

The agent writes every generated file to `examples/a2a_agent/output/`
(created on first save). Filenames are constrained to bare names ending
in `.py` — no path components, no `..`.

## Register the agent in AX

Add an entry under `registry.remote_agents` in your `ax.yaml`:

```yaml
registry:
  remote_agents:
    - id: "coding-agent"
      name: "Coding Agent"
      description: "Writes Python code and saves it under examples/a2a_agent/output/."
      address: "http://127.0.0.1:41241"
      protocol: "a2a"
```

To use the auth flow, also set the credential and add an `auth` block:

```yaml
registry:
  remote_agents:
    - id: "coding-agent"
      name: "Coding Agent"
      description: "Writes Python code and saves it under examples/a2a_agent/output/."
      address: "http://127.0.0.1:41241"
      protocol: "a2a"
      auth:
        type: "bearer"      # or "api_key"
        credential_env: "CODING_AGENT_AUTH_TOKEN"
```

Make sure both AX's process and the agent process see the same value
for `CODING_AGENT_AUTH_TOKEN`. The api_key header name is auto-discovered
from the AgentCard, so you don't need to configure it on the AX side.

## Try it

Start the agent server in one terminal, then in another:

```bash
ax exec --input "Write me a Python flask hello-world server."
```

The planner delegates to `coding-agent`, which:

1. Generates the code.
2. Calls `save_python_file('flask_hello.py', '<code>')` inline. The file
   is written to `examples/a2a_agent/output/flask_hello.py`.
3. Returns its text reply plus the saved source as a `text/x-python`
   FilePart attached to the same response artifact.
   