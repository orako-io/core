// Default expertise tags (project_members.domains) the agent routes on.
// Suggestions only — members may carry free-form custom tags.

export const EXPERTISE_TAGS: readonly string[] = [
  'Product Owner',
  'Product Manager',
  'UX',
  'UI',
  'Design/DA',
  'Frontend',
  'Backend',
  'DevOps',
  'QA',
  'Lead dev',
  'CTO',
] as const

export function parseTags(input: string): string[] {
  return input
    .split(',')
    .map(t => t.trim())
    .filter(t => t.length > 0)
}
