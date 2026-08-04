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

## Image recovery for usage logs

- Keeps the downstream image API response unchanged: URL responses still
  contain the upstream URL, and base64 responses still contain the original
  base64 payload.
- Starts a background cache job after the response has been written and the
  consume log has been recorded. Cache failures never change the API result or
  delay the customer response.
- Shows the local `/image-cache/{random_name}` URL only inside the NewAPI usage
  log details, so an image can be recovered when a downstream client misses
  the original result.
- Removes cached image files after 2 hours. The cleanup runs at startup and
  hourly, and `/data` is already persisted by the default Docker Compose file.

Optional environment variables:

| Variable | Default | Purpose |
| --- | --- | --- |
| `IMAGE_CACHE_DIR` | `/data/image-cache` | Local image cache directory |
| `IMAGE_CACHE_PUBLIC_BASE_URL` | system server address | Public URL base for usage-log previews |
| `IMAGE_CACHE_MAX_MB` | `50` | Maximum size of one cached image |
| `IMAGE_CACHE_DOWNLOAD_TIMEOUT_SECONDS` | `120` | Background upstream image download timeout |

## Google Nano Banana OpenAI compatibility

- Allows Google Gemini image models, including Nano Banana 2 and Nano Banana
  Pro aliases, to be called through the OpenAI `/v1/images/generations`,
  `/v1/images/edits`, and `/v1/chat/completions` interfaces.
- Converts OpenAI `size` values such as `3:4`, `4:3`, `9:16`, `16:9`, and
  portrait dimensions such as `1024x1365` into Google's
  `generationConfig.imageConfig.aspectRatio`, so the requested ratio is not
  silently reset to `1:1`.
- Accepts Google image settings in either snake_case or camelCase under
  `extra_body.google`, including `image_config`/`imageConfig`,
  `aspect_ratio`/`aspectRatio`, and `image_size`/`imageSize`.
- Accepts URL, base64, multipart, and multiple reference-image inputs and
  converts Gemini `inlineData` results to OpenAI `b64_json` results.
- Normalizes native Gemini image responses from snake_case, Markdown image URLs,
  and OpenAI-style `b64_json`/`url` payloads into standard `inlineData` parts
  for Gemini clients such as infinite-canvas frontends.
- Bridges native Gemini `generateContent` calls routed through an OpenAI channel:
  non-streaming Banana image requests use `/v1/images/generations` or
  `/v1/images/edits` with the documented OpenAI fields, while ordinary Gemini
  vision/text requests and streaming calls keep `/v1/chat/completions`.
- Normalizes the top-level image options emitted by common canvas clients
  (`resolution`, `aspectRatio`, `quality`, `n`, and `responseModalities`) into
  the native `generationConfig` shape before conversion. An explicit
  resolution always wins over the UI quality alias, so `resolution: 4k` cannot
  be downgraded by a simultaneous `quality: standard` field.
- Carries the normalized Gemini image tier and candidate count into billing
  metadata, keeping pricing aligned even when the relay reconstructs the
  request body between parsing and upstream conversion.
  Markdown images, content-item images, `message.images`, data URLs, and Images
  API payloads are converted back to Gemini `inlineData` responses.
- Derives supported Gemini aspect ratios from canvas-style `width`/`height` or
  dimension strings, preventing compatible non-square requests from falling
  back to `1:1` when a client omits `aspectRatio`.
- Gemini image generation maps OpenAI `n` to Gemini `candidateCount` (bounded by
  the shared image-count limit) so upstream models that support multiple
  candidates can return more than one image.
- Existing Imagen models continue to use the original `predict` request path;
  only Gemini `generateContent` image models use this bridge.

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
