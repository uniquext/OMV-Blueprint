import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = dirname(dirname(fileURLToPath(import.meta.url)))
const styles = readFileSync(join(root, 'src/styles.css'), 'utf-8')

function rule(selector) {
  const escaped = selector.replaceAll('.', '\\.')
  return styles.match(new RegExp(`${escaped}\\s*\\{[^}]*\\}`))?.[0] ?? ''
}

function assertContains(value, expected, label) {
  if (!value.includes(expected)) {
    throw new Error(`${label} must contain "${expected}".\n${value}`)
  }
}

function assertNotContains(value, unexpected, label) {
  if (value.includes(unexpected)) {
    throw new Error(`${label} must not contain "${unexpected}".\n${value}`)
  }
}

function mediaBlock(query) {
  const start = styles.indexOf(`@media ${query}`)
  if (start === -1) {
    return ''
  }
  const blockStart = styles.indexOf('{', start)
  let depth = 0
  for (let index = blockStart; index < styles.length; index += 1) {
    if (styles[index] === '{') {
      depth += 1
    }
    if (styles[index] === '}') {
      depth -= 1
      if (depth === 0) {
        return styles.slice(start, index + 1)
      }
    }
  }
  return styles.slice(start)
}

const summaryRule = rule('.history-summary')
const toolbarRule = rule('.history-toolbar')
const filterBarRule = rule('.history-filter-bar')
const panelRule = rule('.panel')
const controlRule = rule('.history-filter-control')
const triggerRule = rule('.history-filter-trigger')
const triggerAfterRule = rule('.history-filter-trigger::after')
const dateControlRule = rule('.history-filter-date')
const dateControlAfterRule = rule('.history-filter-date::after')
const calendarPopoverRule = rule('.history-calendar-popover')

assertContains(panelRule, 'overflow: visible;', '.panel')

assertContains(toolbarRule, 'gap: 12px;', '.history-toolbar')
assertContains(toolbarRule, 'padding: 14px 18px 8px;', '.history-toolbar')

assertContains(summaryRule, 'gap: 24px;', '.history-summary')
assertContains(summaryRule, 'min-width: 700px;', '.history-summary')
assertContains(summaryRule, 'margin-bottom: 0;', '.history-summary')
assertContains(summaryRule, 'padding: 0;', '.history-summary')
assertContains(summaryRule, 'font-size: 13px;', '.history-summary')
assertContains(summaryRule, 'border: 0;', '.history-summary')
assertNotContains(summaryRule, 'border: 1px solid #e8e8ec;', '.history-summary')
assertNotContains(summaryRule, 'padding: 14px 16px;', '.history-summary')
assertNotContains(summaryRule, 'margin-bottom: 16px;', '.history-summary')
assertNotContains(summaryRule, 'min-height: 100px;', '.history-summary')

assertContains(filterBarRule, 'margin-bottom: 6px;', '.history-filter-bar')
assertNotContains(filterBarRule, 'margin-bottom: 16px;', '.history-filter-bar')

assertContains(controlRule, 'min-height: 32px;', '.history-filter-control')
assertContains(controlRule, 'border: 1px solid #d0d0d6;', '.history-filter-control')
assertContains(controlRule, 'border-radius: 6px;', '.history-filter-control')
assertContains(controlRule, 'padding: 6px 12px;', '.history-filter-control')
assertContains(controlRule, 'font-size: 13px;', '.history-filter-control')
assertContains(controlRule, 'font-weight: 400;', '.history-filter-control')
assertNotContains(controlRule, 'height: 56px;', '.history-filter-control')
assertNotContains(controlRule, 'font-size: 22px;', '.history-filter-control')

assertContains(triggerRule, 'gap: 6px;', '.history-filter-trigger')
assertContains(triggerRule, 'color: #333333;', '.history-filter-trigger')
assertContains(triggerAfterRule, 'content: "▼";', '.history-filter-trigger::after')
assertContains(triggerAfterRule, 'font-size: 10px;', '.history-filter-trigger::after')

assertContains(dateControlRule, 'width: 180px;', '.history-filter-date')
assertContains(dateControlRule, 'font-weight: 600;', '.history-filter-date')
assertContains(dateControlAfterRule, 'width: 14px;', '.history-filter-date::after')
assertContains(dateControlAfterRule, 'height: 14px;', '.history-filter-date::after')
assertContains(dateControlAfterRule, 'background-color: #333333;', '.history-filter-date::after')
assertContains(dateControlAfterRule, 'mask: url("data:image/svg+xml', '.history-filter-date::after')
assertContains(calendarPopoverRule, 'width: 180px;', '.history-calendar-popover')
assertContains(calendarPopoverRule, 'font-size: 15px;', '.history-calendar-popover')

if (/\.history-(?:filter|toolbar|summary)/.test(mediaBlock('(max-width: 760px)'))) {
  throw new Error('History page styles must not be overridden by the mobile media query.')
}

console.log('History style scale check passed')
