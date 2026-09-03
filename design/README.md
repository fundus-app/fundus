# Fundus brand assets

Generated on 2026-09-03 with OpenAI gpt-image-2 (`scripts/gen-logo.mjs`), selected from
eleven candidates, finished with ImageMagick (`scripts/make-icons.sh`). The other candidates
are kept outside the repository.

| File | Use |
|---|---|
| `logo/source-flat.png` | The chosen design on paper, 1024 px: the source for every icon. |
| `logo/fundus-mark-128.png` | Transparent mark, 128 px, for documents. |
| `logo/fundus-lockup-half.png` | Mark + wordmark (Fraunces 500) on paper, for the README and the website. |
| `../app/assets/icon/app_icon.png`, `mark.png`, `mark-dark.png` | What the app ships; regenerate with the script below. |
| `../app/web/favicon.png`, `../app/web/icons/` | Web icons. |

Idea: three sheets settle into a vessel, the newest on top in ochre. That is the product in one
picture: raw captures come in, stay intact, and the stock (the *fundus*) grows.

Logo palette: paper `#f6f2ea`, ink `#1f1d1a`, ochre `#b8641c` (the app uses the slightly lighter surface `#FCFAF5` and accent `#B8620E`). Type: Fraunces (headings), Inter (UI),
JetBrains Mono (ids and code); all three are bundled with the app.

Regenerate icons after changing the source: `scripts/make-icons.sh design/logo/source-flat.png`,
then `cd app && dart run flutter_launcher_icons`.
