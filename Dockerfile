FROM docker.io/library/golang:1.27.2-alpine3.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bot . \
    && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot@sha256:52dcfbabb7457ea47c82f6e13af8c8a4a1d9f7b0145142b3ecab20f2b888411d
COPY --from=build /out/bot /bot
# Pre-create the data directory owned by the non-root user so a fresh named
# volume mounted here is writable.
COPY --from=build --chown=nonroot:nonroot /out/data /data
ENV DB_PATH=/data/bot.db
VOLUME /data
USER nonroot:nonroot
HEALTHCHECK --interval=60s --timeout=5s --start-period=30s --retries=3 \
    CMD ["/bot", "healthcheck"]
ENTRYPOINT ["/bot"]
