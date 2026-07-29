// Tiny browser-download helper for RPC responses that carry a file as base64
// (connect-JSON encodes a proto `bytes` field that way). Decodes to a Blob and triggers a save via a throwaway anchor click.

export function downloadBase64(base64: string, filename: string, mime = 'application/zip'): void {
  const binary = atob(base64)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  const blob = new Blob([bytes], { type: mime })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}
