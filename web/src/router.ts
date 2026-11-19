// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import {
  createMemoryHistory,
  createRouter,
  createWebHistory,
  type RouteRecordRaw,
  type Router,
  type RouterHistory,
} from 'vue-router'

import DashboardView from './views/DashboardView.vue'
import ListFormView from './views/ListFormView.vue'
import ListView from './views/ListView.vue'
import WishFormView from './views/WishFormView.vue'

export const routes: RouteRecordRaw[] = [
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
  // Anything else is the dashboard: a bad address never leaves a blank page.
  { path: '/:rest(.*)*', redirect: { name: 'dashboard' } },
]

export function createAppRouter(history: RouterHistory = createWebHistory()): Router {
  return createRouter({ history, routes })
}

/** A router that needs no browser, for tests. The test pushes its first route. */
export function createTestRouter(): Router {
  return createAppRouter(createMemoryHistory())
}
