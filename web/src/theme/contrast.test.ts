// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/**
 * Proves every pair wishlist.css fills has to carry passes WCAG 2.2 AA, in
 * both modes.
 *
 * govue ships the exact same check as web/src/styles/contrast.ts, but its
 * package.json "exports" map only publishes ".", "./styles.css" and
 * "./themes/*.css": contrast.ts itself is not on the public surface a
 * consumer can import. This file is therefore the same algorithm and the
 * same pairs, read directly out of govue's source at the pinned version
 * this package depends on, so a change to either side is caught here
 * rather than two implementations quietly drifting apart.
 */
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const TEXT_MINIMUM = 4.5
const NON_TEXT_MINIMUM = 3

const TEXT_PAIRS: Array<[fg: string, bg: string]> = [
  ['text', 'bg'],
  ['text', 'bg-subtle'],
  ['text', 'surface'],
  ['text', 'surface-hover'],
  ['text-muted', 'bg'],
  ['text-muted', 'surface'],
  ['text-faint', 'bg'],
  ['text-faint', 'surface'],
  ['accent-text', 'bg'],
  ['accent-text', 'surface'],
  ['accent-text', 'accent-subtle'],
  ['accent-on', 'accent-strong'],
  ['accent-on', 'accent-strong-hover'],
  ['danger-on', 'danger-strong'],
  ['danger-on', 'danger-strong-hover'],
  ['success-text', 'surface'],
  ['success-text', 'success-subtle'],
  ['warning-text', 'surface'],
  ['warning-text', 'warning-subtle'],
  ['danger-text', 'surface'],
  ['danger-text', 'danger-subtle'],
  ['info-text', 'surface'],
  ['info-text', 'info-subtle'],
]

const NON_TEXT_PAIRS: Array<[fg: string, bg: string]> = [
  ['border-strong', 'bg'],
  ['border-strong', 'surface'],
  ['accent', 'bg'],
  ['accent', 'surface'],
  ['focus', 'bg'],
  ['focus', 'surface'],
  ['focus', 'surface-hover'],
]

type Palette = Record<string, string>

function tokens(css: string): { light: Palette; dark: Palette } {
  const light: Palette = {}
  const dark: Palette = {}
  const pattern =
    /--color-([\w-]+):\s*light-dark\(\s*(#[0-9a-fA-F]{6})\s*,\s*(#[0-9a-fA-F]{6})\s*\)/g
  for (const match of css.matchAll(pattern)) {
    const [, name, lightValue, darkValue] = match
    light[name] = lightValue
    dark[name] = darkValue
  }
  return { light, dark }
}

function channel(value: number): number {
  const srgb = value / 255
  return srgb <= 0.03928 ? srgb / 12.92 : ((srgb + 0.055) / 1.055) ** 2.4
}

function luminance(hex: string): number {
  const value = Number.parseInt(hex.slice(1), 16)
  const r = channel((value >> 16) & 0xff)
  const g = channel((value >> 8) & 0xff)
  const b = channel(value & 0xff)
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

function ratio(foreground: string, background: string): number {
  const a = luminance(foreground)
  const b = luminance(background)
  const [lighter, darker] = a > b ? [a, b] : [b, a]
  return (lighter + 0.05) / (darker + 0.05)
}

function round(value: number): number {
  return Math.round(value * 100) / 100
}

function failures(
  palette: Palette,
  pairs: Array<[fg: string, bg: string]>,
  minimum: number,
): string[] {
  return pairs.flatMap(([fg, bg]) => {
    const missing = [fg, bg].filter((token) => palette[token] === undefined)
    if (missing.length > 0) {
      return [`${fg} on ${bg} cannot be checked, ${missing.join(' and ')} is not defined`]
    }
    const value = ratio(palette[fg], palette[bg])
    return value < minimum ? [`${fg} on ${bg} is ${round(value)}:1, want ${minimum}:1`] : []
  })
}

// Resolved against the Vitest process's own working directory, which
// "task web:test" and "pnpm run test" both run from this package's root,
// rather than import.meta.url: Vitest's module runner does not always
// give that a real file: URL.
const themePath = join(process.cwd(), 'src/theme/wishlist.css')
const css = readFileSync(themePath, 'utf-8')
const { light, dark } = tokens(css)

describe('the wishlist theme', () => {
  it('declares every text pair at 4.5:1 in light mode', () => {
    expect(failures(light, TEXT_PAIRS, TEXT_MINIMUM)).toEqual([])
  })

  it('declares every non-text pair at 3:1 in light mode', () => {
    expect(failures(light, NON_TEXT_PAIRS, NON_TEXT_MINIMUM)).toEqual([])
  })

  it('declares every text pair at 4.5:1 in dark mode', () => {
    expect(failures(dark, TEXT_PAIRS, TEXT_MINIMUM)).toEqual([])
  })

  it('declares every non-text pair at 3:1 in dark mode', () => {
    expect(failures(dark, NON_TEXT_PAIRS, NON_TEXT_MINIMUM)).toEqual([])
  })
})
