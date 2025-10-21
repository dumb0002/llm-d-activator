# Dockerfile has specific requirement to put this ARG at the beginning:
# https://docs.docker.com/engine/reference/builder/#understand-how-arg-and-from-interact
ARG BUILDER_IMAGE=golang:1.24
ARG BASE_IMAGE=gcr.io/distroless/static:nonroot

## Multistage build
FROM ${BUILDER_IMAGE} AS builder
ARG COMMIT_SHA=unknown
ARG BUILD_REF
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ENV CGO_ENABLED=0
ENV GOOS=$TARGETOS
ENV GOARCH=$TARGETARCH


# Dependencies
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

# Sources
COPY cmd/activator ./cmd/activator
COPY pkg/activator ./pkg/activator
COPY internal ./internal
COPY api ./api
COPY version ./version
WORKDIR /src/cmd/activator
RUN go build -ldflags="-X github.com/llm-d-incubation/llm-d-activator/version.CommitSHA=${COMMIT_SHA} -X github.com/llm-d-incubation/llm-d-activator/version.BuildRef=${BUILD_REF}" -o /activator

## Multistage deploy
FROM ${BASE_IMAGE}

WORKDIR /
COPY --from=builder /activator /activator

ENTRYPOINT ["/activator"]
