// Renders social.html to docs/user/assets/social.png (1200x630).
// Usage: node render-social.mjs <wyrd.foo checkout>
// The checkout supplies Playwright, its Chromium and the Geist fonts.
import { createRequire } from "node:module";
import { readFileSync, writeFileSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve, dirname } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const site = resolve(process.argv[2] ?? "");
const here = dirname(fileURLToPath(import.meta.url));
const { chromium } = createRequire(join(site, "package.json"))("@playwright/test");
const fonts = join(site, "node_modules/@fontsource-variable");

const html = readFileSync(join(here, "social.html"), "utf8").replaceAll("__FONTS__", pathToFileURL(fonts).href);
const page = join(mkdtempSync(join(tmpdir(), "dcb-social-")), "social.html");
writeFileSync(page, html);

const browser = await chromium.launch();
const tab = await browser.newPage({ viewport: { width: 1200, height: 630 } });
await tab.goto(pathToFileURL(page).href);
await tab.evaluate(() => document.fonts.ready);
await tab.screenshot({ path: join(here, "../../docs/user/assets/social.png") });
await browser.close();
