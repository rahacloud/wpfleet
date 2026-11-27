# Static files

These files are embedded in the wpfleet binary and served under `/static/`. Nothing in the UI loads from a CDN.

| File | What it is | Version | License |
| --- | --- | --- | --- |
| `htmx-2.0.11.min.js` | [htmx](https://htmx.org), `dist/htmx.min.js` of the npm package `htmx.org` | 2.0.11 (2026-09-22) | Zero-Clause BSD, `LICENSE.htmx` |
| `htmx-ext-sse-2.2.4.min.js` | the htmx server-sent events extension, `dist/sse.min.js` of the npm package `htmx-ext-sse` | 2.2.4 (2025-10-18) | Zero-Clause BSD, `LICENSE.htmx-ext-sse` |
| `app.css` | wpfleet's own stylesheet, written with logical properties so it lays out left to right and right to left | | Apache-2.0, like wpfleet |

The version is in each vendored file's name, so a new version is a new URL and no browser keeps the old one. To upgrade, download the new `dist` files from the npm registry, rename them with the version, update the `<script>` tags in `layout.templ`, run `just generate`, and update this table.

SHA-256 of the vendored files as downloaded:

```text
6fdc75f204e6bdefa99b69bf1e6d4ac69b8a364f77929f45c13476b4000f717  htmx-2.0.11.min.js
98a46496de0c3605fbffdce9167ba427bdd9553184f83f149c261891a92c0136  htmx-ext-sse-2.2.4.min.js
```
