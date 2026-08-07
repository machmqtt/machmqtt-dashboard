# MachMQTT Dashboard UI

React + TypeScript frontend for the MachMQTT Dashboard. The production build is
written to `internal/api/dist/` and embedded into the Go binary, which serves
it alongside the API; see the [project README](../README.md) and
[docs/](../docs/) for the full picture.

## Development

```sh
npm install
npm run dev        # Vite dev server; proxies /api to the backend on :8080
```

Run the backend alongside it (`make dev-backend` from the repo root), then open
the Vite dev URL.

## Scripts

| Command | Purpose |
|---|---|
| `npm run dev` | Vite dev server with HMR |
| `npm run build` | Type-check (`tsc -b`) and build into `../internal/api/dist/` |
| `npm run lint` | ESLint |
| `npm run preview` | Preview the production build |
| `npm run test:e2e` | Playwright end-to-end suite (see [e2e/README.md](e2e/README.md)) |

## Notes

- Only `index.html` and the static icons in `internal/api/dist/` are tracked in
  git; the hashed `assets/` bundle is gitignored and rebuilt by CI/Docker.
- CI runs `tsc --noEmit`, ESLint, and the Vite build on every push — keep all
  three clean.
