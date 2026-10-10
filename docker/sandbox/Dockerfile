# The image a container sandbox runs when sandbox.container_image is unset.
# The release workflow builds it, pushes it and links its digest into the
# binary, so a shhh always starts the image built beside it.
# See docs/capabilities/containment.md#the-sandbox-image-ships-with-the-binary.

# Wolfi is glibc, rebuilt continuously and much smaller than a Debian base, and
# it packages Go, Node and Python as versioned streams, so the toolchains come
# from the base's own index rather than being copied out of other images. Its
# free tier publishes only the latest tag; the release pins the built image by
# digest, which is what makes a given shhh's sandbox reproducible.
FROM cgr.dev/chainguard/wolfi-base:latest

# Links the published package to this repository, which is what keeps the
# release workflow's token able to push new versions to it.
LABEL org.opencontainers.image.source="https://github.com/rfizzle/shhh"

# A sandbox has no network by default, so nothing can be installed once it is
# running: whatever a session may need to build and test is here already.
# Compilers and a project's own linters and scanners are left out on
# purpose: a checkout declares the tools its work needs in
# .shhh/toolchain.toml, and a project that wants a different base altogether
# names its own image in sandbox.container_image.
# See docs/capabilities/containment.md#a-checkout-declares-the-toolchain-its-work-needs.
RUN apk add --no-cache \
        bash ca-certificates-bundle curl fd git go-1.26 jq make \
        nodejs-24 npm python-3.13 ripgrep

# shhh itself, as the helper every program a sandbox session starts here runs
# under: an exec forwards no signal, and the helper is what stops a command
# when the session's stream to it hangs up. It is out of every PATH, and the
# label says which helper protocol it speaks, so `shhh doctor` can read it
# without starting a container. The release workflow builds the binary for
# each platform into bin/ before it builds this image.
# See docs/capabilities/containment.md#a-session-can-run-in-the-sandbox.
ARG TARGETARCH
COPY bin/linux-${TARGETARCH}/shhh /usr/local/libexec/shhh/shhh
LABEL dev.shhh.sandbox-exec="1"
