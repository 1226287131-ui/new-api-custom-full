FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/media-hk .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/media-hk /media-hk
USER 65532:65532
EXPOSE 8091
ENTRYPOINT ["/media-hk"]
