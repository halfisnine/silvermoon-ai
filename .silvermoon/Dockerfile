# Silvermoon packaging of New API. Upstream Dockerfile and attribution are retained.
FROM oven/bun:1.4.4@sha256:1d22548f177f4ff38e0c15baa3c2c46c6c2060ba2d1bc573bb4720b407548f1d AS frontend
WORKDIR /src/web
ENV BUN_CONFIG_MAX_HTTP_REQUESTS=16 NODE_OPTIONS=--max-old-space-size=2304 RAYON_NUM_THREADS=2
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ ./
COPY pkg/billingexpr/testdata/frontend_simulation.json /src/pkg/billingexpr/testdata/frontend_simulation.json
ARG CUSTOM_VERSION
RUN test -n "$CUSTOM_VERSION" && bun run typecheck \
    && bun run test --maxWorkers=1 src/features/home/__tests__/silvermoon-home.test.tsx src/lib/__tests__/status-query.test.tsx \
    && VITE_REACT_APP_VERSION="$CUSTOM_VERSION" bun run build \
    && test -s dist/index.html && printf '%s\n' "$CUSTOM_VERSION" > /src/frontend-version

FROM golang:1.26.10-bookworm@sha256:5de7c99eb78ca5e515a4c340235d0955ec5964a5071c492c15503d7c267da473 AS backend
WORKDIR /src
ENV CGO_ENABLED=0 GOWORK=off GOTOOLCHAIN=local GOEXPERIMENT=greenteagc \
    GOMAXPROCS=2 GOMEMLIMIT=2GiB GOFLAGS="-p=2 -mod=readonly"
COPY . .
RUN go mod download && go mod verify
COPY --from=frontend /src/web/dist ./web/dist
RUN go vet ./... \
    && cd relaykit && go mod download && go mod verify && go build ./... && go test ./... \
    && cd ../tokenkit && go mod download && go mod verify && go build ./... && go test ./...
ARG CUSTOM_VERSION
RUN test -n "$CUSTOM_VERSION" \
    && go build -trimpath -pgo="$(go list -m -f '{{.Dir}}' github.com/Calcium-Ion/moejs)/default.pgo" \
       -ldflags="-s -w -X github.com/QuantumNous/new-api/common.Version=$CUSTOM_VERSION" \
       -o /out/new-api . \
    && test "$(/out/new-api --version)" = "$CUSTOM_VERSION"

FROM debian:bookworm-slim@sha256:f06537653ac770703bc45b4b113475bd402f451e85223f0f2837acbf89ab020a AS runtime
# Match upstream runtime packages; do not copy developer configuration or data.
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata libasan8 wget \
    && rm -rf /var/lib/apt/lists/* && update-ca-certificates
COPY --from=backend /out/new-api /new-api
COPY --from=frontend /src/frontend-version /usr/share/silvermoon/frontend-version
COPY LICENSE NOTICE THIRD-PARTY-LICENSES.md /licenses/
WORKDIR /data
EXPOSE 3000
ENTRYPOINT ["/new-api"]
