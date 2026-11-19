// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/**
 * The SSR entry of the legal pages, the shape govue-prerender --out-dir takes:
 * it exports files(template). Only the legal pages are prerendered, one per
 * language; the owner's pages stay client side. The same App renders as in the
 * browser, so a legal page has the header and the footer of every page.
 */

import { createGovueI18n, useTheme } from '@ostendorf-cloud/govue'
import { localizedFiles, render, type PrerenderFile } from '@ostendorf-cloud/govue/server'
import { createMemoryHistory } from 'vue-router'

import App from './App.vue'
import { legalRouteTable, ORIGIN } from './legal/site'
import { createAppRouter } from './router'

// The state main.ts starts, so the header renders as the browser's first paint.
useTheme({ theme: 'wishlist' })

// The title of each page (WCAG 2.4.2). localizedFiles takes one template for
// every page, so the template's <title> is replaced in its output.
const titles: Record<string, string> = {
  'de/imprint': 'Impressum | Wishlist',
  'de/privacy': 'Datenschutzerklärung | Wishlist',
  'en/imprint': 'Imprint | Wishlist',
  'en/privacy': 'Privacy policy | Wishlist',
}

export async function files(template: string): Promise<PrerenderFile[]> {
  const out = await build(template)
  return out.map((file) => {
    const title = titles[file.path.replace(/\/index\.html$/, '')]
    if (title === undefined) return file
    if (!/<title>[^<]*<\/title>/.test(file.content)) {
      throw new Error(`prerender: ${file.path} has no <title> to replace`)
    }
    return {
      ...file,
      content: file.content.replace(/<title>[^<]*<\/title>/, `<title>${title}</title>`),
    }
  })
}

function build(template: string): Promise<PrerenderFile[]> {
  return localizedFiles(legalRouteTable, {
    origin: ORIGIN,
    template,
    render: (variant) =>
      render(App, {
        setup: async (app) => {
          const router = createAppRouter(createMemoryHistory())
          app.use(router)
          app.use(createGovueI18n({ locale: variant.locale }))
          await router.push(variant.path)
          await router.isReady()
        },
      }),
  })
}
