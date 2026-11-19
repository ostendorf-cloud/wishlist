<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { GovueLegalPage, useLanguage } from '@ostendorf-cloud/govue'

// The privacy text of this site. It states what the code does today and
// nothing else: the access log of the server (internal/webapp/webapp.go,
// middleware.AccessLog with the client address, no user agent), the two
// browser storage entries of govue (useTheme and the language layer), no
// cookie, no resource from another host, and no sign in yet.
// Sections that must grow with later issues are listed in the pull request,
// never in this text. Both languages say the same.
const { locale } = useLanguage()
</script>

<template>
  <GovueLegalPage page="privacy" :skip-link="false">
    <div v-if="locale === 'de'" class="flex flex-col gap-4 text-text" lang="de">
      <h2 class="text-xl font-semibold">Verantwortlicher</h2>
      <p>
        Simon Ostendorf<br />
        Frankenstr. 8<br />
        52070 Aachen<br />
        E-Mail:
        <a class="text-accent-text hover:underline" href="mailto:info@my-wishes.de"
          >info@my-wishes.de</a
        >
      </p>

      <h2 class="text-xl font-semibold">Aufruf dieser Website</h2>
      <p>
        Bei jedem Aufruf schreibt der Server eine Zeile in sein Protokoll. Sie enthält den
        Zeitpunkt, die HTTP-Methode, das Muster der angefragten Route (nicht die vollständige
        Adresse mit ihren Parametern), den Statuscode, die Größe und die Dauer der Antwort sowie die
        IP-Adresse der Verbindung. Den User-Agent Ihres Browsers protokolliert der Server nicht.
      </p>

      <h2 class="text-xl font-semibold">Speicher in Ihrem Browser</h2>
      <p>
        Diese Website setzt keine Cookies und lädt keine Inhalte von anderen Websites nach. Wenn Sie
        den Farbmodus oder die Sprache umschalten, speichert die Seite diese Auswahl im lokalen
        Speicher Ihres Browsers, damit sie beim nächsten Besuch erhalten bleibt. Der Server liest
        diese Einträge nicht.
      </p>

      <h2 class="text-xl font-semibold">Anmeldung und Inhalte</h2>
      <p>
        Eine Anmeldung ist derzeit nicht möglich. Diese Website speichert deshalb derzeit keine
        Konten und keine Inhalte von Besuchern.
      </p>

      <h2 class="text-xl font-semibold">Ihre Rechte</h2>
      <p>
        Sie haben das Recht auf Auskunft über Ihre personenbezogenen Daten, auf Berichtigung, auf
        Löschung, auf Einschränkung der Verarbeitung, auf Datenübertragbarkeit und auf Widerspruch
        gegen die Verarbeitung. Schreiben Sie dazu an die oben genannte E-Mail-Adresse. Außerdem
        haben Sie das Recht, sich bei einer Datenschutzaufsichtsbehörde zu beschweren, etwa in dem
        Mitgliedstaat Ihres gewöhnlichen Aufenthaltsorts, Ihres Arbeitsplatzes oder des Orts des
        mutmaßlichen Verstoßes.
      </p>
    </div>

    <div v-else class="flex flex-col gap-4 text-text" lang="en">
      <p class="text-text-muted">
        The German text is the legally binding one.
        <a class="text-accent-text hover:underline" href="/de/privacy">View the German version</a>
      </p>

      <h2 class="text-xl font-semibold">Controller</h2>
      <p>
        Simon Ostendorf<br />
        Frankenstr. 8<br />
        52070 Aachen, Germany<br />
        Email:
        <a class="text-accent-text hover:underline" href="mailto:info@my-wishes.de"
          >info@my-wishes.de</a
        >
      </p>

      <h2 class="text-xl font-semibold">Visiting this website</h2>
      <p>
        On every request the server writes one line to its log. It holds the time, the HTTP method,
        the pattern of the requested route (not the full address with its parameters), the status
        code, the size and the duration of the response, and the IP address of the connection. The
        server does not log the user agent of your browser.
      </p>

      <h2 class="text-xl font-semibold">Storage in your browser</h2>
      <p>
        This website sets no cookies and loads no content from other websites. When you switch the
        color mode or the language, the page keeps that choice in the local storage of your browser,
        so it is still there on your next visit. The server does not read these entries.
      </p>

      <h2 class="text-xl font-semibold">Sign in and content</h2>
      <p>
        Signing in is not possible at the moment. For that reason this website currently stores no
        accounts and no content of visitors.
      </p>

      <h2 class="text-xl font-semibold">Your rights</h2>
      <p>
        You have the right of access to your personal data, to rectification, to erasure, to
        restriction of processing, to data portability and to object to processing. Write to the
        email address above to use them. You also have the right to lodge a complaint with a data
        protection supervisory authority, for example in the member state of your habitual
        residence, your place of work or the place of the alleged infringement.
      </p>
    </div>
  </GovueLegalPage>
</template>
