# __APP__

A Go API on Postgres with Firebase sign-in, and a React + shadcn/ui admin.

```
backend/   Go API: net/http, pgx, SQL migrations, sqlc queries, Firebase ID-token checks
admin/     Vite + React + TypeScript + Tailwind + shadcn/ui
```

## Run it

```sh
cp .env.example .env          # set FIREBASE_PROJECT_ID, or keep AUTH_DEV_BYPASS=1 locally
make db                       # Postgres in Docker
make backend                  # API on http://localhost:8080 (migrates on start)
make admin                    # admin on http://localhost:5173
```

For Firebase sign-in, create a web app in the Firebase console, enable Email/Password
and Google providers, copy `admin/.env.example` to `admin/.env` and fill in the keys,
set `VITE_AUTH_DEV_BYPASS=0`, and set `FIREBASE_PROJECT_ID` (and `AUTH_DEV_BYPASS=0`)
for the backend.
