// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import {
  legalRouteDefs,
  legalRoutes,
  type LegalPages,
  type LocaleCode,
} from '@ostendorf-cloud/govue'
import type { Component } from 'vue'
import type { RouteRecordRaw } from 'vue-router'

import ImprintPage from './ImprintPage.vue'
import PrivacyPage from './PrivacyPage.vue'

/**
 * The origin of the site, for the canonical and hreflang links and the
 * sitemap of the prerendered pages. It is the domain of the contact address.
 */
export const ORIGIN = 'https://my-wishes.de'

/** The site has no terms: no terms page here, and Terms stays off in Go. */
export const pages: LegalPages<Component> = {
  imprint: ImprintPage,
  privacy: PrivacyPage,
}

/** The route table of the legal pages, for the prerender and the sitemap. */
export const legalRouteTable = legalRouteDefs(pages)

/** The same pages as vue-router records, so the bundle that loads on a legal page shows it. */
export const legalRouteRecords = legalRoutes(pages) as RouteRecordRaw[]

/** The language a legal path names, or undefined for any other path. */
export function localeOfPath(path: string): LocaleCode | undefined {
  const match = /^\/(de|en)\/(imprint|privacy)$/.exec(path)
  return match ? (match[1] as LocaleCode) : undefined
}
