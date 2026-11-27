# Game Session Upload

One Cloud Run service receives session recordings for registered Android package IDs. Games choose the files in their ZIP and the service stores the received archive bytes unchanged.

## Upload

Send `POST /v2/sessions` with content type `application/zip`.

| Header | Value |
| --- | --- |
| `X-Package-Id` | Registered Android application ID, for example `team.nex.nba`. |
| `X-Session-Id` | A UUID written as 32 lowercase hexadecimal characters. |
| `X-Session-Started-At` | Recording start in RFC 3339, normalized to UTC for the object key. |
| `X-App-Version` | Optional game version. |

The ZIP can be empty and has zero required files. The service checks ZIP structure and archive sizes without decoding game files. Both the request and total declared uncompressed contents are limited to 16 MiB. The Unity client uses a local `common.json` sidecar for its queue and supplies the request headers from it.

Register destinations using the `SESSION_DATA_DESTINATIONS` JSON map. For a route with prefix `sessions`, the service writes `sessions/<package-id>/<UTC-start>-<session-id>-<SHA-256>.zip`. Fixed-width UTC timestamps make object names sort by recording time within a package. The package ID and session metadata are also stored on the object. The storage object's creation time records the upload time.

Successful uploads return a JSON object with a `uri` field. Retrying the same archive and session metadata returns the same object URI. `GET /health` reports service availability.

## Legacy Basketball uploads

Already-installed Basketball clients can post to `/sessions` with `X-Game-Id: basketball` and their existing `X-Session-Directory`. Their object naming and manifest contract are preserved.

Include `session.json` and each file it declares. The manifest contains a positive integer `schema_version`, the `session_id` from the directory name, a final `status`, and a `files` object mapping game-specific keys to unique archive filenames. An optional `photo` object with `status` set to `captured` names one additional entry in `file`. Final statuses are `completed`, `cancelled`, `restarted`, `application_quit`, `disabled`, `destroyed`, and `recording_error`.

The ZIP contains 2–32 regular files at the root. Filenames start with an ASCII letter or digit and may contain letters, digits, periods, underscores, and hyphens, up to 128 characters. Both the request and the total uncompressed ZIP entries are limited to 16 MiB. The manifest is limited to 256 KiB. The legacy Basketball client uses this contract.

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

The existing Cloud Run service is named `game-session-data-upload`. Deploy from this repository root.

```sh
gcloud run deploy game-session-data-upload --source . \
  --project development-179808 --region us-central1
```

Add each Android package ID as a key in the current `SESSION_DATA_DESTINATIONS` map. For Basketball, keep the legacy key alongside the new Android package ID.

```json
{
  "basketball": {
    "bucket": "development-179808-game-session-data-upload",
    "prefix": "sessions"
  },
  "team.nex.nba": {
    "bucket": "development-179808-game-session-data-upload",
    "prefix": "sessions"
  }
}
```

Preserve other entries when updating the map. The Cloud Run runtime service account needs object-creation access to each destination. Separate application IDs get separate folders even when they use the same bucket and prefix.
