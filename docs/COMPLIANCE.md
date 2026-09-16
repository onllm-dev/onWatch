# Compliance guide for onWatch deployers

**This is not legal advice.** It is an engineering document. It describes what
onWatch actually does with personal data so that you - the person or
organisation installing it - can fill in your own compliance paperwork with
facts instead of guesses. Where a decision is yours (a lawful basis, a retention
period, a grievance contact) this document marks it as yours and does not decide
it for you. Take your own legal advice before you rely on any of it.

The onWatch project cannot do any of this for you. It never receives your data.
There is no analytics, no telemetry and no vendor backend. Everything below is
about your deployment, on your machine.

**Companion documents:**

- `docs/PRIVACY.md` - the field-level inventory: every table, column, file and
  in-memory store, and what is in it. This document does not repeat it.
- `SECURITY.md` - security posture, known hardening gaps, breach detection and
  the notification deadlines.

---

## 1. Do these obligations even apply to me?

Start here, because for a large share of onWatch users the honest answer is
"no", and reading the rest would be wasted effort.

### The individual self-hoster: largely out of scope

If you install onWatch on your own machine to watch your own AI subscriptions,
you are processing your own data for your own purposes. Both regimes carve this
out:

- **GDPR Art. 2(2)(c)** - the Regulation does not apply to processing by a
  natural person in the course of a purely personal or household activity.
- **DPDP Act 2023, s.3(c)(ii)** - the Act does not apply to personal data
  processed by an individual for any personal or domestic purpose.

You have no controller obligations, no record of processing, no notice to
publish, no DSR runbook. You still have a security problem - `onwatch.db` holds
live provider credentials in cleartext (see `SECURITY.md`, gap 10) - but that is
self-protection, not compliance.

### The trigger that changes everything

**The moment onWatch is deployed by an organisation and watches an account
belonging to an identifiable person other than the deployer, the household
exemption is gone.**

Concretely, any one of these is enough:

| Situation | Why it is in scope |
|-----------|--------------------|
| An employer runs onWatch and one of the tracked accounts is an employee's | The employee is a **data subject** (GDPR) / **Data Principal** (DPDP). Their account identifier, email, and a timestamped history of their AI usage are being processed by the employer |
| A team shares one dashboard and each member's provider account is a separate row | Every member is a data subject. Per-account quota data is behavioural data about an identifiable individual |
| A contractor or agency runs onWatch on a client's behalf | Two-party processing - see roles below |
| onWatch tracks a shared service account, but your logs tie usage to a named person | The linkage makes it personal data even though the account is nominally shared |
| A freelancer tracks only their own accounts, but bills a client from the data | Still your own data. Household exemption is likely intact for the processing; your invoicing is a separate question |

Two points people get wrong:

1. **"It is only quota numbers, not personal data."** No. In a single-operator
   or per-account deployment, every utilisation percentage, reset cycle and
   per-model request count is behavioural data about one identifiable person,
   and the `api_integration_usage_events` rows are a per-request log of that
   person's AI usage. Pseudonymous is not anonymous. Treat the whole database as
   personal data, not just the columns that hold an email address.
2. **"It is self-hosted, so nothing leaves."** Nothing reaches the onWatch
   project. Things do leave: each provider's API (on every poll),
   `api.github.com` (version check, if enabled), your SMTP relay (if
   configured), and the browser's push service (if push alerts are on). See
   `docs/PRIVACY.md` for the full list.

**If you are an organisation, work through the rest of this document. If you are
an individual watching your own accounts, you can stop here and just read
`SECURITY.md`.**

---

## 2. Role mapping

