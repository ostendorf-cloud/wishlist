// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { createApp } from 'vue'
import { createGovueI18n, useTheme } from '@ostendorf-cloud/govue'

import App from './App.vue'
import { localeOfPath } from './legal/site'
import { createAppRouter } from './router'
import './styles.css'

// The theme never varies: this app ships exactly one brand, once chosen;
// see src/theme/wishlist.css. Mode starts at "system" and the header's
// switch lets the visitor override it.
useTheme({ theme: 'wishlist' })

// The language layer serves govue's own texts: the footer and the legal pages.
// A legal page is a URL per language, so the path decides there. Any other
// page follows the stored choice, then the browser, then German.
createApp(App)
  .use(createAppRouter())
  .use(createGovueI18n({ locale: localeOfPath(window.location.pathname) }))
  .mount('#app')
