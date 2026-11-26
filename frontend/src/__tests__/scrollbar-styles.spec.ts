/// <reference types="node" />
// @vitest-environment happy-dom
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import mainSource from '../main.ts?raw'
import defaultLayoutSource from '../layouts/DefaultLayout.vue?raw'
import insightWordcloudSource from '../components/InsightWordcloudPanel.vue?raw'
import onboardingWizardSource from '../components/OnboardingWizard.vue?raw'

const scrollbarCss = readFileSync(
  resolve(process.cwd(), 'src/styles/scrollbar.css'),
  'utf8',
)

describe('global scrollbar design', () => {
  it('loads global scrollbar styles after Vuetify so cascade layers stay ordered', () => {
    const vuetifyIdx = mainSource.indexOf("import vuetify from './plugins/vuetify'")
    const scrollbarIdx = mainSource.indexOf("import './styles/scrollbar.css'")

    expect(vuetifyIdx).toBeGreaterThanOrEqual(0)
    expect(scrollbarIdx).toBeGreaterThan(vuetifyIdx)
  })

  it('scopes base scrollbar rules inside vuetify-core.base and isolates standard scrollbar props for non-WebKit browsers', () => {
    expect(scrollbarCss).toContain('@layer vuetify-core.base')
    expect(scrollbarCss).toContain('*::-webkit-scrollbar')
    expect(scrollbarCss).toContain('*::-webkit-scrollbar-thumb')
    expect(scrollbarCss).toContain('*::-webkit-scrollbar-button')
    expect(scrollbarCss).toContain('*::-webkit-scrollbar-corner')
    expect(scrollbarCss).toContain('html::-webkit-scrollbar')
    expect(scrollbarCss).toContain('.v-theme--dark')
    expect(scrollbarCss).toContain('var(--v-theme-on-surface)')
    expect(scrollbarCss).toContain('var(--v-theme-background)')
    expect(scrollbarCss).toContain('@supports not selector(::-webkit-scrollbar)')
  })

  it('keeps component scrollbars aligned without overriding WebKit styling in Chrome 121+', () => {
    for (const source of [defaultLayoutSource, insightWordcloudSource]) {
      expect(source).toContain('@supports not selector(::-webkit-scrollbar)')
      expect(source).not.toContain('@supports not (scrollbar-color')
    }
    expect(onboardingWizardSource).toContain('scrollbar-width: none')
    expect(onboardingWizardSource).toContain('.onboarding-steps::-webkit-scrollbar')
  })
})
