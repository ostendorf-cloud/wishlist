// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { watch } from 'vue'
import {
  createMemoryHistory,
  createRouter,
  createWebHistory,
  type RouteRecordRaw,
  type Router,
  type RouterHistory,
} from 'vue-router'

import { sharedAuth, type Auth } from './api/auth'
import { legalRouteRecords } from './legal/site'
import DashboardView from './views/DashboardView.vue'
import ListFormView from './views/ListFormView.vue'
import ListView from './views/ListView.vue'
import LoginView from './views/LoginView.vue'
import SignupView from './views/SignupView.vue'
import WishFormView from './views/WishFormView.vue'

export const routes: RouteRecordRaw[] = [
  { path: '/login', name: 'login', component: LoginView, meta: { public: true } },
  { path: '/signup', name: 'signup', component: SignupView, meta: { public: true } },
  { path: '/', name: 'dashboard', component: DashboardView },
  { path: '/lists/new', name: 'list-new', component: ListFormView },
  { path: '/lists/:id(\\d+)', name: 'list', component: ListView, props: true },
  { path: '/lists/:id(\\d+)/edit', name: 'list-edit', component: ListFormView, props: true },
  {
    path: '/lists/:listId(\\d+)/wishes/new',
    name: 'wish-new',
    component: WishFormView,
    props: true,
  },
  { path: '/wishes/:id(\\d+)/edit', name: 'wish-edit', component: WishFormView, props: true },
  // /de/imprint, /en/imprint, /de/privacy and /en/privacy. The server serves them
  // as prerendered files, and the bundle that loads on one has to show it too.
  // The paths come from govue, so they are not literals here: internal/webapp
  // matches them with exact patterns, not through spaRoutes.
  ...legalRouteRecords,
  // Anything else is the dashboard: a bad address never leaves a blank page.
  { path: '/:rest(.*)*', redirect: { name: 'dashboard' } },
]

export function createAppRouter(
  history: RouterHistory = createWebHistory(),
  auth: Auth = sharedAuth(),
): Router {
  const router = createRouter({ history, routes })

  // Every route except the two public ones needs a session. The server is the
  // authority and answers 401 anyway: this only saves the owner a failed page.
  router.beforeEach(async (to) => {
    if (!auth.state.loaded) {
      await auth.refresh()
    }
    if (to.meta.public) {
      return auth.state.signedIn ? { name: 'dashboard' } : true
    }
    return auth.state.signedIn ? true : { name: 'login' }
  })

  // A session that ends while a page is open (a 401, a sign out elsewhere)
  // sends the owner to the sign in once.
  watch(
    () => auth.state.signedIn,
    (signedIn) => {
      if (!signedIn && !router.currentRoute.value.meta.public) {
        void router.push({ name: 'login' })
      }
    },
  )
  return router
}

/** A router that needs no browser, for tests. The test pushes its first route. */
export function createTestRouter(auth: Auth): Router {
  return createAppRouter(createMemoryHistory(), auth)
}
