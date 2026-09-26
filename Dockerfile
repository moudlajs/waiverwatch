# syntax=docker/dockerfile:1

FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The release tag, stamped in because the build context has no .git.
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.buildVersion=${VERSION}" -o /waiverwatch . && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /killswitch ./cmd/killswitch

# Static binary, CA certificates, no shell, non-root.
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /waiverwatch /waiverwatch
# The billing kill switch (#51) ships in the same image; its Cloud Run
# service runs it with --command /killswitch.
COPY --from=build /killswitch /killswitch
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["/waiverwatch"]
