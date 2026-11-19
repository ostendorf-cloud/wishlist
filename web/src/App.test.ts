// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { useTheme } from '@ostendorf-cloud/govue'
import { screen, waitFor } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { fakeApi, renderApp } from './testing'

// main.ts starts the theme once, outside any component, so its watcher lives as
// long as the page. A test does the same, or the first component to call
// useTheme would own the watcher and take it down when it unmounts.
useTheme({ theme: 'wishlist' })

describe('App', () => {
  it('has a banner, one main landmark and the dashboard as the start page', async () => {
    await renderApp(fakeApi())

    expect(screen.getByRole('banner')).toBeVisible()
    expect(await screen.findByRole('heading', { name: 'Your lists', level: 1 })).toBeVisible()
    expect(screen.getAllByRole('main')).toHaveLength(1)
  })

  it('offers a skip link to the main landmark', async () => {
    await renderApp(fakeApi())

    expect(screen.getByRole('link', { name: 'Skip to content' })).toHaveAttribute('href', '#main')
  })

  it('sends an unknown address to the dashboard', async () => {
    const { router } = await renderApp(fakeApi(), '/nothing/here')

    expect(router.currentRoute.value.name).toBe('dashboard')
  })

  it('switches the color mode from the header and keeps it on the page element', async () => {
    await renderApp(fakeApi())

    const toggle = screen.getByRole('button', { name: /Color mode/ })
    expect(toggle).toHaveAccessibleName('Color mode: System')

    await userEvent.click(toggle)
    await waitFor(() => expect(document.documentElement.dataset.mode).toBe('light'))
    expect(toggle).toHaveAccessibleName('Color mode: Light')

    await userEvent.click(toggle)
    await waitFor(() => expect(document.documentElement.dataset.mode).toBe('dark'))
  })
})
