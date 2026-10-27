# dcb brand assets

The published assets live in `docs/user/assets/` and are listed in
`docs/user/docs.yml`.

| File | Use |
| --- | --- |
| `logo.svg` | Logo on light backgrounds. |
| `logo-dark.svg` | Logo on dark backgrounds. |
| `favicon.svg` | Favicon: the mark on the wyrd.foo accent tile, for both themes. |
| `social.png` | Social preview, 1200×630. |

The mark is a stack of three layers with the top layer lit. Colours are the
wyrd.foo tokens: `#0b4fa8` and `#111113` on light, `#6aa5ff` and `#ededef` on
dark. The SVGs hold fixed colours because the site loads them as images.

`social.png` is rendered from `social.html`. From a wyrd.foo checkout that has
its dependencies installed:

```sh
node docs/design/brand/render-social.mjs <path to wyrd.foo>
```
