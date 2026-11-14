// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { createApp } from 'vue'
import { useTheme } from '@ostendorf-cloud/govue'

import App from './App.vue'
import './styles.css'

// The theme never varies: this app ships exactly one brand, once chosen;
// see src/theme/wishlist.css. Mode starts at "system", the visitor's own
// choice only overrides it once a theme toggle is built.
useTheme({ theme: 'wishlist' })

createApp(App).mount('#app')
