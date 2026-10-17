# syntax=docker/dockerfile:1.7
# Static, nonroot image for every role: `docker run IMAGE api|storefront|worker|migrate up`.
# Build from the repository root:
#   docker buildx build -f deploy/docker/Dockerfile --platform linux/amd64,linux/arm64 .

FROM --platform=$BUILDPLATFORM golang:1.27.2-alpine3.24@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
      -o /out/platform ./cmd/platform

# distroless/static: no shell, no package manager; uid/gid 65532.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/platform /platform
USER 65532:65532
EXPOSE 8080 8081 8082
ENTRYPOINT ["/platform"]
CMD ["api"]
