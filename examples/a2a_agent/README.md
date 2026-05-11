# A2A Agent

`coding_agent.py` is a sample [A2A-protocol](https://github.com/a2aproject/A2A)
agent built on Google ADK's `LlmAgent` (Gemini). It demonstrates how to
register and use any A2A-compliant agent from AX.

The agent writes Python code on request, asks the user to confirm a save
path before writing anything, and returns the saved source as a file
attachment.

## What it demonstrates

- **The full A2A integration surface AX supports**: AgentCard discovery,
  multi-transport (gRPC + JSON-RPC + HTTP+JSON REST), streaming and
  polling-fallback, FilePart artifacts.
- **HITL via A2A's `TASK_STATE_INPUT_REQUIRED`** mapped to AX's
  `ConfirmationContent`. The agent proposes a path; AX prompts the
  user; the user's "yes" reply resumes the same A2A task.
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

## Register the agent in AX

Add an entry under `registry.remote_agents` in your `ax.yaml`:

```yaml
registry:
  remote_agents:
    - id: "coding-agent"
      name: "Coding Agent"
      description: "Writes Python code and saves it to disk after the user confirms a path."
      address: "http://127.0.0.1:41241"
      protocol: "a2a"
```

To use the auth flow, also set the credential and add an `auth` block:

```yaml
registry:
  remote_agents:
    - id: "coding-agent"
      name: "Coding Agent"
      description: "Writes Python code and saves it to disk after the user confirms a path."
      address: "http://127.0.0.1:41241"
      protocol: "a2a"
      auth:
        type: "bearer"      # or "api_key"
        credential_env: "CODING_AGENT_AUTH_TOKEN"
```

Make sure both AX's process and the agent process see the same value
for `CODING_AGENT_AUTH_TOKEN`.

## Try it

Start the agent server in one terminal, then in another:

```bash
ax exec --input "Write me a Python flask hello-world server."
```

The planner delegates to `coding-agent`, which:

1. Generates the code
2. Proposes a save path (e.g. `~/flask_app.py`) and pauses for confirmation
3. AX prompts you `Confirm? [yes/no]`
4. On `yes`, the agent writes the file and returns the source as a
   `text/x-python` FilePart alongside its text reply
5. On `no`, the agent emits the proposed code as a fenced markdown block
   instead and skips the write
