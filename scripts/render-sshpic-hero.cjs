#!/usr/bin/env node

const fs = require("node:fs");
const fsp = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const { spawnSync } = require("node:child_process");

const ROOT = path.resolve(__dirname, "..");
const ASSET_DIR = path.join(ROOT, "docs", "assets");
const HTML_PATH = path.join(ASSET_DIR, "sshpic-hero.html");
const PNG_PATH = path.join(ASSET_DIR, "sshpic-hero.png");
const MP4_PATH = path.join(ASSET_DIR, "sshpic-hero.mp4");
const GIF_PATH = path.join(ASSET_DIR, "sshpic-hero.gif");
const TMP_DIR = path.join(os.tmpdir(), `sshpic-hero-${process.pid}`);
const FRAME_DIR = path.join(TMP_DIR, "frames");
const WIDTH = 1280;
const HEIGHT = 720;
const FPS = 24;
const DURATION = 9;
const FRAME_COUNT = FPS * DURATION;

function requireFromCandidates(name) {
  const candidates = [
    name,
    path.join(os.homedir(), ".cache", "codex-runtimes", "codex-primary-runtime", "dependencies", "node", "node_modules", name),
  ];

  for (const candidate of candidates) {
    try {
      return require(candidate);
    } catch (error) {
      if (error.code !== "MODULE_NOT_FOUND") {
        throw error;
      }
    }
  }

  throw new Error(`Could not load ${name}. Install it or use the Codex bundled runtime.`);
}

function run(command, args) {
  const result = spawnSync(command, args, {
    cwd: ROOT,
    stdio: "inherit",
  });

  if (result.error) {
    throw result.error;
  }

  if (result.status !== 0) {
    throw new Error(`${command} exited with status ${result.status}`);
  }
}

async function launchBrowser(chromium) {
  const common = {
    headless: true,
    args: [
      "--disable-background-timer-throttling",
      "--disable-renderer-backgrounding",
      "--font-render-hinting=medium",
    ],
  };

  try {
    return await chromium.launch({ ...common, channel: "chrome" });
  } catch (error) {
    const chrome = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
    return chromium.launch({ ...common, executablePath: chrome });
  }
}

function humanSize(bytes) {
  const units = ["B", "KB", "MB", "GB"];
  let size = bytes;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${size.toFixed(size >= 10 || unit === 0 ? 0 : 1)} ${units[unit]}`;
}

async function main() {
  const { chromium } = requireFromCandidates("playwright");

  await fsp.mkdir(ASSET_DIR, { recursive: true });
  await fsp.rm(TMP_DIR, { recursive: true, force: true });
  await fsp.mkdir(FRAME_DIR, { recursive: true });

  const browser = await launchBrowser(chromium);
  const page = await browser.newPage({
    viewport: { width: WIDTH, height: HEIGHT },
    deviceScaleFactor: 1,
  });

  await page.goto(`file://${HTML_PATH}?render=1`, { waitUntil: "load" });
  await page.evaluate(() => document.fonts.ready);

  for (let frame = 0; frame < FRAME_COUNT; frame += 1) {
    const seconds = frame / FPS;
    await page.evaluate((time) => window.__setHeroTime(time), seconds);
    await page.screenshot({
      path: path.join(FRAME_DIR, `frame-${String(frame).padStart(4, "0")}.png`),
      type: "png",
    });
  }

  await page.evaluate(() => window.__setHeroTime(8.2));
  await page.screenshot({ path: PNG_PATH, type: "png" });
  await browser.close();

  const input = path.join(FRAME_DIR, "frame-%04d.png");
  const palette = path.join(TMP_DIR, "palette.png");

  run("ffmpeg", [
    "-y",
    "-framerate",
    String(FPS),
    "-i",
    input,
    "-c:v",
    "libx264",
    "-pix_fmt",
    "yuv420p",
    "-crf",
    "18",
    "-movflags",
    "+faststart",
    MP4_PATH,
  ]);

  run("ffmpeg", [
    "-y",
    "-framerate",
    String(FPS),
    "-i",
    input,
    "-vf",
    "fps=15,scale=1280:720:flags=lanczos,palettegen=stats_mode=diff",
    palette,
  ]);

  run("ffmpeg", [
    "-y",
    "-framerate",
    String(FPS),
    "-i",
    input,
    "-i",
    palette,
    "-filter_complex",
    "fps=15,scale=1280:720:flags=lanczos[x];[x][1:v]paletteuse=dither=bayer:bayer_scale=3",
    "-loop",
    "0",
    GIF_PATH,
  ]);

  await fsp.rm(TMP_DIR, { recursive: true, force: true });

  for (const output of [PNG_PATH, MP4_PATH, GIF_PATH]) {
    const stats = fs.statSync(output);
    console.log(`${path.relative(ROOT, output)} ${humanSize(stats.size)}`);
  }
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
