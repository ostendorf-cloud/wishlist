// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { createApp } from 'vue'
import { useTheme } from '@ostendorf-cloud/govue'

import App from './App.vue'
import { createAppRouter } from './router'
import './styles.css'

// The theme never varies: this app ships exactly one brand, once chosen;
// see src/theme/wishlist.css. Mode starts at "system" and the header's
// switch lets the visitor override it.
useTheme({ theme: 'wishlist' })

createApp(App).use(createAppRouter()).mount('#app')
