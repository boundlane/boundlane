FROM node:22-bookworm-slim

ARG OPENCODE_VERSION=1.18.34

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl git python3 python3-pip \
 && rm -rf /var/lib/apt/lists/*

RUN npm install -g "opencode-ai@${OPENCODE_VERSION}" \
 && npm config set audit false --location=global

RUN useradd --create-home --uid 1001 --shell /bin/bash sandbox

# OpenCode installs @opencode-ai/plugin into its config directory on startup
# unless that directory already has a node_modules and a lockfile naming the
# package (packages/core/src/npm.ts, v1.18.34). The install requests
# registry.npmjs.org, and the package path is refused. These files make the
# check succeed without a download. HOME in the sandbox is the workdir; the
# account home is covered too.
RUN for dir in /sandbox/.config/opencode /home/sandbox/.config/opencode; do \
      mkdir -p "$dir/node_modules" \
      && printf '%s\n' '{"dependencies":{"@opencode-ai/plugin":"1.18.34"}}' > "$dir/package.json" \
      && printf '%s\n' '{"packages":{"":{"dependencies":{"@opencode-ai/plugin":"1.18.34"}}}}' > "$dir/package-lock.json"; \
    done \
 && chown -R sandbox:sandbox /sandbox/.config /home/sandbox/.config

USER sandbox
WORKDIR /sandbox
