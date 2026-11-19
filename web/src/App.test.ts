// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { useTheme } from '@ostendorf-cloud/govue'
import { screen, waitFor, within } from '@testing-library/vue'
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

    const group = within(screen.getByRole('banner')).getByRole('radiogroup')
    const light = within(group).getByRole('radio', { name: 'Light' })
    const dark = within(group).getByRole('radio', { name: 'Dark' })
    expect(within(group).getByRole('radio', { name: 'System' })).toBeChecked()

    await userEvent.click(light)
    await waitFor(() => expect(document.documentElement.dataset.mode).toBe('light'))
    expect(light).toBeChecked()

    await userEvent.click(dark)
    await waitFor(() => expect(document.documentElement.dataset.mode).toBe('dark'))
    expect(dark).toBeChecked()
  })

  it('uses govue theme switch in the header and keeps no stand in', () => {
    const sources = import.meta.glob<string>(['./**/*.vue', './**/*.ts', '!./**/*.test.ts'], {
      query: '?raw',
      import: 'default',
      eager: true,
    })

    expect(sources['./App.vue']).toMatch(/<GovueHeader\s+theme-switch/)
    expect(Object.keys(sources).filter((p) => /ThemeToggle/.test(p))).toEqual([])
    expect(Object.entries(sources).filter(([, text]) => /ThemeToggle/.test(text))).toEqual([])
  })
})
