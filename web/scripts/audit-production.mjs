// SPDX-License-Identifier: AGPL-3.0-or-later

import { spawnSync } from 'node:child_process'

const acceptedAdvisories = new Set([
  'https://github.com/advisories/GHSA-qwww-vcr4-c8h2',
])

const result = spawnSync('npm', ['audit', '--omit=dev', '--json'], {
  encoding: 'utf8',
  shell: process.platform === 'win32',
})

if (!result.stdout) {
  process.stderr.write(result.stderr)
  process.exit(result.status ?? 1)
}

const report = JSON.parse(result.stdout)
const vulnerabilities = Object.values(report.vulnerabilities ?? {})
const advisories = vulnerabilities.flatMap((vulnerability) =>
  vulnerability.via.filter((cause) => typeof cause === 'object'),
)
const unexpected = advisories.filter(
  (advisory) => !acceptedAdvisories.has(advisory.url),
)

if (unexpected.length > 0) {
  for (const advisory of unexpected) {
    console.error(`${advisory.severity}: ${advisory.title} (${advisory.url})`)
  }
  process.exit(1)
}

if (vulnerabilities.length > 0 && advisories.length === 0) {
  console.error('npm reported vulnerabilities without advisory details')
  process.exit(1)
}

if (advisories.length > 0) {
  console.log(
    'Accepted GHSA-qwww-vcr4-c8h2: Orako is a browser SPA and does not use React Router RSC mode.',
  )
} else {
  console.log('No production dependency vulnerabilities found.')
}
