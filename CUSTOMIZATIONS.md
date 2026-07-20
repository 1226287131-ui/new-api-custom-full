# Custom Feature Set

This repository combines three independently tested extensions on top of
QuantumNous/new-api commit `7c28993f6bd9e92616f3f578212577f8b7c40b45`.
The original project metadata, notices, and license files are preserved.

## Branches

- `main`: complete build containing both feature sets.
- `feature/image-resolution-billing`: image-resolution billing only.
- `feature/video-sora-cache`: Sora-compatible video relay and caching only.
- `upstream`: remote that tracks `https://github.com/QuantumNous/new-api`.

## Image-resolution billing

- Adds per-model `1K`, `2K`, and `4K` prices through the
  `ImageResolutionPrice` system option.
- Recognizes OpenAI-style and Gemini-native image resolution fields.
- Classifies decimal megapixel boundaries so standard `1920x1080` and
  `3840x2160` dimensions enter the advertised `2K` and `4K` tiers.
- Normalizes resolution and image-count aliases across top-level,
  `parameters`, `generationConfig`, `input`, and `extra_body` payloads.
- Rejects conflicting or invalid billing parameters instead of silently
  selecting a cheaper tier.
- Revalidates the converted outbound payload against the frozen pre-consume
  tier and count, including channel parameter overrides.
- Applies image count and the effective group or special user multiplier.
- Shows all resolution prices in the admin editor and public pricing views.
- Treats resolution-priced models as billable in OpenAI and Gemini model
  listing endpoints.

Billing precedence is:

```text
tiered expression > image resolution > fixed model price > token ratio
```

## Sora-compatible video relay

- Adds channel type `59` for the NewAPI video task adapter.
- Supports `POST /v1/videos`, task polling, and the standard content route.
- Accepts authenticated reference-image uploads and stores them temporarily in
  `/data/video-input-cache` for JSON-only upstreams.
- Downloads completed upstream videos into `/data/video-cache` before marking
  the task successful, preventing upstream result URLs from being exposed.
- Redacts upstream URLs and provider task IDs at submission, polling, storage,
  and task-response boundaries.
- Publishes cached results as `/video-cache/{task_id}.mp4` with `HEAD` and HTTP
  Range support.
- Removes completed video files after 48 hours and input images after 12 hours.

Optional environment variables:

| Variable | Default | Purpose |
| --- | --- | --- |
| `VIDEO_CACHE_DIR` | `/data/video-cache` | Completed MP4 storage |
| `VIDEO_CACHE_MAX_MB` | `1024` | Maximum cached MP4 size |
| `VIDEO_CACHE_DOWNLOAD_TIMEOUT_SECONDS` | `600` | Upstream download timeout |
| `TASK_TERMINAL_ERROR_TIMEOUT_MINUTES` | `30` | Maximum age for contradictory upstream terminal-error responses before failing the task; negative disables this safeguard |
| `VIDEO_INPUT_CACHE_DIR` | `/data/video-input-cache` | Reference-image storage |
| `VIDEO_INPUT_CACHE_MAX_MB` | `20` | Maximum reference-image size |
| `VIDEO_INPUT_CACHE_PUBLIC_BASE_URL` | system server address | Public input URL base |

## Openai Video multi-reference relay

- Adds the independent `Openai Video` channel type `60` without changing the
  existing Sora, NewAPI Video, or DoubaoVideo adaptors.
- Submits JSON requests to the upstream `POST /v1/videos` endpoint and polls
  tasks through `GET /v1/videos/{task_id}`.
- Preserves ordered `images`, `videos`, and `audios` URL arrays for
  multi-reference Seedance-style generation requests.
- Accepts native `duration`, `ratio`, and `resolution` fields while translating
  OpenAI/Sora aliases such as `seconds`, `size`, and `input_reference`.
- Supports channel model mapping, for example from a downstream
  `seedance-2.0` model name to the provider's deployment name.
- Keeps provider task IDs and result URLs private. Completed videos are exposed
  through the authenticated local `/v1/videos/{task_id}/content` proxy and are
  streamed without storing the completed video on the server.
- Stores the selected multi-key credential with the private task state so
  polling and same-origin content fetches use the key that created the task.
- Does not forward the provider Bearer credential to cross-origin CDN result
  URLs.

## Build

The upstream `Dockerfile` remains unchanged. `Dockerfile.custom` uses locked
BuildKit caches and reduced Bun concurrency for lower-memory servers:

```bash
docker build -f Dockerfile.custom -t newapi-custom:full .
```

## Focused verification

```bash
go test ./relay/helper ./relay/channel/gemini ./setting/ratio_setting
go test ./relay/channel/task/newapivideo ./service ./model
go test ./relay/channel/task/openaivideo ./relay/channel/task/newapivideo ./relay/channel/task/sora

cd web/default
bun run typecheck
bunx oxlint -c .oxlintrc.json \
  src/features/system-settings/models/image-resolution-pricing-editor.tsx \
  src/features/usage-logs/components/columns/task-logs-columns.tsx
```

Do not commit deployment `.env` files, API keys, database dumps, logs, cached
images, or cached videos. Each deployment should keep its own database and
mounted `/data` directory while using the same application image.
