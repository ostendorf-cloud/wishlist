<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { GovueLegalPage, useLanguage } from '@ostendorf-cloud/govue'

// The imprint of this site, written for it. Both languages say the same.
// The operator block (name, address, contact, VAT ID) is the one the other
// ostendorf-cloud sites carry. There is no section on consumer dispute
// resolution.
const { locale } = useLanguage()
</script>

<template>
  <GovueLegalPage page="imprint" back-href="/" :skip-link="false">
    <div v-if="locale === 'de'" class="flex flex-col gap-4 text-text" lang="de">
      <p>
        Simon Ostendorf<br />
        Frankenstr. 8<br />
        52070 Aachen
      </p>

      <h2 class="text-xl font-semibold">Kontakt</h2>
      <p>
        Telefon:
        <a class="text-accent-text hover:underline" href="tel:+4915730971784">+49 1573 0971784</a
        ><br />
        E-Mail:
        <a class="text-accent-text hover:underline" href="mailto:info@my-wishes.de"
          >info@my-wishes.de</a
        >
      </p>

      <h2 class="text-xl font-semibold">Umsatzsteuer-ID</h2>
      <p>Umsatzsteuer-Identifikationsnummer gemäß §27a Umsatzsteuergesetz: DE362164357</p>
    </div>

    <div v-else class="flex flex-col gap-4 text-text" lang="en">
      <p class="text-text-muted">
        The German text is the legally binding one.
        <a class="text-accent-text hover:underline" href="/de/imprint">View the German version</a>
      </p>

      <p>
        Simon Ostendorf<br />
        Frankenstr. 8<br />
        52070 Aachen, Germany
      </p>

      <h2 class="text-xl font-semibold">Contact</h2>
      <p>
        Phone:
        <a class="text-accent-text hover:underline" href="tel:+4915730971784">+49 1573 0971784</a
        ><br />
        Email:
        <a class="text-accent-text hover:underline" href="mailto:info@my-wishes.de"
          >info@my-wishes.de</a
        >
      </p>

      <h2 class="text-xl font-semibold">VAT ID</h2>
      <p>VAT identification number per Section 27a of the German VAT Act: DE362164357</p>
    </div>
  </GovueLegalPage>
</template>
