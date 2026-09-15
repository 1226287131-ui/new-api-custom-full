# Custom Feature Set

This repository preserves the custom feature set at commit
`239a4597ca91ca65f404f454ce7e3631986d5617` while merging the official
QuantumNous/new-api mainline at
`5caafd3d84dc74c8b0d081524f57a534b3980cb0` (2026-09-15, Asia/Shanghai).
The original project metadata, notices, and license files are preserved.

## Branches

- `codex/merge-upstream-20260915`: complete local integration branch.
- `codex/pre-upstream-merge-20260915`: pre-merge recovery point (`239a4597`).
- `official/main`: official upstream revision used for this integration.

The source integration does not deploy a server or migrate production data.
Server-only settings, proxy services, backup jobs and Sub2API customizations
are separate from this NewAPI repository.

## Compatibility decisions for the 2026-09-15 merge

- Existing channel type IDs `59` through `64` keep their custom meanings.
  Official Task Plugin, vLLM and SGLang use `65`, `66` and `67`, respectively,
  in both the backend and frontend. Existing custom channel rows are not
  silently reinterpreted as task plugins.
- The new plugin engine and legacy video adaptors coexist. For a shared video
  endpoint/model, an authorized legacy video channel keeps the existing
  request contract, including 14/30-second requests. An explicitly pinned
  Task Plugin channel, or a group with only plugin channels, uses the new
  plugin contract. The legacy channel filter also applies to retries.
- Legacy video task polling and cache recovery remain independent of the
  new plugin failure counters. A temporary polling/cache failure does not
  turn a provider-success video into a failed/refunded task. The reverted
  embedded-error inference is not reintroduced.
- Task creation uses the official durable task/settlement flow through a
  legacy adaptor bridge. A successful submission response is released only
  after the task has been persisted and its submission accounting completed.
- The new atomic model-pricing API preserves `ImageResolutionPrice`,
  `billing_setting.task_billing_pricing` and
  `billing_setting.scheduled_discount`, including optimistic concurrency,
  reset and rollback behavior. Ordinary price synchronization does not erase
  the custom resolution prices or scheduled discounts.
- New built-in expressions do not silently replace administrator-configured
  legacy prices. Frozen group/user ratios and scheduled discounts continue
  through image reservation adjustments and asynchronous task settlement.
- Frontend pages use the new upstream component structure; the custom
  channel fields, pricing controls, group/user overrides and usage views are
  integrated into those pages rather than leaving an obsolete second UI.

## Accounts, logs and routing

- Tokens can select multiple groups and retain an explicit Auto group order.
  Selection, retries and the effective billed group follow those settings.
- Administrators can set a user's individual ratio inside a group. The
  personal ratio overrides that group's ordinary ratio; the selected ratio
  is frozen for task settlement.
- Scheduled model discounts use Beijing time and are visible in model
  pricing. Reservation, final charge and log metadata use the same discount.
- The real-time channel usage dashboard, error logs, task request-body display
  and cached image/video recovery remain available. Recorded task request
  bodies are capped at 1 MiB.
- Usage APIs hide upstream model metadata; task display uses the original
  requested model rather than inferring it from upstream task data. Error
  messages and task result boundaries retain upstream identifier/URL masking.
- Host-only Responses base URLs (`/responses`) and the normal `/v1/responses`
  entry remain supported alongside the new plugin router.
- Channel-level upstream egress and video-download egress are separate
  switches. `UPSTREAM_EGRESS_PROXY` selects the configured relay egress;
  turning the switch off preserves the ordinary channel proxy behavior.
  The new Responses WebSocket, AdvancedCustom balance query, task-plugin
  OAuth lifecycle and plugin artifact paths also honor this switch.
- Existing cache download protections, trusted upstream ports, IPv4 video
  downloads and cache recovery remain in place.
- Login avoids the old shared CriticalRateLimit false-positive 429 behavior,
  while API-wide throttling, login failure protections and secure account
  recovery remain. Optional session limits are described below.
- Versioned frontend assets retain cache-safe deployment behavior: stale
  JavaScript requests return 404 instead of the HTML homepage, and HTML is
  not served with long-lived asset caching.

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

## Optional login Session limits

- Allows `USER_SESSION_ACTIVE_LIMIT=0` to disable the per-user active Session
  limit.
- Allows `USER_SESSION_ISSUANCE_LIMIT=0` to disable the per-user Session
  issuance limit while retaining normal Session expiry and cleanup.
- Negative and invalid values still fall back to the secure defaults.

## Sora-compatible video relay

- Adds channel type `59` for the NewAPI video task adapter.
- Supports `POST /v1/videos`, task polling, and the standard content route.
- Accepts authenticated reference-image uploads and stores them temporarily in
  `/data/video-input-cache` for JSON-only upstreams.
- Downloads completed upstream videos into `/data/video-cache`. Provider
  success remains successful while a failed local download is retried;
  cache availability is reported separately without exposing the upstream URL.
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
- Selects the upstream endpoint/contract in channel settings, retaining
  `/v1/videos`, `/v1/video/generations` and `/v1/videos/generations`.
  Endpoint-specific submission and polling behavior is covered by the adaptor
  tests.