| Role | Who | Why |
|------|-----|-----|
| **Controller** (GDPR Art. 4(7)) / **Data Fiduciary** (DPDP s.2(i)) | **The deploying organisation.** You chose to install it, you chose which providers to poll, you decide the retention and you hold the database | You determine the purposes and means of the processing. Nobody else can |
| **Processor** (GDPR Art. 4(8)) / **Data Processor** (DPDP s.2(k)) | **Nobody**, in the normal case | onWatch is software running on your own infrastructure. No third party processes the data on your behalf. The exception: if you host onWatch *for someone else* - a managed-service provider, an agency running it for a client, an internal platform team running it for a separate legal entity - then **you** are the processor for that other entity's data and need a processing agreement with them (GDPR Art. 28 / DPDP s.8(2)) |
| **The onWatch project and its maintainers** | **Neither controller nor processor** | The project supplies GPL-3.0 source code. It has no access to your deployment, receives no data from it, gives no instructions to it and cannot reach it. Supplying software is not processing |
| **The AI provider vendors** (Anthropic, OpenAI, Google, GitHub, Cursor, xAI, Moonshot, MiniMax, DeepSeek, OpenRouter, Z.ai, Ollama and the rest) | **Independent controllers** of their own account and usage data | Each has its own pre-existing relationship with the account holder and its own privacy notice. onWatch is a read-only client of that relationship. Your vendor contracts and any transfer mechanism they require are a separate exercise from onWatch |
| **Your SMTP provider** | **Your processor**, under whatever contract you already have with them | Alert emails contain provider account names and quota figures |
| **The browser push service** (Google FCM / Mozilla / Apple / Microsoft, chosen by the *viewer's* browser, not by you) | Independent third party; effectively an unavoidable intermediary | Payloads are encrypted so it cannot read the alerts, but it learns your daemon's egress IP and the timing of every alert. You cannot contract with it or choose it. If that is unacceptable, do not enable browser push |

### No DPA with the onWatch project is needed - or possible

You will not get a Data Processing Agreement from the onWatch project, and you
do not need one. A DPA under GDPR Art. 28 binds a *processor* - an entity that
processes personal data on the controller's behalf. The project processes none
of your data. There is no data flow to paper over, no sub-processor list, no
transfer mechanism, and no entity on the other side with access to bind.

If a procurement questionnaire demands a DPA, the accurate answer is: *"onWatch
is self-hosted GPL-3.0 software. The supplier has no access to any personal data
and is not a processor. No DPA applies. The controller is [your organisation]."*
Point them at this section and at `docs/PRIVACY.md`. What you *may* legitimately
need is a licence and security review of the software, which is a different
artefact - `SECURITY.md` is written for that.

---

## 3. Record of processing (GDPR Art. 30)

Adapt this table into your own ROPA. Cells in `[SQUARE BRACKETS]` are yours to
fill; everything else is pre-filled from what onWatch actually does. The
field-level detail sits in `docs/PRIVACY.md` - reference it from your ROPA
rather than copying 52 rows into it.

> **Art. 30(5) note:** organisations under 250 employees are exempt from
> maintaining a full ROPA *unless* the processing is not occasional, or is likely
> to result in a risk to rights and freedoms, or includes special-category data.
> Continuous automated polling every 120 seconds is **not occasional**, so the
> exemption almost certainly does not apply to a standing onWatch deployment.
> Keep the record.

| Art. 30(1) item | Content for onWatch |
|---|---|
| **Name and contact of the controller** | `[YOUR LEGAL ENTITY, ADDRESS]` |
| **DPO / contact** | `[DPO OR RESPONSIBLE PERSON, EMAIL]` - the same contact you must publish under DPDP s.8(9) |
| **Purposes of processing** | Monitoring consumption of paid AI provider quotas in order to (a) avoid service interruption from quota exhaustion, (b) control and forecast spend, (c) allocate capacity between teams. `[ADD OR REMOVE TO MATCH YOUR ACTUAL PURPOSE - and keep it narrow: the database supports far more than you probably intend to do with it]` |
| **Categories of data subject** | Holders of the AI provider accounts being tracked: `[EMPLOYEES / CONTRACTORS / TEAM MEMBERS]`. Plus dashboard viewers, whose IP addresses are logged, and alert recipients |
| **Categories of personal data** | Account identifiers (provider account names, external IDs, project IDs, GitHub login, Codex account ID, Kimi user ID); email addresses (Antigravity, Grok, Ollama account email, SMTP from/to); authentication credentials (provider OAuth access and refresh tokens, API keys, session cookies, the dashboard password hash); usage and behavioural history (per-provider quota snapshots, reset cycles, per-model request counts, per-request usage events); technical data (dashboard client IP addresses, User-Agent, push subscription endpoints which are stable device identifiers); verbatim vendor API responses (`raw_json`). **No special-category data.** Full field-level inventory: `docs/PRIVACY.md` |
| **Recipients** | The 16 AI provider APIs (each an independent controller, contacted on every poll using the account holder's own credential); `api.github.com` for the version check **if enabled** - off-switchable in Settings -> General -> "Privacy & Outbound Connections" or pinned off with `ONWATCH_UPDATE_CHECK=false`; your SMTP relay **if configured**; the viewer's browser push service **if push alerts are enabled**. **No data of any kind goes to the onWatch project.** No analytics, no CDN, no font host - all dashboard assets are served from the binary |
| **Third-country transfers** | onWatch itself transfers nothing to the project. Provider polling reaches vendor endpoints that are predominantly US-hosted, using a credential the account holder already has with that vendor. `[IDENTIFY THE VENDORS YOU POLL, THEIR HOSTING LOCATION, AND THE TRANSFER MECHANISM IN YOUR CONTRACT WITH EACH - SCCs, adequacy, or the vendor's DPA]`. The version check reaches GitHub (US) when enabled. DPDP s.16 restricts transfer only to countries the Central Government notifies; check the current notified list |
| **Retention** | `[YOUR CONFIGURED PERIOD]`. **Read section 7 before filling this in** - the default for most tables has historically been "forever", and you must set a period and be able to show it is enforced. Configure it in Settings -> Data & Retention; `api_integration_usage_events` additionally honours `ONWATCH_API_INTEGRATIONS_RETENTION` (default 60 days) |
| **Security measures** | Self-hosted, single-tenant, no third-party access. bcrypt dashboard password; `subtle.ConstantTimeCompare` for credential comparison; per-IP login rate limiting; AES-256-GCM with an HKDF-SHA256 key derived from the dashboard password for the stored SMTP password (and, once the in-progress encryption work lands, for `gemini_tokens` and `provider_accounts.metadata` - **verify against your build before you write this down**); parameterised SQL only; data directory `0700`, `.env` and debug log `0600`; security headers and a `'self'`-only CSP with zero third-party subresources; optional TLS termination, secure cookies and trusted-proxy SSO. **Read `SECURITY.md` in full** - the known hardening gaps (default `0.0.0.0` bind, default password `changeme`, unauthenticated `/metrics`, unencrypted database) are part of your honest answer here, together with the mitigations you applied. `[LIST YOUR APPLIED MITIGATIONS: bind address, TLS, disk encryption, firewall, metrics token]` |

### Art. 35 - do you need a DPIA?

Probably yes, if you are an employer. Systematic monitoring of employees is on
most supervisory authorities' mandatory-DPIA lists, and onWatch is continuous
(every 120 seconds by default), automated, and produces a detailed behavioural
record retained for as long as you configure. The combination of *systematic
monitoring* and *data about employees, who are in a position of dependency* is
the standard trigger.

If you conclude a DPIA is not needed, write down why. `[DPIA REFERENCE OR THE
DOCUMENTED REASON IT IS NOT REQUIRED]`

DPDP has no general DPIA duty, but a **Significant Data Fiduciary** designated
under s.10 must appoint a DPO based in India, conduct a Data Protection Impact
Assessment and commission a periodic independent audit. Check whether you are
designated.

---

## 4. Lawful basis - and why the two regimes do not give the same answer

This is the section most compliance write-ups get wrong by treating GDPR and
DPDP as interchangeable. They are not, and the difference has real engineering
consequences for an Indian deployment.

### GDPR: legitimate interests is available

For an employer monitoring AI spend, **Art. 6(1)(f) legitimate interests** is
usually the workable basis. Controlling expenditure on paid services and keeping
a team from hitting a hard quota mid-sprint is a real interest, the data is
mostly consumption metrics, and the processing is what the employee would
reasonably expect from a company-paid subscription.

It is not automatic. To rely on it you must:

1. **Run and document a three-part balancing test (LIA):** the legitimate
   interest; the necessity of *this* processing for it; and the balance against
   the data subject's interests, rights and freedoms. Keep it on file - you have
   to be able to produce it.
2. **Be honest about necessity.** Spend control needs aggregate consumption. It
   does not obviously need indefinite retention of verbatim vendor responses, or
   per-request event logs, or an email address in fourteen snapshot tables.
   Necessity is where a broad retention setting fails a balancing test that
   would otherwise pass - so set retention *before* you write the LIA.
3. **Give an Art. 13 notice anyway.** Legitimate interests is a basis, not an
   exemption from transparency. Employees must be told, before or at the start
   of monitoring: that it happens, what is collected, why, on what basis, for
   how long, who receives it, and their rights. `docs/PRIVACY.md` gives you the
   content; the notice itself is yours to issue.
4. **Honour Art. 21 objection.** Under legitimate interests a data subject can
   object, and you must stop unless you demonstrate compelling overriding
   grounds. In practice: be able to disable polling for one account. The
   dashboard's per-provider and per-account polling toggles are how you do it.
5. **Check national employment law.** GDPR Art. 88 lets member states add rules
   for employment processing, and several have. Works-council or employee-
   representative consultation is mandatory in some jurisdictions before
   introducing a monitoring tool. `[CHECK YOUR JURISDICTION]`

**Do not use consent for employee monitoring under GDPR.** Consent must be
freely given, and the EDPB's settled position is that an employee's consent to
their employer is rarely free because of the imbalance of power. Consent you
cannot rely on is worse than no consent, because withdrawal would oblige you to
stop and you built the process on it.

### DPDP Act: there is no legitimate-interests basis of that shape

The DPDP Act does **not** have a general balancing-test basis. Personal data may
be processed only:

- **with consent under s.6**, or
- **for a "certain legitimate use" enumerated in s.7.**

That list is closed. It covers things like a voluntary provision of data for a
specified purpose, employment-related purposes, State functions, medical
emergencies and compliance with law. It does **not** contain a
"whatever-is-proportionate-to-our-business-interest" clause. You cannot import
Art. 6(1)(f) reasoning into it.

**s.7(i) - employment purposes - is the provision to examine.** It permits
processing necessary for purposes of employment, or for safeguarding the
employer from loss or liability. Cost control on employer-paid AI subscriptions
is a credible fit. But it is narrower than it looks: the processing must be
*necessary* for that purpose. Indefinite retention of verbatim API responses is
not necessary to prevent loss, so an over-broad deployment can fall outside s.7
even where a lean one sits comfortably inside it.

If you cannot land on s.7, you need **consent under s.6**, and s.6(1) sets a
high bar. It must be:

| Requirement | What it means for an onWatch deployment |
|---|---|
| **Free** | Same problem as GDPR. Employee consent to an employer is hard to make free, and the DPDP text does not soften it |
| **Specific** | Consent to "AI quota monitoring" is not consent to seven providers the employee never configured. See section 6 |
| **Informed** | The s.5 notice must precede or accompany the request, and must itemise the personal data and the purpose |
| **Unconditional** | You may not make access to a service contingent on consent to processing not necessary for it (s.6(1) read with s.7) |
| **Unambiguous, with clear affirmative action** | An actual opt-in. Not a pre-ticked box, not silence, not "continued use implies consent", not a line in an onboarding pack |
| **Withdrawable with comparable ease** (s.6(4)-(6)) | Withdrawal must be as easy as giving it - and it triggers a **hard erasure duty** under s.8(7) |

### What the difference means in practice for an Indian deployment

| Consequence | Detail |
|---|---|
| **Decide the basis before you deploy, not after** | Under GDPR you can document an LIA for a running system. Under DPDP, if consent is your basis, you need it *before* the first poll - and onWatch auto-starts polling seven providers on first run (section 6). Disable what you have no basis for *before* first start, using the pre-start environment kill switches where they exist |
| **Build the withdrawal path** | GDPR objection is "stop unless overriding grounds". DPDP withdrawal is "stop, and erase" (s.8(7)). You need a tested procedure: turn off that account's polling, run the erasure control, and deal with backups and the JSONL input files (section 5) |
| **Erasure is not request-driven** | s.8(7) obliges erasure on withdrawal of consent **or** when the purpose is no longer being served, whichever is earlier - whether or not anyone asks. Under GDPR, Art. 17 is largely request-driven while Art. 5(1)(e) storage limitation runs in the background. Under DPDP it is an operational trigger you must monitor: when an employee leaves, their data's purpose is served |
| **Notice language** | s.5(3): the notice must be available in English or any of the 22 languages in the **Eighth Schedule** to the Constitution. A translation obligation GDPR does not impose in that form |
| **Accuracy duty bites harder** | s.8(3) requires completeness, accuracy and consistency where the data is used to make a decision affecting the Data Principal or is disclosed to another Data Fiduciary. If onWatch figures feed a performance or chargeback decision, that is squarely in scope - and note that provider-sourced data is read-only in onWatch and can only be corrected at the provider (section 5) |
| **Rank the work by exposure** | See section 7 |

**If you operate in both regimes,** build to the stricter requirement per
control: DPDP's consent mechanics and erasure-on-withdrawal, GDPR's documented
balancing test and Art. 13/14 notice detail. Do not try to run one basis for
both.

---

## 5. Data-subject-request runbook

Concrete operator steps. Adapt the wording to your own intake process; the
onWatch actions are the part that matters here.

> **Implementation status:** the retention, export and erasure controls
> referenced below and in section 3 live in the dashboard's settings, under
> "Data & Retention" or "General" depending on how the control shipped. **Open
> your own build and confirm they are there before you promise a requester a
> one-month turnaround.** If they are absent, the build predates that work: fall
> back to scoped SQL against `onwatch.db` with the daemon stopped, followed by
> `VACUUM`, and note that a hand-written `DELETE` must cascade on `account_id`
> and `snapshot_id` across every provider table or it will leave orphaned
> quota, reset-cycle and model-value rows behind.

### 5.1 Access (GDPR Art. 15) and portability (Art. 20) / DPDP s.11

| Step | Action |
|---|---|
| 1 | Verify the requester's identity against the account they are asking about. Do not disclose one person's quota history to another |
| 2 | Settings -> Data & Retention -> **Export**. Scope the export to the provider account(s) belonging to the requester |
| 3 | The export covers the database. It does **not** cover: `~/.onwatch/data/.onwatch.log` and its rotations (which hold dashboard client IP addresses, proxy-asserted usernames and provider account names), `~/.onwatch/api-integrations/*.jsonl` input files, `~/.onwatch/data/codex-profiles/*.json`, or your backups. If the request is broad, retrieve those manually |
| 4 | **Redact credentials before you hand anything over.** An export scoped to a person can include that person's provider OAuth tokens and API keys. Those are their data, but emailing them is creating a new breach. Deliver credentials through a secure channel or omit them with an explanation |
| 5 | Add what only you can supply: your identity as controller, the purposes, the lawful basis, recipients, retention, and their rights. `docs/PRIVACY.md` gives you the field-level descriptions to attach |
| 6 | **Deadlines:** GDPR Art. 12(3) - one month, extendable by two further months for complex requests with notice inside the first month. DPDP - as prescribed by the Rules; do not assume a longer period than GDPR |

Portability (Art. 20) applies where processing rests on consent or contract and
is automated. If you relied on legitimate interests, Art. 20 does not strictly
apply - but the export is machine-readable anyway, so refusing on that technical
ground is rarely worth the argument.

### 5.2 Erasure (GDPR Art. 17) / DPDP s.12 and s.8(7)

| Step | Action |
|---|---|
| 1 | Establish whether an exception applies (GDPR Art. 17(3) - legal obligation, legal claims; DPDP s.12(3) - retention required by law). Financial records you must keep for tax are a real limit; the quota history behind them usually is not |
| 2 | **Stop the collection first.** Turn off polling for that provider account in the dashboard, or erasure will simply re-populate on the next poll cycle |
| 3 | Settings -> Data & Retention -> **Erase**, scoped to the account. This must cascade on `account_id` and `snapshot_id` across all provider tables - deleting the identity row alone leaves the linked quota, reset-cycle and model-value rows behind |
| 4 | **`VACUUM` afterwards.** SQLite leaves deleted rows in freed pages. `sqlite3 ~/.onwatch/data/onwatch.db "VACUUM;"` with the daemon stopped. Note also that `api_integration_usage_events.raw_line` was dropped by a schema migration and `DROP COLUMN` reclaims nothing - a pre-upgrade database still contains whole raw event lines until vacuumed |
| 5 | **Handle the JSONL input files.** `~/.onwatch/api-integrations/*.jsonl` are only *tailed* by onWatch - it never deletes or truncates them. The full original events survive any database-side erasure. You must delete or rotate them yourself, and remember the `api_integration_ingest_state` row keeps `source_path` and up to 512 KB of `partial_line` telemetry |
| 6 | **Handle the log.** `~/.onwatch/data/.onwatch.log` plus `.1`/`.2`/`.3` hold IP addresses and account names. Size-capped at 4 x 50 MB with no time limit, so they do not age out on a schedule. Delete or truncate them as part of a full erasure |
| 7 | **Handle backups.** Nothing in onWatch reaches your backups. Either restore-and-re-erase, or - far more practical - keep backup retention short, document it, and tell the requester the date by which the last copy expires. A backup you cannot surgically edit is a known and defensible limitation only if you have written the retention down |
| 8 | **Handle credential files.** If the person is leaving, the third-party credential files onWatch reads and rewrites (`~/.claude/.credentials.json`, `~/.codex/auth.json`, `~/.gemini/oauth_creds.json`, `~/.grok/auth.json`, the Kimi credential file, Cursor's `state.vscdb`, the macOS Keychain item and the GNOME keyring entry) and the `.bak` copies onWatch leaves beside them are their credentials, on your machine. Revoke at the provider and delete locally |
| 9 | Record what you erased, when, and what you could not erase and why |

**The DPDP s.8(7) wrinkle - this is the one a GDPR-shaped process misses.** Under
s.8(7) a Data Fiduciary must erase personal data **as soon as** the Data
Principal withdraws consent, **or** as soon as it is reasonable to assume the
specified purpose is no longer being served - whichever is earlier - unless
retention is required by law. **Nobody has to ask.** You need a trigger for it:

- An employee leaves -> the purpose of tracking their quota is served -> erase.
- A provider subscription is cancelled -> that account's purpose is served ->
  erase.
- Consent withdrawn -> erase, immediately, and stop polling.

Wire this into your offboarding checklist. It is the DPDP obligation most likely
to be breached silently for years, because nothing generates a ticket.

### 5.3 Correction / rectification (GDPR Art. 16) / DPDP s.12

Split the data in two, because the answer differs:

| Data | Correctable in onWatch? | What to do |
|---|---|---|
| **Provider-sourced data** - quota snapshots, reset cycles, account emails read from the vendor, `raw_json`, per-model counts | **No. Read-only.** onWatch is a polling client; it never writes back to a vendor and there is no edit path for polled values | Correct it **at the provider** (the vendor's own account settings), then let onWatch re-poll. New snapshots carry the corrected value. Explain to the requester that you are a downstream copy and name the upstream controller so they can exercise the right there too |
| **Historical snapshots already stored** | Not editable | They are a time-series record of what the provider reported at that moment, not a claim about the person now. If a stored value is genuinely wrong and matters, the remedy is erasure of the affected rows, not editing them - editing a historical record makes it less accurate, not more |
| **Local labels** - the dashboard provider labels and the `provider_accounts.name` you typed | **Yes.** Editable in the dashboard's provider management | Fix in place. A misspelled name, a wrong display label or a stale team attribution is corrected here |
| **Derived figures you publish** - a chargeback report, a spend allocation | Your artefact, not onWatch's | Correct it in your own system. Remember DPDP s.8(3): where data is used for a decision affecting the Data Principal, the completeness and accuracy duty is on you |

### 5.4 Objection (GDPR Art. 21) and restriction (Art. 18)

Objection is the practical right where you relied on legitimate interests. The
operator action is the same as the first step of erasure: turn off polling for
that provider or that account in the dashboard. Do that while you assess whether
you have compelling overriding grounds - not after.

Restriction under Art. 18 has no dedicated control. Turning off polling and
recording internally that the stored data must not be used is the workable
equivalent.

### 5.5 Grievance redressal (DPDP s.13) and complaints

DPDP gives every Data Principal a **right to readily available means of
grievance redressal** in respect of any act or omission of the Data Fiduciary,
and the Data Principal must exhaust that route before approaching the Board.

- `[PUBLISH A CONTACT - an address or a form - AND THE RESPONSE PERIOD YOU
  COMMIT TO.]` This is a publication obligation (s.8(9)-(10)), not an internal
  note. Put it wherever your privacy notice lives.
- The contact must be one that a person who does not work for you can find and
  use.
- Log each grievance, the response and the date. You will be asked.
- Under GDPR, also tell data subjects they may complain to a supervisory
  authority and name the relevant one.

### 5.6 Automated decision-making

onWatch makes no automated decisions about people. It polls quotas, stores them
and draws charts. Alerts are threshold notifications, not decisions.

**But:** if *you* feed onWatch data into a decision about a person - performance
review, chargeback, capacity allocation, disciplinary action - that is your
automated or semi-automated decision-making. GDPR Art. 22 and the associated
transparency duties attach to *your* process, and DPDP s.8(3)'s accuracy duty
applies squarely. Do not assume it is out of scope because the tool is passive.

---

## 6. The seven auto-enabled providers

**You cannot write an accurate privacy notice without this section.**

onWatch begins polling seven provider APIs with **no configuration whatsoever**,
purely because another CLI's credential file exists on the machine
(`main.go:733-824`):

| Provider | Detected from | Pre-start kill switch |
|---|---|---|
| Anthropic | `~/.claude/.credentials.json`, macOS Keychain item `Claude Code-credentials`, GNOME keyring | **None** |
| Codex | `~/.codex/auth.json` or `$CODEX_HOME/auth.json`, or saved Codex profiles | **None** |
| OpenCode | The OpenCode `auth.json` (enabled as a side effect of Codex credential detection) | **None** |
| Cursor | Cursor's `state.vscdb` session token | **None** |
| Gemini | `~/.gemini/oauth_creds.json` | `GEMINI_ENABLED=false` |
| Grok | `~/.grok/auth.json` or `$GROK_HOME/auth.json` | `GROK_ENABLED=false` |
| Kimi | The `kimi-code.json` credential file | `KIMI_ENABLED=false` (or `KIMI_CODE_ENABLED=false`) |

Read that table twice. **An operator who installs onWatch intending to watch one
provider can silently start contacting seven** - each poll using the account
holder's own credential, on a 120-second timer, writing identifiers and usage
history into the database for each one.

For Anthropic, Codex, OpenCode and Cursor there is **no environment kill switch
at all**, so the first poll happens before anyone can object. You turn them off
after the fact, with the per-provider and per-account polling toggles in the
dashboard.

### What this obliges you to do

1. **Enumerate what your install actually polls**, on a representative machine,
   rather than what you configured. Check the dashboard's provider list after
   first start. Your ROPA's recipients cell and your notice's list of providers
   must match reality, not intent.
2. **Decide for each one: disable or document.**
   - *Disable*: set the kill switch before first start where one exists; switch
     polling off in the dashboard for the four that have none.
   - *Document*: name it in your notice, and make sure your lawful basis covers
     it. Under DPDP this is acute - consent must be **specific**, and consent to
     monitor one provider is not consent to monitor seven.
3. **Re-check after installs and upgrades.** Auto-detection is a function of
   what is on disk. An engineer installing a new AI CLI can silently add an
   eighth provider to your processing weeks after you signed off the notice.
   Put a periodic check in your review cycle.

`docs/PRIVACY.md` has the full outbound-call inventory. `SECURITY.md` carries
this item in its hardening checklist.

---

## 7. DPDP-specific checklist

Items a GDPR-shaped compliance programme will not generate, plus the penalty
exposure so you can rank them.

| # | Obligation | Section | What to do |
|---|---|---|---|
| 1 | **Publish a contact for data-protection questions** - the business contact information of a Data Protection Officer (if you are a Significant Data Fiduciary) or of a person able to answer a Data Principal's questions about the processing | s.8(9) | Publish a monitored address alongside your notice. This is a publication duty, not an internal assignment |
| 2 | **Provide a grievance-redressal mechanism** | s.8(10), s.13 | A readily available route with a stated response period, usable by someone outside your organisation. Log every grievance |
| 3 | **Offer nomination** | s.14 | A Data Principal may nominate another individual to exercise their rights in the event of death or incapacity. onWatch has no feature for this - handle it in your own process, and say in your notice how a nomination is made and recorded |
| 4 | **Notice in English or an Eighth Schedule language** | s.5(3) | Your notice must be *available* in English or any of the 22 Eighth Schedule languages. If your workforce reads Hindi, Tamil, Bengali or Marathi, translate. Itemise the personal data and the purpose - a generic notice does not satisfy s.5 |
| 5 | **Erase on withdrawal or when the purpose is served** | s.8(7) | The operational trigger from section 5.2. Wire it into offboarding and subscription-cancellation. Nobody has to ask |
| 6 | **Reasonable security safeguards** | s.8(5) | Work through `SECURITY.md`'s hardening checklist and record what you applied. The default `0.0.0.0` bind, the default `changeme` password and the unencrypted database are the three findings an auditor will reach first |
| 7 | **Breach notification to the Board *and* each affected Data Principal** | s.8(6) | No risk threshold of GDPR's kind. Plan on notifying. Deadlines and workflow: `SECURITY.md` |
| 8 | **Accuracy where data drives decisions or is disclosed onward** | s.8(3) | If onWatch figures feed a chargeback or a review, you own their accuracy - and provider data is read-only, correctable only upstream (section 5.3) |
| 9 | **Check cross-border restrictions** | s.16 | Transfer is permitted except to countries the Central Government restricts by notification. Check the current list against the vendors you poll |
| 10 | **Check Significant Data Fiduciary designation** | s.10 | If designated: an India-based DPO, a DPIA, and a periodic independent audit |
| 11 | **Children's data** | s.9 | Verifiable parental consent, no tracking or behavioural monitoring of children. An onWatch deployment should not be processing a child's data. If it could - an educational setting - stop and take advice; s.9(3) prohibits tracking and behavioural monitoring of children, and quota monitoring is behavioural monitoring |

### Penalty exposure (s.33 and the Schedule)

Use this to rank the work. Penalties are **per the Schedule**, imposed by the
Data Protection Board after an inquiry, and are upper limits rather than tariffs -
s.33(2) requires the Board to consider the nature, gravity and duration of the
breach, the type of data affected, repetition, any gain or loss, mitigation, and
proportionality.

| Breach | Maximum penalty |
|---|---|
| Failure to take reasonable security safeguards to prevent a personal data breach (s.8(5)) | **Up to Rs 250 crore** |
| Failure to notify the Board or affected Data Principals of a breach (s.8(6)) | **Up to Rs 200 crore** |
| Breach of the additional obligations relating to children (s.9) | **Up to Rs 200 crore** |
| Breach of the additional obligations of a Significant Data Fiduciary (s.10) | **Up to Rs 150 crore** |
| Breach of any other provision of the Act or the Rules | **Up to Rs 50 crore** |
| Breach of a Data Principal's own duties (s.15) | Up to Rs 10,000 |

**The ranking that follows:** the two largest exposures are *security* and
*breach notification* - exactly the two things a self-hosted deployment with a
default password on `0.0.0.0` and an unencrypted credential store gets wrong.
Work `SECURITY.md`'s hardening checklist before you polish your notice
wording. A perfect notice does not reduce a s.8(5) finding.

For comparison, GDPR Art. 83 caps at the higher of EUR 20 million or 4% of total
worldwide annual turnover for the more serious infringements (including the
Art. 5 and Art. 6 basics), and EUR 10 million or 2% for the rest (including
Art. 30 records and Art. 33 notification). Same ranking conclusion: the
foundational duties carry the larger number.

---

## 8. Deployment-posture matrix

What changes with scale. Find your row.

| | **Single developer, own accounts** | **Small team behind a VPN** | **Company-wide behind SSO** |
|---|---|---|---|
| **In scope at all?** | No - GDPR Art. 2(2)(c) / DPDP s.3(c)(ii) household exemption | **Yes.** Every team member whose account is tracked is a data subject / Data Principal | **Yes**, and at a scale that makes Art. 30 records, a DPIA and a published contact unavoidable |
| **Lawful basis** | None needed | GDPR: Art. 6(1)(f) with a documented LIA. DPDP: identify s.7(i) employment purposes, or get s.6 consent | Same, plus a formal sign-off. Consult works councils or employee representatives where required (GDPR Art. 88) |
| **Notice** | None | Written notice to each tracked person before monitoring starts. DPDP: English or an Eighth Schedule language | Published notice plus onboarding-pack inclusion; keep versions and dates |
| **Bind address** | `ONWATCH_HOST=127.0.0.1`. No reason to expose it | `127.0.0.1` behind a reverse proxy with TLS, reachable only over the VPN. Never `0.0.0.0` on a routable interface | `127.0.0.1` behind the proxy that terminates SSO. Firewall the port |
| **Authentication** | Change the password from `changeme`. That is the whole story | Real password, `ONWATCH_SECURE_COOKIES=true`, TLS at the proxy. Rate limiting is per-IP and header-spoofable (`SECURITY.md` gap 8) - have the proxy strip `X-Forwarded-For` | `ONWATCH_AUTH_MODE=trusted_proxy` with `ONWATCH_TRUSTED_PROXY_CIDRS` set to the proxy only. It reads `RemoteAddr`, not `X-Forwarded-For`, and requires exactly one identity header. Note the proxy-asserted username reaches the debug log |
| **`/metrics`** | Leave it, or set a token | Set `ONWATCH_METRICS_TOKEN`. It exposes per-account quota data and maps `account_id` to `account_name` | Token, plus block `/metrics` at the proxy except from your scraper's source |
| **Retention** | Your choice | **Set a period and be able to show it is enforced.** Default was historically unbounded for most tables | Set it, document it in the ROPA, and reconcile it against what the database actually holds. Include the JSONL input files and the debug log in the policy |
| **Access control on the data** | You | Name who may read the dashboard and who holds the host. Anyone with the dashboard password can read every tracked person's usage - there are no per-user roles | Same limitation: onWatch has a **single** admin account, no role separation, no per-user scoping. If one team must not see another team's usage, onWatch cannot enforce that - run separate instances |
| **Disk encryption** | Recommended | **Required.** `onwatch.db` holds live provider credentials in cleartext | Required, plus encrypted backups with a written, short retention |
| **Auto-enabled providers** | Note them; they are your own accounts | **Audit and decide** - disable or document. Your notice must match what the install actually polls | Audit per machine class, re-check after every image or CLI rollout, and put it in the change-management checklist |
| **DSR process** | None | A named owner and a tested export/erase path. Test it before you need it | Documented runbook, intake route, deadline tracking, grievance log, published contact |
| **DPIA** | No | Likely yes if employees are monitored. If you conclude not, write down why | Yes. Systematic monitoring of employees at scale |
| **Breach response** | Rotate credentials and move on | Notification duties apply. `SECURITY.md` has the deadlines | Rehearsed plan, named signatory for the Art. 33 / s.8(6) notification, forensics-ready logging |
| **Push notifications** | Your call | Note the push service learns the daemon's IP and every alert's timing. Payload is encrypted | Consider disabling browser push and using SMTP to an internal relay instead - one fewer third party you cannot contract with |
| **Version check** | Leave on, get security updates | Decide deliberately. Off = `ONWATCH_UPDATE_CHECK=false`, and then subscribe to releases so you still learn about fixes | Usually off in a controlled environment, with patching handled by your own pipeline |

### A note on multi-tenancy

onWatch has one admin account and no authorisation model beyond
authenticated/not. Anyone who can log in sees every tracked account. If your
compliance position depends on Team A not seeing Team B's usage, **onWatch
cannot enforce it** - run separate instances with separate databases, or accept
and document that every dashboard user is a recipient of every tracked person's
data. Do not describe a single instance as segregated.

---

## 9. Quick reference

| I need to... | Go to |
|---|---|
| Know exactly what is stored, and where | `docs/PRIVACY.md` |
| Harden the deployment | `SECURITY.md`, "Hardening checklist" |
| Report or respond to a vulnerability | `SECURITY.md`, "Reporting a vulnerability" |
| Find the breach notification deadlines | `SECURITY.md`, "Breach detection and response" |
| Fill in a ROPA | Section 3 above |
| Pick a lawful basis | Section 4 above |
| Answer a data-subject request | Section 5 above |
| See what onWatch polls without being told to | Section 6 above |
| Destroy data properly | `SECURITY.md`, "Destroying the database properly", plus section 5.2 above |
| Answer a procurement DPA request | Section 2 above |

Statutory references are to Regulation (EU) 2016/679 (GDPR) and the Digital
Personal Data Protection Act, 2023 (India) as at the time of writing. Both are
supplemented by instruments that change - GDPR by member-state law and EDPB
guidance, DPDP by the Rules and Board practice. Check the current text. This
document describes software behaviour, which the maintainers can vouch for; it
does not interpret the law for your situation, which they cannot.
