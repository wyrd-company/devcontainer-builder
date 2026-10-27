---
version: alpha
name: dcb
description: >-
  The visual identity of dcb: a stack of three layers with the top layer lit,
  set beside a Geist Mono wordmark. It uses the wyrd.foo palette, the dark
  theme without a suffix and the light theme with `-light`.
colors:
  primary: "#6aa5ff"
  primary-light: "#0b4fa8"
  page: "#0b0b0c"
  page-light: "#fafafa"
  text: "#ededef"
  text-light: "#111113"
  text-2: "#a6a6af"
  text-faint: "#8e8e97"
  accent-bar: "#3b6fc4"
  tile-text: "#0b0b0c"
typography:
  product-name:
    fontFamily: Geist Mono
    fontSize: 60px
    fontWeight: 500
    lineHeight: 1
    letterSpacing: -0.04em
  headline:
    fontFamily: Geist
    fontSize: 64px
    fontWeight: 700
    lineHeight: 1.08
    letterSpacing: -0.04em
  subline:
    fontFamily: Geist Mono
    fontSize: 28px
    fontWeight: 400
    lineHeight: 1.4
  url:
    fontFamily: Geist Mono
    fontSize: 24px
    fontWeight: 400
    lineHeight: 1
rounded:
  tile: 4.57px
spacing:
  mark-grid: 64px
  mark-stroke: 4px
  favicon-grid: 32px
  favicon-stroke: 2.6px
  social-margin: 80px
  social-top: 72px
  social-mark: 72px
  social-mark-gap: 22px
  social-bar: 6px
components:
  logo:
    backgroundColor: "{colors.page-light}"
    textColor: "{colors.text-light}"
    size: 64px
  logo-top-layer:
    backgroundColor: "{colors.page-light}"
    textColor: "{colors.primary-light}"
  logo-dark:
    backgroundColor: "{colors.page}"
    textColor: "{colors.text}"
    size: 64px
  logo-dark-top-layer:
    backgroundColor: "{colors.page}"
    textColor: "{colors.primary}"
  favicon:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.tile-text}"
    rounded: "{rounded.tile}"
    size: 32px
  social-image:
    backgroundColor: "{colors.page}"
    textColor: "{colors.text}"
    typography: "{typography.headline}"
    padding: "{spacing.social-margin}"
    width: 1200px
    height: 630px
  social-wordmark:
    textColor: "{colors.primary}"
    typography: "{typography.product-name}"
  social-subline:
    textColor: "{colors.text-2}"
    typography: "{typography.subline}"
  social-url:
    textColor: "{colors.text-faint}"
    typography: "{typography.url}"
  social-accent-bar:
    backgroundColor: "{colors.accent-bar}"
    height: "{spacing.social-bar}"
---

# dcb design

The tokens above name the values in `docs/user/assets/`. The wyrd.foo design
system supplies the palette and the type faces, so the dcb assets sit beside
every other Wyrd Tools product without a separate theme.

## Overview

dcb is a command-line tool that turns a `devcontainer.json` into an image and
a running container. The identity is plain and technical: one mark, one
accent, monospace for the name.

- **The mark is layers.** Three stacked layers stand for the image layers that
  dcb builds. The top layer is solid and in the accent colour; the two below
  are strokes.
- **The name is monospace.** `dcb` is lower case, in Geist Mono, wherever it
  is the subject.
- **One accent.** Blue is the only hue. The lower layers and all text are
  neutral.

## Colors

Each colour is the wyrd.foo token of the same name. A token without a suffix
is the dark theme's value, and `-light` is the light theme's value.

| Token | Use |
| --- | --- |
| `primary` | The solid top layer on dark backgrounds, the favicon tile and the wordmark on the social image. |
| `primary-light` | The solid top layer on light backgrounds. |
| `page` | The social image background. |
| `page-light` | The page the light logo sits on. |
| `text` | The lower layers of the dark logo, and the headline on the social image. |
| `text-light` | The lower layers of the light logo. |
| `text-2` | The subline on the social image. |
| `text-faint` | The address on the social image. |
| `accent-bar` | The 6px bar along the bottom of the social image. |
| `tile-text` | The mark on the favicon tile. |