- Ordinary OpenAI Video durations accept integer values from 5 through 30
  seconds, including 14 seconds; the separate Seedance 2.5 profile keeps its
  documented 4-through-30-second contract.
- Preserves ordered `images`, `videos`, and `audios` URL arrays for
  multi-reference Seedance-style generation requests.
- Accepts native `duration`, `ratio`, and `resolution` fields while translating
  OpenAI/Sora aliases such as `seconds`, `size`, and `input_reference`.
- Supports channel model mapping, for example from a downstream
  `seedance-2.0` model name to the provider's deployment name.
- Keeps provider task IDs and result URLs private. Completed videos support
  local caching/recovery and the authenticated
  `/v1/videos/{task_id}/content` entry.
- Stores the selected multi-key credential with the private task state so
  polling and same-origin content fetches use the key that created the task.
- Does not forward the provider Bearer credential to cross-origin CDN result
  URLs.

### Seedance 2.5 profile for Openai Video

- For a 60 `Openai Video` channel, select **Seedance 2.5 (unrestricted model
  names)** in Channel Extra Settings. This activates the SD2.5 contract for
  every downstream model on that channel, so downstream model aliases are not
  restricted. Use ordinary channel model mapping to select the upstream
  deployment name.
- Existing channels retain the former name-based fallback for `video-v3`,
  `seedance-2.5`, and `sd2.5`, preserving backward compatibility.
- Accepts integer `duration` or string/integer `seconds` from 4 through 30;
  omitted duration defaults to 4 seconds.
- Enforces 720p output metadata and billing even when a client sends another
  resolution alias, while accepting the documented `auto` and fixed aspect
  ratios.
- Supports up to 30 image, 10 video, and 10 audio references. URL-based
  `input_reference` arrays are accepted; recognizable video and audio file
  extensions are classified as their corresponding media type.
- Accepts the native `content[]` contract (`text`, `image_url`, `video_url`,
  `audio_url`). When a client uses separate `videos` or `audios` arrays, the
  adaptor converts the request into the native `content[]` format required by
  the upstream.

## Grok Video native relay

- Adds the independent `Grok Video` channel type `63` without changing the
  existing NewAPI Video or Openai Video adaptors.
- Accepts the native Grok `POST /v1/videos` multipart contract: `model`,
  `prompt`, `aspect_ratio`, `seconds`, `resolution`, and an optional PNG
  `input_reference` file.
- Uses deterministic defaults of `16:9`, `5` seconds, and `720p` when the
  optional provider fields are omitted, so per-second and per-resolution task
  billing remain predictable.
- Polls `GET /v1/videos/{task_id}` and downloads completed results through the
  authenticated `GET /v1/videos/{task_id}/content` endpoint. A cache failure
  does not undo the provider's successful task status.
- Returns only the local shareable `/video-cache/{task_id}.mp4` result URL and
  retains the video under the existing 48-hour cache cleanup policy.

## MiniMax Video native relay

- Adds the independent `MiniMax Video` channel type `64` without changing the
  existing video adaptors.
- Supports the documented `POST /v1/videos` JSON and multipart contracts with
  `model`, `prompt`, `seconds`/`duration` (4-15, default 5), `size`, `audio`,
  `prompt_enhance`, `resolution`, `clarity`, `aspect_ratio`, `megapixels`, and
  `metadata.multiple`.
- Supports `mode: "first_last_frame"` for exactly two ordered reference images:
  the first image is used as the first frame and the second image as the last
  frame. This mode rejects reference videos, reference audio, and companion
  audio; two ordinary reference images without `mode` remain a normal
  multi-reference request.
- Accepts image references (`input_reference`, `image`, `images`,
  `reference_images`), video references (`reference_video`, `reference_videos`),
  video companion audio (`reference_video_audio`, `reference_video_audios`),
  and independent audio (`reference_audio`, `reference_audios`) in one request.
  JSON accepts public HTTP(S) URLs; multipart accepts the corresponding repeated
  file fields. Limits are 9 images, 3 videos, 3 companion audio files, and 3
  independent audio files per request.
- Normalizes legacy `video_urls` and `audio_urls` aliases, de-duplicates media,
  validates URL and multipart file inputs, and applies the configured SSRF
  protection before accepting a remote reference URL.
- Polls `GET /v1/videos/{task_id}` and uses the authenticated
  `/v1/videos/{task_id}/content` endpoint as the fallback cache source when the
  provider does not return a separate result URL.
- Exposes completed videos only through the local `/video-cache/{task_id}.mp4`
  URL and removes cached files after 48 hours.
- H3's exact dimension table in `constant/minimax_h3.go` takes precedence over
  generic resolution classification: for example `1376x768` and `1024x1024`
  belong to `768p`, while `1920x1088` and `1440x1440` belong to `1080p`.
  The old broad megapixel approximation is not used to reclassify these sizes.
- Standard 480p/720p/768p/1080p/2K/4K dimensions and portrait forms retain
  their generic billing classification. Standard `1920x1080` bills as 1080p.
  The UI displays `2K`; its compatible stored task-price key remains `1440p`.
