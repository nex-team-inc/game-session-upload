# Game session data upload

This Cloud Run service receives completed game recordings. Each game captures and defines its own payload. The server validates the shared archive envelope and writes it to the destination registered for that game.

Send a finished ZIP to `POST /sessions` as `application/zip` with two headers. The current shared endpoint is `https://game-session-data-upload-626314545571.us-central1.run.app/sessions`.

| Header | Value |
| --- | --- |
| `X-Game-Id` | A configured game ID, such as `basketball`. |
| `X-Session-Directory` | The session directory name, formatted as a UTC timestamp (`yyyyMMddTHHmmssfffZ`), a hyphen, and a 32-character lowercase hexadecimal session ID. |

The server selects the bucket and prefix from its own configuration. It writes `<prefix>/<session directory>/<SHA-256>.zip` with a create-only precondition and returns the object's `gs://` URI in a JSON `uri` field. Posting identical bytes again returns the same URI. `GET /health` reports availability.

## Session data format

Include `session.json` and each file it declares. The manifest contains a positive integer `schema_version`, the `session_id` from the directory name, a final `status`, and a `files` object mapping game-specific keys to unique archive filenames. An optional `photo` object with `status` set to `captured` names one additional entry in `file`. Final statuses are `completed`, `cancelled`, `restarted`, `application_quit`, `disabled`, `destroyed`, and `recording_error`.

The ZIP contains 2–32 regular files at the root. Filenames start with an ASCII letter or digit and may contain letters, digits, periods, underscores, and hyphens, up to 128 characters. Both the request and the total uncompressed ZIP entries are limited to 16 MiB. The manifest is limited to 256 KiB. Every game uses these checks and chooses its own data files.

For example, a game that records its own manifest and a compressed event stream can use this envelope.

```json
{
  "schema_version": 1,
  "session_id": "0123456789abcdef0123456789abcdef",
  "status": "completed",
  "files": {
    "recording_manifest": "manifest.json",
    "events": "events.jsonl.gz"
  }
}
```

The ZIP then contains `session.json`, `manifest.json`, and `events.jsonl.gz` at its root. The directory header must end with the same 32-character session ID.

## Deploy

To update the existing service, run this from the repository root. Cloud Run keeps the configured game destinations.

```sh
gcloud run deploy game-session-data-upload --source . \
  --project development-179808 --region us-central1
```

For a first deployment, the example below registers basketball. The runtime service account has `roles/storage.objectCreator` under `sessions/` in the private `development-179808-game-session-data-upload` bucket.

```sh
gcloud run deploy game-session-data-upload \
  --source server/game-session-data-upload \
  --project development-179808 --region us-central1 \
  --service-account game-session-data-upload@development-179808.iam.gserviceaccount.com \
  --min-instances 0 --max-instances 2 --cpu 1 --memory 256Mi --concurrency 2 --timeout 90 \
  --update-env-vars='^|^SESSION_DATA_DESTINATIONS={"basketball":{"bucket":"development-179808-game-session-data-upload","prefix":"sessions"}}'
```

A Cloud Run administrator can grant anonymous invocation for all games using this service.

```sh
gcloud run services add-iam-policy-binding game-session-data-upload \
  --project development-179808 --region us-central1 \
  --member=allUsers --role=roles/run.invoker
```

## Add a game

Add the game's ID, private bucket name, and object prefix to the existing `SESSION_DATA_DESTINATIONS` JSON map. Preserve all existing entries when updating this environment variable or redeploying. Choose a separate prefix when sharing a bucket across games. Give the runtime service account `roles/storage.objectCreator` on the destination prefix if it lacks that access. The new game posts its manifest and files to the same URL with its own `X-Game-Id` header. The public invoker grant applies to the service across revisions.
