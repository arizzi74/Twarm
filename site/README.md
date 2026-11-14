# Project website

The public site is hosted at
[Twarm (Coding agent control hub)](https://arizzi74.github.io/Twarm/).
This directory contains the static website and Playwright screenshots of the
actual Web UI using fictional demo data. It does not connect to a gateway.

## Publish

Set the repository's **Settings → Pages → Build and deployment → Source** to
**GitHub Actions** once. Push changes in `site/` to `main`; the **Website**
workflow checks the site in Chromium, uploads this directory, and deploys it.
The workflow can also be started from the repository's Actions tab. Starting
it through an API token requires that token to have Actions write permission.

After deployment, open the public site and check the desktop and mobile
screenshots. GitHub Pages publication is independent of gateway and worker
binary releases.

See [the website guide](../docs/website.md) for screenshot regeneration and
local checks, and [screenshot provenance](assets/screenshots/README.md) for
details about the captured interface and demo data.

## Swarm background

`assets/swarm-background.js` draws small copies of the logo's triangular delta
on a decorative Canvas layer. They are faint gray at rest; nearby arrows turn
toward a mouse pointer and fade to a subdued lime, then return to gray. The
layer cannot intercept clicks or keyboard focus. Touch devices and visitors
requesting reduced motion see a static pattern. Animation stops when settled
or when the tab is hidden; pixel density and arrow count are bounded.

`node scripts/test-site.cjs` checks the real pointer response, fading, idle
animation shutdown, reduced-motion/touch behavior and normal site navigation.