`primary-light` and `text-light` meet 4.5:1 on `page-light`, and `primary` and
`text` meet it on `page`.

The SVG files hold these values as fixed colours, because the site loads them
as images and an image cannot read the page's theme.

## Typography

Geist Mono sets the name and the technical lines. Geist sets the headline.
Both faces are the ones the wyrd.foo site self-hosts.

| Token | Where |
| --- | --- |
| `product-name` | The `dcb` wordmark beside the mark on the social image. |
| `headline` | The tagline on the social image: "Build and run development containers from devcontainer.json." |
| `subline` | The line under the headline on the social image. |
| `url` | The docs address at the bottom right of the social image. |

The headline breaks after "Build and run" and after "development containers",
and the second line is `primary`.

## Layout

The mark is drawn on a 64-unit square, with the artwork between x 6 and 58 and
between y 8 and 56. The favicon is drawn on a 32-unit square.

- The top layer is a diamond with corners at (32, 8), (58, 20), (32, 32) and
  (6, 20). Each lower layer is a chevron 12 units below the one above it,
  from (6, y) through (32, y + 12) to (58, y).
- Strokes are `mark-stroke` (4 units) in the logo and `favicon-stroke` (2.6
  units) in the favicon, with round caps and joins. The favicon is thicker
  relative to its square so it holds at 16px.
- The social image is 1200×630. Content starts `social-margin` (80px) from
  the left and right edges and `social-top` (72px) from the top. The mark is
  `social-mark` (72px) square, `social-mark-gap` (22px) from the wordmark.
  The headline starts 244px from the top and the subline 486px. The address
  is right-aligned on the same margin, 56px above the bottom edge.

## Elevation & Depth

The identity is flat. It has no shadows, gradients or outlines around the
mark. The top layer is the only solid shape, and it comes forward by colour
alone.

## Shapes

The mark is made of straight lines at the angles of an isometric grid: the
top layer's sides rise 12 units over 26. The favicon tile has a corner radius
of `tile` (4.57px on its 32px square, one seventh of its side), the same as
the wyrd.foo "W" tile.

## Components

- **Logo.** `docs/user/assets/logo.svg` on light backgrounds and
  `docs/user/assets/logo-dark.svg` on dark backgrounds. The two stroked
  layers are the `textColor` of `logo` and `logo-dark`, and the solid top layer
  is the `textColor` of `logo-top-layer` and `logo-dark-top-layer`, each on the
  page colour it is drawn for. The logo is square, has no tile and no padding,
  and the site shows it at 28, 44 and 72px.
- **Favicon.** `docs/user/assets/favicon.svg`: the mark in `tile-text` on a
  `primary` tile, with the top layer filled. It has the same colours in both
  themes, as the wyrd.foo favicon does.
- **Social image.** `docs/user/assets/social.png`, 1200×630, dark. From the
  top: the dark logo and the `dcb` wordmark in `primary`, the headline, the
  subline, and the address, with `social-accent-bar` along the bottom edge.
  It is rendered from `scripts/social-image/social.html` by `task docs:social`.
  The task uses the Playwright and the fonts of a wyrd.foo checkout, which
  defaults to `/workspaces/tools/wyrd.foo`. Pass another with
  `WYRD_FOO=<path>`.
- **README header.** The light and dark logos, shown by GitHub's
  `#gh-light-mode-only` and `#gh-dark-mode-only` image fragments.

## Do's and Don'ts

- Do keep the top layer solid and in the accent colour, and the lower layers
  as strokes.
- Do use `logo-dark.svg` on dark backgrounds and `logo.svg` on light ones.
- Do set the name as `dcb`, lower case, in Geist Mono.
- Do re-render `social.png` with `task docs:social` when the tagline or the
  headline text changes.
- Don't add a second accent hue, a gradient or a shadow to the mark.
- Don't put the logo on a tile. The favicon is the only tiled form.
- Don't recolour the lower layers to the accent.
