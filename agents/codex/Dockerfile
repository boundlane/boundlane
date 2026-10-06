FROM node:22-bookworm-slim

ARG CODEX_VERSION=0.160.1

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl git python3 python3-pip \
 && rm -rf /var/lib/apt/lists/*

RUN npm install -g "@openai/codex@${CODEX_VERSION}" \
 && npm config set audit false --location=global

RUN useradd --create-home --uid 1001 --shell /bin/bash sandbox
USER sandbox
WORKDIR /sandbox
