#!/usr/bin/env node
/**
 * gen-logo.mjs — logo candidates via OpenAI gpt-image-2.
 *
 *   OPENAI_API_KEY=... node scripts/gen-logo.mjs --name Fundus --out design/logo [--n 4] [--variant mark|icon|wordmark] [--quality high]
 *
 * Writes PNGs with a transparent background (mark, icon) or on paper (wordmark).
 * Prompts encode the product's visual language: calm, editorial, warm paper and
 * ink with one ochre accent, no gradients, no 3D, no clutter.
 */
import fs from "node:fs";
import path from "node:path";

const argv = process.argv.slice(2);
const arg = (k, d) => { const i = argv.indexOf(k); return i !== -1 && argv[i + 1] ? argv[i + 1] : d; };
const NAME = arg("--name", "Fundus");
const OUT = path.resolve(arg("--out", "design/logo"));
const N = parseInt(arg("--n", "4"), 10);
const VARIANT = arg("--variant", "mark");
const QUALITY = arg("--quality", "high");
const KEY = process.env.OPENAI_API_KEY;
if (!KEY) { console.error("OPENAI_API_KEY is required"); process.exit(1); }
fs.mkdirSync(OUT, { recursive: true });

const PALETTE = "warm off-white paper (#f6f2ea), deep ink (#1f1d1a), a single ochre accent (#b8641c)";
const RULES = " Flat vector style, crisp edges, no gradients, no 3D, no shadows, no photorealism, no clutter, no extra decoration, no watermark. Centered, generous margins, must read clearly at 32 pixels.";

const CONCEPTS = {
  Fundus: "an abstract stack of layered sheets settling into a rounded vessel, geometric strata, calm",
};
const concept = CONCEPTS[NAME] || `an abstract emblem that evokes collecting and keeping thoughts safely, related to the name "${NAME}"`;

const PROMPTS = {
  mark: `Minimal logo mark for a personal knowledge app named "${NAME}": ${concept}. Two colors only: ${PALETTE} — ink for the shape, one small ochre detail. Transparent background. Absolutely NO text, NO letters.${RULES}`,
  icon: `App icon for a personal knowledge app named "${NAME}": ${concept}, drawn in ink on a warm off-white paper rounded square, one small ochre accent. Palette: ${PALETTE}. The rounded-square tile fills the canvas. NO text, NO letters.${RULES}`,
  wordmark: `Wordmark logo: the word "${NAME}" set in an elegant editorial serif with slightly optical, warm letterforms, ink on warm off-white paper, with a tiny ochre accent (a dot, a bookmark tick or a bird) integrated into one letter. Correctly spelled exactly "${NAME}", nothing else written. Palette: ${PALETTE}.${RULES}`,
};
const prompt = PROMPTS[VARIANT] || PROMPTS.mark;

async function generate(i) {
  const body = {
    model: "gpt-image-2",
    prompt,
    size: "1024x1024",
    quality: QUALITY,
    output_format: "png",
    n: 1,
  };
  if (VARIANT !== "wordmark") body.background = "transparent";
  const res = await fetch("https://api.openai.com/v1/images/generations", {
    method: "POST",
    headers: { Authorization: `Bearer ${KEY}`, "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`HTTP ${res.status}: ${(await res.text()).slice(0, 400)}`);
  const json = await res.json();
  const b64 = json.data?.[0]?.b64_json;
  if (!b64) throw new Error("no image in response");
  const file = path.join(OUT, `${NAME.toLowerCase()}-${VARIANT}-${i + 1}.png`);
  fs.writeFileSync(file, Buffer.from(b64, "base64"));
  console.log("wrote", file);
}

console.log(`→ ${N} × ${VARIANT} for "${NAME}" (gpt-image-2, ${QUALITY})`);
for (let i = 0; i < N; i++) {
  try { await generate(i); } catch (e) { console.error(`  #${i + 1} failed:`, e.message); }
}
