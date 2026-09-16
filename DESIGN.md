# Design

## Source of truth

Status: Active. Updated: 2026-09-16. Product surfaces: authenticated CQA web application, with current focus on the Service Quality dashboard and insight-detail dialog. Evidence reviewed: `README.md`, `frontend/src/plugins/vuetify.ts`, `frontend/src/App.vue`, `frontend/src/views/ServiceQuality.vue`, `frontend/src/components/InsightWordcloudPanel.vue`, and the supplied mobile screenshot.

## Brand

Professional, clear, and operational. Red is the primary BBI accent and should signal selection or priority without reducing legibility. Avoid decorative styling that competes with customer-service data.

## Product goals

Help service teams scan operational state, understand AI-classified conversations, and reach source conversations quickly. Preserve accurate data presentation at all viewport sizes. Non-goal: introducing a separate visual system outside Vuetify. Success means labels, counts, actions, and states remain unambiguous on desktop and mobile.

## Personas and jobs

Owners and admins configure channels and analysis; managers monitor quality and response queues; agents inspect classified conversations. They need fast scanning, trustworthy counts, and direct navigation to evidence.

## Information architecture

Tenant-scoped navigation leads to dashboards, channels, jobs, messages, CRM, and settings. Service Quality presents filters, summary KPIs, data-source health, content insights, and the conversation queue. Insight details pair a keyword/count list with its matching source conversations.

## Design principles

Keep operational values visually stable; preserve content hierarchy under localization and wrapping; use progressive detail in dialogs; prefer existing Vuetify components and theme tokens; keep controls discoverable and keyboard accessible.

## Visual language

Use the Vuetify light/dark themes and their `primary`, `surface`, `background`, and semantic status colors. Cards use the configured rounded `lg` shape and light elevation or outlined variants. Typography follows Vuetify type classes. Spacing uses Vuetify utilities and the existing 8px-oriented rhythm. Motion should be functional and brief. Use Material Design Icons already shipped with the app.

## Components

Vuetify owns cards, dialogs, buttons, fields, selects, alerts, chips, tables, and layout primitives. Repository components own domain presentation. In keyword rows, the label consumes flexible width and may wrap; the numeric count is a non-shrinking, single-line value using tabular numerals.

## Accessibility

Retain semantic buttons and links, visible `:focus-visible` outlines, meaningful labels, and keyboard navigation. Text and status colors must use theme tokens with adequate contrast. Wrapped content must not overlap, clip, or reorder its associated value.

## Responsive behavior

Desktop insight details use two columns; at 600px and below they stack into one column with a bounded keyword list. Long labels may wrap naturally. Associated numeric counts remain aligned at the row end and never wrap between digits.

## Interaction states

Support loading skeletons/progress, empty reports and keyword lists, recoverable errors, selected and hover states, disabled controls, and refreshed data that may remove the current selection. Slow refreshes should preserve the last valid report where possible.

## Content voice

Vietnamese is the default operational language, with English localization supported. Copy is concise, factual, and action-oriented. Reuse domain terms consistently, including “keyword”, “hội thoại”, “kênh”, and “chất lượng”.

## Implementation constraints

Frontend stack: Vue 3, TypeScript, Vuetify 4, Vite, and scoped component styles. Reuse theme variables and existing breakpoints. UI changes require focused component tests plus the full frontend test/build checks; responsive defects should be validated against the narrow mobile layout supplied by the user.

## Open questions

- [ ] Product owner: confirm the long-term minimum supported viewport width; this affects future density and truncation choices below 320px.
