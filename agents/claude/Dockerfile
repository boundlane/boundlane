FROM node:22-bookworm-slim

ARG CLAUDE_CODE_VERSION=2.1.288

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl git python3 python3-pip \
 && rm -rf /var/lib/apt/lists/*

RUN npm install -g "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
 && npm config set audit false --location=global

# The policy sets run_as_user: sandbox, so the image has that account.
RUN useradd --create-home --uid 1001 --shell /bin/bash sandbox
USER sandbox
WORKDIR /sandbox
