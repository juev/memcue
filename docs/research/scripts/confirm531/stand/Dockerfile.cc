# Claude Code for the #531 stand. The run directory is mounted at /stand and the fixtures read-only at /opt/fixtures;
# nothing of the host is in the image.
FROM node:22-bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends git python3 ca-certificates \
	&& rm -rf /var/lib/apt/lists/*
ARG CLAUDE_CODE_VERSION
RUN npm install -g "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" && npm cache clean --force
COPY memcue /usr/local/bin/memcue
COPY plugin-base /opt/plugin-base
COPY plugin-cand /opt/plugin-cand
COPY web_mcp.py seed.py agent_cc.sh mcp-cc.json /opt/stand/
ENTRYPOINT ["sh", "/opt/stand/agent_cc.sh"]
