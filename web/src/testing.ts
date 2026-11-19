// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Helpers for tests only. Nothing in the app imports this file.

import { render } from '@testing-library/vue'
import { ApiError, ErrorCode } from '@ostendorf-cloud/govue'
import { vi } from 'vitest'

import { ownerApiKey, type OwnerApi } from './api/owner'
import type { List, Wish } from './api/types'
import App from './App.vue'
import { createTestRouter } from './router'

export function aList(overrides: Partial<List> = {}): List {
  return {
    id: 1,
    title: 'Birthday',
    occasion: 'Turning 40',
    greeting: '',
    event_date: '2030-05-01',
    reserve_open: true,
    archived: false,
    shared: false,
    wish_count: 2,
    created_at: '2030-01-01T00:00:00Z',
    updated_at: '2030-01-01T00:00:00Z',
    ...overrides,
  }
}

export function aWish(overrides: Partial<Wish> = {}): Wish {
  return {
    id: 10,
    list_id: 1,
    title: 'Book',
    description: '',
    url: '',
    shop: '',
    price_cents: null,
    currency: 'EUR',
    priority: 1,
    quantity: 1,
    group_gift: false,
    position: 0,
    archived: false,
    created_at: '2030-01-01T00:00:00Z',
    updated_at: '2030-01-01T00:00:00Z',
    ...overrides,
  }
}

/** A fake API whose every call is a spy that resolves to a sensible default. */
export function fakeApi(
  overrides: Partial<OwnerApi> = {},
): { [K in keyof OwnerApi]: ReturnType<typeof vi.fn> } & OwnerApi {
  const api = {
    lists: vi.fn().mockResolvedValue({ lists: [aList()] }),
    list: vi.fn().mockResolvedValue({ list: aList(), wishes: [aWish()] }),
    createList: vi.fn().mockResolvedValue(aList({ id: 5 })),
    updateList: vi.fn().mockResolvedValue(aList()),
    archiveList: vi.fn().mockResolvedValue(aList({ archived: true })),
    deleteList: vi.fn().mockResolvedValue(undefined),
    wish: vi.fn().mockResolvedValue(aWish()),
    createWish: vi.fn().mockResolvedValue(aWish({ id: 11 })),
    updateWish: vi.fn().mockResolvedValue(aWish()),
    archiveWish: vi.fn().mockResolvedValue(aWish({ archived: true })),
    deleteWish: vi.fn().mockResolvedValue(undefined),
    moveWish: vi.fn().mockResolvedValue(undefined),
    relocateWish: vi.fn().mockResolvedValue(aWish({ list_id: 2 })),
  }
  // An override is a spy too, so a test can assert on the call it replaced.
  const target: Record<string, unknown> = api
  for (const [name, impl] of Object.entries(overrides)) {
    target[name] = vi.fn(impl)
  }
  return api
}

/** An error the way createClient throws it. */
export function apiError(
  code: string,
  status: number,
  details: Record<string, string> = {},
): ApiError {
  return new ApiError({ code, message: 'server text', status, details, requestId: 'req-1' })
}

export const notFound = (): ApiError => apiError(ErrorCode.notFound, 404)

/** Renders the whole app at a route with a fake API. */
export async function renderApp(api: OwnerApi, route = '/') {
  const router = createTestRouter()
  await router.push(route)
  const result = render(App, { global: { plugins: [router], provide: { [ownerApiKey]: api } } })
  await router.isReady()
  return { router, ...result }
}
