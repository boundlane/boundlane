FROM node:22-bookworm-slim

ARG GROK_VERSION=1.0.46

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl git python3 python3-pip \
 && rm -rf /var/lib/apt/lists/*

RUN npm install -g "@xai-official/grok@${GROK_VERSION}" \
 && npm config set audit false --location=global

RUN useradd --create-home --uid 1001 --shell /bin/bash sandbox
USER sandbox
WORKDIR /sandbox
