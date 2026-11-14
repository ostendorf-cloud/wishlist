// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

import { render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'

import App from './App.vue'

describe('App', () => {
  it('renders the placeholder heading and lead', () => {
    render(App)

    expect(screen.getByRole('heading', { name: 'wishlist' })).toBeVisible()
    expect(
      screen.getByText('Shared wish lists with reservations the owner never sees.'),
    ).toBeVisible()
  })

  it('offers a skip link to the main landmark', () => {
    render(App)

    const link = screen.getByRole('link', { name: 'Skip to content' })
    expect(link).toHaveAttribute('href', '#main')
  })
})
