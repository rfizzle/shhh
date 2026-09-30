# The image a container sandbox runs when sandbox.container_image is unset.
# The release workflow builds it, pushes it and links its digest into the
# binary, so a shhh always starts the image built beside it.
# See docs/capabilities/containment.md#the-sandbox-image-ships-with-the-binary.
ARG GO_VERSION=1.26
ARG NODE_VERSION=24

FROM golang:${GO_VERSION}-trixie AS go
# Documentation, examples and the toolchain's own tests are not what a
# sandboxed build runs.
RUN rm -rf /usr/local/go/doc /usr/local/go/test /usr/local/go/misc

FROM node:${NODE_VERSION}-trixie-slim AS node

FROM debian:trixie-slim

# A sandbox has no network by default, so nothing can be installed once it is
# running: whatever a session may need to build and test is here already.
# Compilers are left out on purpose; a project that needs one names its own
# image in sandbox.container_image.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash ca-certificates curl fd-find git jq make python3 ripgrep \
    && ln -s "$(command -v fdfind)" /usr/local/bin/fd \
    && rm -rf /var/lib/apt/lists/*

COPY --from=go /usr/local/go /usr/local/go
COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
    && ln -s ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx
ENV PATH=/usr/local/go/bin:$PATH
