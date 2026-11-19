// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { ErrorCode } from '@ostendorf-cloud/govue'
import { screen, waitFor } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { aList, apiError, fakeApi, renderApp } from '../testing'

describe('DashboardView', () => {
  it('shows each list with its wish count and badges', async () => {
    const api = fakeApi({
      lists: () =>
        Promise.resolve({
          lists: [
            aList({ id: 1, title: 'Birthday', wish_count: 1, shared: true }),
            aList({ id: 2, title: 'Christmas', wish_count: 3, archived: true, event_date: null }),
          ],
        }),
    })
    await renderApp(api)

    expect(await screen.findByRole('heading', { name: 'Birthday' })).toBeVisible()
    expect(screen.getByText('1 wish')).toBeVisible()
    expect(screen.getByText('3 wishes')).toBeVisible()
    expect(screen.getByText('Shared')).toBeVisible()
    expect(screen.getByText('Archived')).toBeVisible()
    expect(screen.getByText('On May 1, 2030')).toBeVisible()
  })

  it('opens a list', async () => {
    const { router } = await renderApp(fakeApi())
    await userEvent.click(await screen.findByRole('button', { name: 'Open Birthday' }))

    await waitFor(() => expect(router.currentRoute.value.fullPath).toBe('/lists/1'))
  })

  it('asks for archived lists only when the filter is on', async () => {
    const api = fakeApi()
    await renderApp(api)
    await screen.findByRole('heading', { name: 'Birthday' })
    expect(api.lists).toHaveBeenLastCalledWith({ archived: false })

    await userEvent.click(screen.getByRole('checkbox', { name: 'Show archived lists' }))

    await waitFor(() => expect(api.lists).toHaveBeenLastCalledWith({ archived: true }))
  })

  it('offers to create the first list when there is none', async () => {
    const { router } = await renderApp(fakeApi({ lists: () => Promise.resolve({ lists: [] }) }))

    expect(await screen.findByText('No lists yet')).toBeVisible()
    const buttons = screen.getAllByRole('button', { name: 'New list' })
    await userEvent.click(buttons[buttons.length - 1])
    await waitFor(() => expect(router.currentRoute.value.fullPath).toBe('/lists/new'))
  })

  it('shows an error with the request id and loads again on retry', async () => {
    let calls = 0
    const api = fakeApi({
      lists: () => {
        calls++
        return calls === 1
          ? Promise.reject(apiError(ErrorCode.internal, 500))
          : Promise.resolve({ lists: [aList()] })
      },
    })
    await renderApp(api)

    expect(await screen.findByText('This could not be loaded')).toBeVisible()
    expect(screen.getByText('req-1')).toBeVisible()
    await userEvent.click(screen.getByRole('button', { name: 'Try again' }))

    expect(await screen.findByRole('heading', { name: 'Birthday' })).toBeVisible()
  })

  it('says so when nobody is signed in', async () => {
    const api = fakeApi({
      lists: () => Promise.reject(apiError(ErrorCode.unauthenticated, 401)),
    })
    await renderApp(api)

    expect(await screen.findByText('You are not signed in')).toBeVisible()
  })
})
