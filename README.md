# Game Session Upload Server

A server side end point that accepts [game session uploads](https://github.com/nex-team-inc/nex-unity-packages/tree/main/GameSessionUpload/Package) and stores them in Cloud Storage.

## Download recordings

Recordings are stored in the `development-179808-game-session-data-upload` bucket under `sessions/<android-package-id>/`.

For Basketball, list the recordings and replace `<file>.zip` with the archive you want to download.

```sh
gcloud storage ls 'gs://development-179808-game-session-data-upload/sessions/team.nex.nba/'
gcloud storage cp 'gs://development-179808-game-session-data-upload/sessions/team.nex.nba/<file>.zip' .
```

Older Basketball recordings are under `sessions/<session-directory>/`.
