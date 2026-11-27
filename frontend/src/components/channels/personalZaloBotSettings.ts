export type PersonalZaloBotMode = 'chatbot' | 'internbot'

export interface PersonalZaloBotSettings {
  chatbot_mode: PersonalZaloBotMode
  supervisor_group_id: string
  internbot_enabled: boolean
}

export function readPersonalZaloBotSettings(raw?: string | Record<string, unknown>): PersonalZaloBotSettings {
  let metadata: Record<string, unknown> = {}
  if (typeof raw === 'string') {
    try {
      const decoded = JSON.parse(raw)
      if (decoded && typeof decoded === 'object' && !Array.isArray(decoded)) metadata = decoded
    } catch {
      // Treat unreadable metadata as the legacy Chatbot mode.
    }
  } else if (raw) {
    metadata = raw
  }
  const mode = metadata.chatbot_mode === 'internbot' ? 'internbot' : 'chatbot'
  return {
    chatbot_mode: mode,
    supervisor_group_id: typeof metadata.supervisor_group_id === 'string' ? metadata.supervisor_group_id : '',
    internbot_enabled: mode === 'internbot' && metadata.internbot_enabled === true,
  }
}
