# Both demo apps in one image; the command picks one. Build from the
# repository root so the web module's replace directive finds the SDK:
#   docker build -f examples/web/Dockerfile .
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY examples/web/go.mod examples/web/go.sum examples/web/
RUN cd examples/web && go mod download
COPY . .
RUN cd examples/web && CGO_ENABLED=0 go build -trimpath -o /out/wc-dapp ./dapp \
 && CGO_ENABLED=0 go build -trimpath -o /out/wc-wallet ./wallet

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ /app/
