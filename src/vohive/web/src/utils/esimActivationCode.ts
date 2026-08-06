export type EsimActivationCode = {
  raw: string
  smdp: string
  matchingId: string
  optionalFields: string[]
}

/**
 * Parse the activation payload encoded by a consumer eSIM QR code.
 *
 * The QR payload is normally `LPA:1$<SM-DP+ address>$<matching ID>`.
 * SGP.22 also allows optional fields after the matching ID.  The current
 * download API only needs the first two values; keeping the optional fields
 * visible lets the UI warn without accidentally treating an OID or a
 * confirmation-code flag as the user's confirmation code.
 */
export function parseEsimActivationCode(input: string): EsimActivationCode {
  const raw = input.trim()
  if (!raw) {
    throw new Error('二维码中没有读取到内容')
  }

  const payload = raw.replace(/^LPA:/i, '')
  const parts = payload.split('$')
  if (parts[0] !== '1' || parts.length < 2) {
    throw new Error('不是受支持的 LPA:1 eSIM 激活码')
  }

  const smdp = parts[1].trim().replace(/^https?:\/\//i, '').replace(/\/+$/, '')
  if (!smdp) {
    throw new Error('LPA 激活码缺少 SM-DP+ 地址')
  }

  return {
    raw,
    smdp,
    matchingId: (parts[2] || '').trim(),
    optionalFields: parts.slice(3).map((part) => part.trim())
  }
}
