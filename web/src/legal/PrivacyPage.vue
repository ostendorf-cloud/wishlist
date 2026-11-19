<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { GovueLegalPage, useLanguage } from '@ostendorf-cloud/govue'

// The privacy text of this site. It states what the code does today and
// nothing else: the access log of the server (internal/webapp/webapp.go,
// middleware.AccessLog with the client address, no user agent), the two
// browser storage entries of govue (useTheme and the language layer), no
// resource from another host, and the owner account: login, Argon2id hash,
// TOTP secret and counter in owner_credentials (migration 0002,
// internal/store/account.go), the deletion of unconfirmed registrations after
// 24 hours at the next registration, the cookie __Host-session and its
// lifetimes (govue session defaults, internal/auth), the CSRF token held in
// memory by web/src/api/auth.ts, and the rate limiters of internal/auth and
// internal/webapp. Change a number here when its constant changes.
// Sections that must grow with later issues are listed in the pull request,
// never in this text. Both languages say the same.
const { locale } = useLanguage()
</script>

<template>
  <GovueLegalPage page="privacy" back-href="/" :skip-link="false">
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
        IP-Adresse der Verbindung sowie eine zufällig erzeugte Kennung der Anfrage. Den User-Agent
        Ihres Browsers protokolliert der Server nicht.
      </p>
      <p>
        Für Anfragen an die Programmierschnittstelle unter /api begrenzt der Server die Anzahl der
        Anfragen je Adresse. Dazu hält er die Adressen der Verbindungen, bis zu 100000 gleichzeitig,
        nur im Arbeitsspeicher.
      </p>
      <p>
        Wenn Sie /imprint oder /privacy aufrufen, leitet der Server Sie auf die deutsche oder die
        englische Seite weiter. Dafür liest er die Kopfzeile Accept-Language Ihres Browsers und,
        falls vorhanden, ein Cookie mit dem Namen govue-language. Diese Website setzt dieses Cookie
        nicht selbst. Der Server speichert dabei nichts.
      </p>

      <h2 class="text-xl font-semibold">Speicher in Ihrem Browser</h2>
      <p>
        Diese Website lädt keine Inhalte von anderen Websites nach. Sie setzt ein einziges Cookie,
        und zwar nur, wenn Sie sich anmelden (siehe unten). Wenn Sie den Farbmodus oder die Sprache
        umschalten, speichert die Seite diese Auswahl im lokalen Speicher Ihres Browsers, damit sie
        beim nächsten Besuch erhalten bleibt. Der Server liest diese Einträge nicht.
      </p>

      <h2 class="text-xl font-semibold">Konto und Anmeldung</h2>
      <p>
        Wer Listen verwalten möchte, kann ein Konto als Eigentümer anlegen, solange die
        Registrierung geöffnet ist. Sie kann geschlossen sein. Als Anmeldename fragt das Formular
        nach Ihrer E-Mail-Adresse. Der Server prüft das Format nicht. Das Konto ist durch ein
        Passwort und durch einen Code aus einer Authenticator-App (TOTP) gesichert. Ohne Konto ist
        keine Anmeldung möglich.
      </p>
      <p>
        Der Server speichert in der Datenbank: den Anmeldenamen so, wie Sie ihn eingeben, und ein
        zweites Mal in Kleinbuchstaben, damit eine Adresse nicht zwei Konten ergibt, das Passwort
        nur als Argon2id-Hash und nie im Klartext, das TOTP-Geheimnis, einen Zähler des zuletzt
        verwendeten Codes, der die erneute Verwendung eines Codes verhindert, und den Zeitpunkt der
        Anlage. Dazu kommen eine interne Nummer des Kontos, eine zufällige Kennung und eine
        Markierung, mit der ein Konto gesperrt werden kann. Die Markierung setzt derzeit nichts. Das
        TOTP-Geheimnis liegt in der Datenbank unverschlüsselt.
      </p>
      <p>
        Eine Registrierung gilt erst mit dem ersten gültigen Code als bestätigt. Bei der nächsten
        Registrierung löscht der Server automatisch Konten, die nie bestätigt wurden, älter als 24
        Stunden sind und zu denen keine Liste gehört. Bis dahin kann ein solches Konto länger als 24
        Stunden bestehen.
      </p>
      <p>
        Wenn Sie sich anmelden oder Ihre Registrierung bestätigen, setzt der Server das Cookie
        __Host-session. Es ist technisch notwendig, damit Sie angemeldet bleiben. Es enthält nur
        eine Kennung, ist für Skripte der Seite nicht lesbar (HttpOnly), wird nur über HTTPS
        gesendet (Secure), gilt nur für diese Domain und hat SameSite Lax. Es hat kein Ablaufdatum
        und Ihr Browser löscht es beim Schließen. Der Server beendet die Sitzung nach 2 Stunden ohne
        Nutzung, spätestens nach 12 Stunden. Die Sitzung mit der Kennung, der Kontonummer und dem
        Anmeldenamen liegt nur im Arbeitsspeicher des Servers. Beim Abmelden entfernt der Server sie
        und das Cookie. Ein Neustart des Servers beendet alle Sitzungen.
      </p>
      <p>
        Die Seite hält ein CSRF-Token, das der Server für Ihre Sitzung erzeugt, nur im
        Arbeitsspeicher. Es wird nicht im Browser gespeichert.
      </p>
      <p>
        Für die Routen zum Registrieren, Bestätigen und Anmelden begrenzt der Server die Versuche
        mehrfach: je Adresse (5 pro Minute), je Anmeldename und Adresse (10 in 15 Minuten) und je
        Anmeldename allein (100 pro Stunde). Er hält dafür die Adressen der Verbindungen und die
        Anmeldenamen, diese nur als SHA-256-Wert, bis zu 100000 Einträge je Zähler, nur im
        Arbeitsspeicher. Ein Eintrag wird bei der nächsten Anfrage entfernt, nachdem sein Zähler
        wieder voll aufgefüllt ist (nach etwa einer Minute, 15 Minuten oder einer Stunde, je nach
        Zähler), oder bei einem Neustart des Servers. Der Server räumt nicht von sich aus auf,
        sondern nur, wenn eine Anfrage eintrifft.
      </p>
      <p>
        Wer kein Konto hat, gibt auf dieser Website keine Inhalte ein, und es werden keine von ihm
        gespeichert.
      </p>

      <h2 class="text-xl font-semibold">Inhalte Ihres Kontos</h2>
      <p>
        Mit einem Konto können Sie Listen und Wünsche anlegen. Der Server speichert sie in der
        Datenbank, bis Sie sie löschen. Zu einer Liste gehören: Titel, Anlass, Datum des
        Ereignisses, Begrüßungstext, die Markierung, ob sie archiviert ist, sowie der Zeitpunkt der
        Anlage und der letzten Änderung. Zu einem Wunsch gehören: Titel, Beschreibung,
        Internetadresse, Geschäft, Preis mit Währung, Priorität, Anzahl, die Markierung, ob es ein
        gemeinsames Geschenk ist, die Reihenfolge in der Liste, die Markierung, ob er archiviert
        ist, sowie der Zeitpunkt der Anlage und der letzten Änderung. Diese Angaben machen Sie
        selbst.
      </p>
      <p>
        Die Zeile Ihres Kontos in der Datenbank enthält außerdem einen Benutzernamen der Form
        owner-Nummer, der ohne Ihre Anmeldedaten vergeben wird, und den Zeitpunkt der letzten
        Nutzung. Die Sitzung im Arbeitsspeicher enthält zusätzlich den Zeitpunkt ihrer Ausstellung,
        der letzten Nutzung und ihres Ablaufs.
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
        code, the size and the duration of the response, the IP address of the connection and a
        randomly generated id of the request. The server does not log the user agent of your
        browser.
      </p>
      <p>
        For requests to the programming interface under /api, the server limits the number of
        requests per address. For that it keeps the addresses of the connections, up to 100000 at a
        time, in memory only.
      </p>
      <p>
        When you open /imprint or /privacy, the server redirects you to the German or the English
        page. For that it reads the Accept-Language header of your browser and, if present, a cookie
        named govue-language. This website does not set that cookie itself. The server stores
        nothing in doing so.
      </p>

      <h2 class="text-xl font-semibold">Storage in your browser</h2>
      <p>
        This website loads no content from other websites. It sets a single cookie, and only when
        you sign in (see below). When you switch the color mode or the language, the page keeps that
        choice in the local storage of your browser, so it is still there on your next visit. The
        server does not read these entries.
      </p>

      <h2 class="text-xl font-semibold">Account and sign in</h2>
      <p>
        Anyone who wants to manage lists can create an owner account while registration is open.
        Registration can be closed. As the login, the form asks for your email address. The server
        does not check the format. The account is secured by a password and by a code from an
        authenticator app (TOTP). Without an account, signing in is not possible.
      </p>
      <p>
        The server stores in the database: the login as you enter it and a second time in lower
        case, so that one address cannot become two accounts, the password only as an Argon2id hash
        and never in plain text, the TOTP secret, a counter of the code used last, which prevents a
        code from being used again, and the time of creation. It also stores an internal number of
        the account, a random identifier and a flag that can block an account. Nothing sets that
        flag at the moment. The TOTP secret is stored unencrypted in the database.
      </p>
      <p>
        A registration counts as confirmed with the first valid code. At the next registration, the
        server automatically deletes accounts that were never confirmed, are older than 24 hours and
        have no list. Until then, such an account can exist for longer than 24 hours.
      </p>
      <p>
        When you sign in or confirm your registration, the server sets the cookie __Host-session. It
        is strictly necessary to keep you signed in. It holds only an identifier, cannot be read by
        scripts of the page (HttpOnly), is sent over HTTPS only (Secure), applies to this domain
        only and has SameSite Lax. It has no expiry date and your browser deletes it when it closes.
        The server ends the session after 2 hours without use and after 12 hours at the latest. The
        session with the identifier, the account number and the login is held in the memory of the
        server only. At sign out the server removes it and the cookie. A restart of the server ends
        all sessions.
      </p>
      <p>
        The page holds a CSRF token that the server creates for your session in memory only. It is
        not stored in the browser.
      </p>
      <p>
        For the routes to register, confirm and sign in, the server limits attempts in several ways:
        per address (5 per minute), per login and address (10 per 15 minutes) and per login alone
        (100 per hour). For that it keeps the addresses of the connections and the logins, the
        latter only as a SHA-256 value, up to 100000 entries per counter, in memory only. An entry
        is removed at the next request after its counter has refilled completely (after about one
        minute, 15 minutes or one hour, depending on the counter), or at a restart of the server.
        The server does not tidy up on its own, only when a request arrives.
      </p>
      <p>
        Anyone without an account enters no content on this website, and none is stored from them.
      </p>

      <h2 class="text-xl font-semibold">Content of your account</h2>
      <p>
        With an account you can create lists and wishes. The server stores them in the database
        until you delete them. A list holds: title, occasion, date of the event, greeting, the flag
        whether it is archived, and the time of creation and of the last change. A wish holds:
        title, description, web address, shop, price with currency, priority, quantity, the flag
        whether it is a group gift, the order in the list, the flag whether it is archived, and the
        time of creation and of the last change. You enter these details yourself.
      </p>
      <p>
        The row of your account in the database also holds a username of the form owner-number,
        which is assigned without your sign in details, and the time of the last use. The session in
        memory also holds the time it was issued, the time of last use and the time it expires.
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
