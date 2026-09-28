# Hong Kong media receiver

This standalone service accepts authenticated, checksum-verified uploads from a trusted application node and writes media to local disk. It does not fetch upstream URLs. Public media reads should be served directly by Nginx from the same directory tree.

## Build and run

Create a `.env` next to `compose.yaml` with a randomly generated secret (do not commit it), then run:

```sh
openssl rand -hex 32
docker compose up -d --build
```

The default bind mount is `./data`; it contains `image-cache/` and `video-cache/`. Configure Nginx's filesystem alias to the absolute host path for those directories, using `nginx-media.conf.example`. Keep port 8091 private and do not publish it publicly. The sample Nginx locations serve local files with GET/HEAD, byte ranges, and a real 404 for cache misses. The media hostname is configured in the Nginx server block and must also be supplied to the application as its public media base URL; neither is hard-coded in this service.

## Receiver API

Upload with `PUT /__media_ingest/{image-cache|video-cache}/{filename}`, `Authorization: Bearer <token>`, and `X-Content-SHA256: <lowercase-or-uppercase-sha256-hex>`. Successful writes return 201. Filenames are restricted to safe ASCII basenames and extensions are allowlisted. Upload bytes are streamed to a temporary file, bounded per media type, hashed, fsynced, and atomically renamed only after checksum verification.

Images are limited to 50 MiB and expire after 2 hours; MP4 files are limited to 1 GiB and expire after 48 hours. Cleanup runs every 10 minutes. Tune these values only to match the source cache policy. Public URL shape is `/{kind}/{filename}`.

## Verification

```sh
go test ./...
go vet ./...
docker build -t media-hk .
```

The public path must be verified through the actual Nginx vhost after deployment: GET, HEAD, a `Range: bytes=0-15` request (expect 206 and `Content-Range`), and an absent filename (expect 404). This repository change does not deploy or alter any server.
