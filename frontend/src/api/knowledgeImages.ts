import api from '.'

export interface KnowledgeImage {
  id: string
  document_id: string
  url: string
  file_name: string
  caption: string
  enabled: boolean
  width: number
  height: number
}

export interface KnowledgeImageDraft {
  key: number
  file: File
  caption: string
}

export async function uploadKnowledgeImages(endpoint: string, drafts: readonly KnowledgeImageDraft[]) {
  const form = new FormData()
  for (const { file } of drafts) form.append('images', file)
  form.append('captions', JSON.stringify(drafts.map(({ caption }) => caption.trim())))
  const { data } = await api.post<{ images: KnowledgeImage[] }>(`${endpoint}/images`, form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  })
  return data.images
}
