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

export function files(template: string): Promise<PrerenderFile[]> {
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
