// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { createClient, type ApiClient } from '@ostendorf-cloud/govue'
import { inject, reactive, type InjectionKey } from 'vue'

/** The answer of GET /account/session. */
interface SessionInfo {
  signed_in: boolean
  login?: string
  csrf_token?: string
  registration_open: boolean
}

export interface AuthState {
  /** False until the first answer of the server. */
  loaded: boolean
  signedIn: boolean
  login: string
  registrationOpen: boolean
}

/**
 * Who is signed in. The session is the server's cookie, which the page cannot
 * read. The page holds only what the server tells it: the login and the CSRF
 * token of that session, in memory and never in storage.
 */
export interface Auth {
  readonly state: AuthState
  /** The client of the sign in forms and of every owner request. */
  readonly client: ApiClient
  /** Asks the server who is signed in. A failure counts as signed out. */
  refresh(): Promise<void>
  /** Ends the session on the server, then forgets it here. */
  signOut(): Promise<void>
}

export const authKey: InjectionKey<Auth> = Symbol('auth')

export function createAuth(options: { fetch?: typeof globalThis.fetch } = {}): Auth {
  const state = reactive<AuthState>({
    loaded: false,
    signedIn: false,
    login: '',
    registrationOpen: false,
  })
  let token: string | undefined

  function forget(): void {
    token = undefined
    state.signedIn = false
    state.login = ''
  }

  const client = createClient({
    baseUrl: '/api/v1',
    csrfToken: () => token,
    // The server says the session is gone: the router sends the page to /login.
    onUnauthenticated: forget,
    ...(options.fetch ? { fetch: options.fetch } : {}),
  })

  return {
    state,
    client,
    async refresh() {
      try {
        const info = await client.get<SessionInfo>('/account/session')
        token = info.signed_in ? info.csrf_token || undefined : undefined
        state.signedIn = info.signed_in && token !== undefined
        state.login = state.signedIn ? (info.login ?? '') : ''
        state.registrationOpen = info.registration_open
      } catch {
        // Fail closed: nothing proves a session, so nobody is signed in.
        forget()
      } finally {
        state.loaded = true
      }
    },
    async signOut() {
      await client.post('/account/logout')
      forget()
    },
  }
}

let shared: Auth | undefined

/** The auth of the running app, created once. */
export function sharedAuth(): Auth {
  return (shared ??= createAuth())
}

/** The auth a view uses: the one a parent provided, a fake in a test, else the real one. */
export function useAuth(): Auth {
  return inject(authKey, () => sharedAuth(), true)
}