- `size: 2k/2K` and `4k/4K` are forwarded as uppercase `2K`/`4K` and use the
  corresponding price. These enum sizes also require `aspect_ratio`.
- The channel prompt-enhancement switch overrides client `prompt_enhance`
  values for JSON and multipart requests. Existing workflow selection,
  strict H3 payloads, mixed media and first/last-frame behavior remain.

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
go test ./relay/channel/task/grokvideo ./service

cd relaykit
GOWORK=off go test ./...
GOWORK=off go build ./...

cd ../web
bun run typecheck
bun run test
bun run lint
bun run build
```

## 2026-09-15 integration verification

Database checks use local scratch instances only: SQLite `3.50.4`, MySQL
`8.4.6` and PostgreSQL `17.6`. These are tested supported versions, not a
claim that every older engine version was exercised.

| Check | Coverage |
| --- | --- |
| `TestCustomUpgradePreservesStoredConfiguration` | Fresh merged schema, custom `239a4597` schema and official release `v1.0.0-rc.37` schema; merge migration and second migration on all three engines |
| Stored data preservation | User/token balances, multi-group token setting, channel IDs 59–64 and JSON settings, image/task prices, discounts, in-progress task/private cache fields, log count, personal group ratio and uniqueness constraints |
| Separate log database | MySQL and PostgreSQL main/log databases are physically separate scratch databases; SQLite uses the shared-file mode |
| `TestMigrationSchemaStability` | Real three-engine column/index/constraint migrations and idempotency |
| Token, prefill group and session migration tests | Uniqueness and refresh-hash schema preservation on all three engines |
| `TestFixedPriceBillingDatabaseMatrix` | Pre-consume, actual-usage settlement, refund/insufficient balance, image-count reservation changes and zero fixed price; separate main/log databases |
| Atomic pricing API database tests | Image tiers, video tiers and scheduled discounts read/update/reset together; version conflicts and rollback on all three engines |
| Legacy video submission | A real local HTTP upstream receives one 30-second request; task persistence/settlement precede the success response and upstream IDs stay private |
| Router composition | All built-in plugins and production API/dashboard/relay/video/task/web routes register together without conflicts |
| Egress integration | Local HTTP/CONNECT proxies verify WebSocket, balance and plugin OAuth submission/poll/artifact routing, including disabled/unconfigured fallback |

Validation runtime: Go `1.27.0` on Windows, Bun `1.4.2`. Windows builds and a
CGO-disabled Linux/amd64 build include the newly built frontend. This verifies
compilation, not a live Linux deployment. The independent `relaykit` module
is tested and built with `GOWORK=off`.

Final root-module `go test -p 2 ./...` and `go build -p 2 ./...` pass.
The controller suite initially exposed unclosed SQLite test connections on
Windows; its fixtures now close every opened connection, including repeated
startup and separate audit-log connections. Legacy duration/endpoint tests
and plugin channel-ID fixtures were aligned with their preserved contracts.
The final suite passes without skipping these failures.

Frontend typecheck, changed-file lint, pricing/channel/token/log regressions
and the production build pass. Follow-up regression batches contained 27,
73, 122 and 78 passing tests, with overlap between batches. Seven locales
have no missing translation keys. Full lint has the same 194 pre-existing
errors as the pinned official source, with no additional errors from this
merge; it is not reported as a clean full-lint run. The existing Windows
cross-drive dependency junction triggers a font URI build error, so the
identical frontend source was built with source and dependencies on D: and
the clean output copied into `web/dist`. Authenticated browser end-to-end
testing and external provider acceptance were not performed.

The upgrade fixture is opt-in. Set `NEWAPI_UPGRADE_FIXTURE=create` with the
source checkout and `NEWAPI_UPGRADE_SOURCE=custom`, `rc37` or `merged`, then run
`NEWAPI_UPGRADE_FIXTURE=verify` with this checkout. Use dedicated loopback
`SQL_DSN`/`LOG_SQL_DSN` databases, or `SQL_DSN=local` with
`NEWAPI_UPGRADE_SQLITE` pointing to a scratch file. Do not run it on production
data. The old custom checkout contains unrelated stale video test enum imports;
its fixture creation was compiled against its unchanged production `model/*.go`
files plus only this fixture test. The merged package uses its complete suite.

Deployment must preserve the existing database/Redis configuration, mounted
data directories, encryption/session secrets and proxy environment variables.
Official startup now rejects MySQL/PostgreSQL wallet columns that are still
32-bit; do not bypass that safeguard. The tested old custom schema upgraded
without bypassing it. Official response-header timeout is now configurable as
`RELAY_RESPONSE_HEADER_TIMEOUT` (default 1800 seconds); `0` restores the former
unbounded header wait. This is distinct from an already-open response stream.

No production migration, upstream paid request, GitHub push or live deployment
is part of this source integration verification.

Do not commit deployment `.env` files, API keys, database dumps, logs, cached
images, or cached videos. Each deployment should keep its own database and
mounted `/data` directory while using the same application image.
