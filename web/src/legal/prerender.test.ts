// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'

import { files } from '../prerender'

const template = `<!doctype html>
<html lang="en" data-theme="wishlist">
  <head><meta charset="UTF-8" /><!--app-head--></head>
  <body><div id="app"><!--app-html--></div></body>
</html>`

describe('prerender of the legal pages', () => {
  it('writes the four pages and the sitemap, and nothing for terms', async () => {
    const out = await files(template)

    expect(out.map((f) => f.path).sort()).toEqual([
      'de/imprint/index.html',
      'de/privacy/index.html',
      'en/imprint/index.html',
      'en/privacy/index.html',
      'sitemap.xml',
    ])
  })

  it('gives every page its language and the exact footer links of that language', async () => {
    const out = await files(template)

    for (const file of out.filter((f) => f.path.endsWith('.html'))) {
      const code = file.path.slice(0, 2)
      expect(file.content).toContain(`<html lang="${code}"`)
      expect(file.content).toContain(`href="/${code}/imprint"`)
      expect(file.content).toContain(`href="/${code}/privacy"`)
      expect(file.content).toContain(
        `rel="canonical" href="https://my-wishes.de/${file.path.replace('/index.html', '')}"`,
      )
      expect(file.content).not.toContain('/terms')
      expect(file.content).not.toContain('/agb')
    }
  })

  it('lists the four pages in the sitemap on one origin', async () => {
    const sitemap = (await files(template)).find((f) => f.path === 'sitemap.xml')?.content ?? ''
    const locs = [...sitemap.matchAll(/<loc>([^<]+)<\/loc>/g)].map((m) => m[1])

    expect(locs.sort()).toEqual(
      ['de', 'en']
        .flatMap((c) => ['imprint', 'privacy'].map((p) => `https://my-wishes.de/${c}/${p}`))
        .sort(),
    )
  })
})
