ARG BUILD_FROM=ghcr.io/home-assistant/amd64-base:3.20

FROM golang:alpine AS build

WORKDIR /src
ENV CGO_ENABLED=0

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETARCH
ARG TARGETVARIANT

RUN GOOS=linux \
    GOARCH=${TARGETARCH:-amd64} \
    GOARM=${TARGETVARIANT#v} \
    go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/dash ./cmd/dash

FROM ${BUILD_FROM}

COPY --from=build /out/dash /usr/bin/dash
RUN chmod +x /usr/bin/dash

# The HA base image uses s6-overlay as ENTRYPOINT and filters the container
# environment for legacy services, so SUPERVISOR_TOKEN would be empty. A plain
# static binary must inherit the Docker environment directly.
ENTRYPOINT []
CMD [ "/usr/bin/dash" ]
