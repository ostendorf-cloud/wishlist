// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { screen, waitFor } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { createAuth } from '../api/auth'
import { fakeApi, fakeAuth, renderApp } from '../testing'

describe('the route guard', () => {
  it('sends a visitor without a session to the sign in', async () => {
    const { router } = await renderApp(fakeApi(), '/', fakeAuth({ signedIn: false }))

    await waitFor(() => expect(router.currentRoute.value.name).toBe('login'))
    expect(await screen.findByRole('heading', { name: 'Sign in', level: 1 })).toBeVisible()
  })

  it('sends an unknown address of a visitor to the sign in too', async () => {
    const { router } = await renderApp(fakeApi(), '/nothing/here', fakeAuth({ signedIn: false }))

    await waitFor(() => expect(router.currentRoute.value.name).toBe('login'))
  })

  it('keeps a signed in owner off the sign in page', async () => {
    const { router } = await renderApp(fakeApi(), '/login')

    await waitFor(() => expect(router.currentRoute.value.name).toBe('dashboard'))
  })

  it('sends the owner to the sign in when the session ends under an open page', async () => {
    const { router, auth } = await renderApp(fakeApi(), '/')
    expect(router.currentRoute.value.name).toBe('dashboard')

    auth.state.signedIn = false

    await waitFor(() => expect(router.currentRoute.value.name).toBe('login'))
  })
})

describe('the sign in page', () => {
  it('posts the login, the password and the code to the account route', async () => {
    const auth = fakeAuth({ signedIn: false })
    auth.client.post.mockResolvedValue({ login: 'alice@example.invalid' })
    await renderApp(fakeApi(), '/login', auth)

    await userEvent.type(
      await screen.findByRole('textbox', { name: /Email|Login/i }),
      'alice@example.invalid',
    )
    await userEvent.type(screen.getByLabelText(/Password/i), 'correct horse battery staple')
    await userEvent.type(screen.getByLabelText(/Code/i), '123456')
    await userEvent.click(screen.getByRole('button', { name: /Sign in|Log in/i }))

    await waitFor(() => expect(auth.client.post).toHaveBeenCalled())
    expect(auth.client.post.mock.calls[0]?.[0]).toBe('/account/login')
    expect(auth.client.post.mock.calls[0]?.[1]).toMatchObject({
      login: 'alice@example.invalid',
      code: '123456',
    })
    expect(auth.refresh).toHaveBeenCalled()
  })

  it('links to registration only while it is open', async () => {
    await renderApp(fakeApi(), '/login', fakeAuth({ signedIn: false, registrationOpen: false }))
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeVisible()
    expect(screen.queryByRole('link', { name: 'Create an account' })).toBeNull()
  })
})

describe('the sign up page', () => {
  it('says so when registration is closed and shows no form', async () => {
    await renderApp(fakeApi(), '/signup', fakeAuth({ signedIn: false, registrationOpen: false }))

    expect(await screen.findByRole('heading', { name: 'Registration is closed' })).toBeVisible()
    expect(screen.queryByRole('button', { name: /Create|Sign up/i })).toBeNull()
  })
})

describe('sign out', () => {
  it('ends the session and shows the sign in', async () => {
    const auth = fakeAuth()
    const { router } = await renderApp(fakeApi(), '/', auth)

    await userEvent.click(await screen.findByRole('button', { name: 'Sign out' }))

    await waitFor(() => expect(router.currentRoute.value.name).toBe('login'))
    expect(auth.signOut).toHaveBeenCalledOnce()
  })

  it('stays signed in on screen when the server could not end the session', async () => {
    const auth = fakeAuth()
    auth.signOut.mockRejectedValue(new Error('network'))
    const { router } = await renderApp(fakeApi(), '/', auth)

    await userEvent.click(await screen.findByRole('button', { name: 'Sign out' }))

    expect(await screen.findByText('You could not be signed out')).toBeVisible()
    expect(router.currentRoute.value.name).toBe('dashboard')
  })
})

describe('the auth state', () => {
  function answer(body: unknown, status = 200): typeof fetch {
    return vi.fn().mockResolvedValue(
      new Response(JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
  }

  it('holds the CSRF token in memory and sends it on writes', async () => {
    const fetch = vi.fn().mockImplementation((url: string) =>
      Promise.resolve(
        new Response(
          url.endsWith('/account/session')
            ? JSON.stringify({
                signed_in: true,
                login: 'a@example.invalid',
                csrf_token: 'tok-1',
                registration_open: true,
              })
            : null,
          {
            status: url.endsWith('/account/session') ? 200 : 204,
            headers: { 'Content-Type': 'application/json' },
          },
        ),
      ),
    )
    const auth = createAuth({ fetch })
    await auth.refresh()
    expect(auth.state).toMatchObject({ loaded: true, signedIn: true, login: 'a@example.invalid' })

    await auth.signOut()

    const logout = fetch.mock.calls.find(([url]) => String(url).endsWith('/account/logout'))
    expect(new Headers(logout?.[1]?.headers).get('X-CSRF-Token')).toBe('tok-1')
    expect(auth.state.signedIn).toBe(false)
    expect(window.localStorage.length).toBe(0)
    expect(window.sessionStorage.length).toBe(0)
  })

  it('treats a failed answer as signed out', async () => {
    const auth = createAuth({ fetch: answer({ error: { code: 'internal', message: 'x' } }, 500) })
    await auth.refresh()
    expect(auth.state).toMatchObject({ loaded: true, signedIn: false })
  })

  it('treats a session without a token as signed out', async () => {
    const auth = createAuth({
      fetch: answer({ signed_in: true, login: 'a', registration_open: true }),
    })
    await auth.refresh()
    expect(auth.state.signedIn).toBe(false)
  })

  it('forgets the session when the server answers 401', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ signed_in: true, login: 'a', csrf_token: 't', registration_open: true }),
          {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          },
        ),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ error: { code: 'unauthenticated', message: 'x' } }), {
          status: 401,
          headers: { 'Content-Type': 'application/json' },
        }),
      )
    const auth = createAuth({ fetch })
    await auth.refresh()
    await expect(auth.client.get('/owner/lists')).rejects.toBeDefined()
    expect(auth.state.signedIn).toBe(false)
  })
})
