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
# Compilers are left out on purpose; a project that needs one names its own
# image in sandbox.container_image.
RUN apk add --no-cache \
        bash ca-certificates-bundle curl fd git go-1.26 jq make \
        nodejs-24 npm python-3.13 ripgrep
