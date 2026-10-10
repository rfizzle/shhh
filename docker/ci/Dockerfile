# A replica of the CI job in .github/workflows/test.yml, for `make ci-container`.
# It installs what that job installs: an Ubuntu runner's base, Go at the
# version go.mod names, golangci-lint, goimports, tmux, python3 and
# bubblewrap. The versions below are the only place the Makefile reads them
# from (they name the image tag), and docker/ci/ci_image_test.go fails when
# they disagree with go.mod or the workflow.
#
# Running bubblewrap inside Docker needs a weaker outer boundary, which is
# acceptable for a CI replica and must never be the shape of a sandbox
# session (docs/capabilities/containment.md). `make ci-container` runs this
# image with these options, none of them verified on the host that wrote the
# Makefile, which had no container engine:
#   --security-opt seccomp=unconfined    Docker's default profile refuses the
#                                        unshare and clone calls with the
#                                        user-namespace flag that bwrap makes.
#   --security-opt systempaths=unconfined  Docker masks parts of /proc, and bwrap
#                                        mounts a fresh /proc in its sandbox.
#   --security-opt apparmor=unconfined   On a host with AppArmor, Docker's
#                                        profile denies the mounts bwrap makes.
FROM ubuntu:24.04

ARG GO_VERSION=1.26.2
ARG GOLANGCI_LINT_VERSION=v2.12.2
ARG TARGETARCH

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl git make gcc tmux python3 bubblewrap \
 && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${TARGETARCH:-amd64}.tar.gz" | tar -C /usr/local -xz
ENV GOPATH=/go
ENV GOTOOLCHAIN=local
ENV PATH=/usr/local/go/bin:/go/bin:${PATH}

RUN go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}
RUN go install golang.org/x/tools/cmd/goimports@latest \
 && chmod -R a+rX /go

WORKDIR /src
